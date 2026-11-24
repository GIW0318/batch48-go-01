// Package task sits in front of a recursive resolver, coalesces concurrent
// identical DNS questions into one upstream lookup, caches negative answers
// with their own TTL and keeps per client token buckets isolated.
package task

import (
	"container/list"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Errors returned by the deduper.
var (
	ErrBadQuestion   = errors.New("invalid question")
	ErrBadClient     = errors.New("blank client id")
	ErrNXDomain      = errors.New("upstream answered nxdomain")
	ErrServfail      = errors.New("upstream answered servfail")
	ErrUpstream      = errors.New("upstream lookup failed")
	ErrTimeout       = errors.New("waiter timed out")
	ErrQuotaExceeded = errors.New("client quota exceeded")
	ErrBadQuota      = errors.New("invalid quota")
	ErrBadSnapshot   = errors.New("bad snapshot")
)

// Question types understood by the deduper.
const (
	TypeA    uint16 = 1
	TypeAAAA uint16 = 28
)

// DNS response codes understood by the deduper.
const (
	RCodeSuccess  = 0
	RCodeServfail = 2
	RCodeNXDomain = 3
)

const snapshotVersion = 1

// Question is one DNS lookup key.
type Question struct {
	Name string
	Type uint16
}

// Record is one resource record of an answer.
type Record struct {
	Data string
	TTL  uint32
}

// Answer is one upstream response together with the cache bookkeeping derived
// from it.
type Answer struct {
	Name      string
	Type      uint16
	RCode     int
	Records   []Record
	TTL       time.Duration
	ExpiresAt time.Time
}

// UpstreamFunc performs the real lookup behind the deduper.
type UpstreamFunc func(q Question) (Answer, error)

// DeduperOptions bounds negative caching, waiter patience and cache size.
type DeduperOptions struct {
	NegativeTTL     time.Duration
	ServfailTTL     time.Duration
	WaitTimeout     time.Duration
	MaxCacheEntries int
}

// DeduperStats reports aggregate counters.
type DeduperStats struct {
	Entries       int
	Flights       int
	UpstreamCalls int
	Clients       int
}

type cacheKey struct {
	name  string
	qtype uint16
}

type cachedEntry struct {
	key       cacheKey
	answer    Answer
	expiresAt time.Time
}

type flight struct {
	done   chan struct{}
	key    cacheKey
	q      Question
	now    time.Time
	answer Answer
	err    error
}

type quotaState struct {
	QPS    float64
	Burst  int
	Tokens float64
	Last   time.Time
}

type snapshotFile struct {
	Version int                   `json:"version"`
	Entries []snapshotEntry       `json:"entries"`
	Quotas  map[string]quotaState `json:"quotas"`
}

type snapshotEntry struct {
	Name      string    `json:"name"`
	Type      uint16    `json:"type"`
	Answer    Answer    `json:"answer"`
	ExpiresAt time.Time `json:"expires_at"`
}

// QueryDeduper owns the cache, the single flight groups and the client
// quotas.
type QueryDeduper struct {
	mu            sync.Mutex
	upstream      UpstreamFunc
	opts          DeduperOptions
	cache         map[cacheKey]*list.Element
	lru           *list.List
	flights       map[cacheKey]*flight
	quotas        map[string]*quotaState
	upstreamCalls int
}

// NewQueryDeduper builds a deduper around one upstream resolver.
func NewQueryDeduper(upstream UpstreamFunc, opts DeduperOptions) *QueryDeduper {
	return &QueryDeduper{
		upstream: upstream,
		opts:     opts,
		cache:    make(map[cacheKey]*list.Element),
		lru:      list.New(),
		flights:  make(map[cacheKey]*flight),
		quotas:   make(map[string]*quotaState),
	}
}

// Resolve answers one question, sharing in flight upstream lookups with every
// caller that asks the same name and type.
func (d *QueryDeduper) Resolve(q Question, clientID string, now time.Time) (Answer, error) {
	canonical := canonicalName(q.Name)
	if canonical == "" || q.Type == 0 {
		return Answer{}, ErrBadQuestion
	}
	if isBlank(clientID) {
		return Answer{}, ErrBadClient
	}

	key := cacheKey{name: canonical, qtype: q.Type}

	d.mu.Lock()
	if !d.allowLocked(clientID, now) {
		d.mu.Unlock()
		return Answer{}, ErrQuotaExceeded
	}

	if answer, err, ok := d.cachedLocked(key, now); ok {
		d.mu.Unlock()
		return answer, err
	}

	if group, ok := d.flights[key]; ok {
		d.mu.Unlock()
		return waitForFlight(group, d.opts.WaitTimeout)
	}

	group := &flight{
		done: make(chan struct{}),
		key:  key,
		q:    q,
		now:  now,
	}
	d.flights[key] = group
	d.upstreamCalls++
	d.mu.Unlock()

	go d.runFlight(group)
	return waitForFlight(group, d.opts.WaitTimeout)
}

// Invalidate removes every cached type of one name.
func (d *QueryDeduper) Invalidate(qname string) {
	canonical := canonicalName(qname)

	d.mu.Lock()
	defer d.mu.Unlock()

	for key, element := range d.cache {
		if key.name == canonical {
			d.removeElementLocked(element)
		}
	}
}

// SetQuota replaces the token bucket of one client.
func (d *QueryDeduper) SetQuota(clientID string, qps float64, burst int) error {
	if isBlank(clientID) {
		return ErrBadClient
	}
	if !validRate(qps) || burst < 1 {
		return ErrBadQuota
	}

	d.mu.Lock()
	d.quotas[clientID] = &quotaState{
		QPS:    qps,
		Burst:  burst,
		Tokens: float64(burst),
	}
	d.mu.Unlock()
	return nil
}

// Sweep removes expired cache entries and reports how many were removed.
func (d *QueryDeduper) Sweep(now time.Time) int {
	d.mu.Lock()
	defer d.mu.Unlock()

	removed := 0
	for element := d.lru.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*cachedEntry)
		if !now.Before(entry.expiresAt) {
			d.removeElementLocked(element)
			removed++
		}
		element = next
	}
	return removed
}

// CacheSize reports the number of cached entries.
func (d *QueryDeduper) CacheSize() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lru.Len()
}

// Stats reports cache, flight and upstream counters.
func (d *QueryDeduper) Stats() DeduperStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return DeduperStats{
		Entries:       d.lru.Len(),
		Flights:       len(d.flights),
		UpstreamCalls: d.upstreamCalls,
		Clients:       len(d.quotas),
	}
}

// Quota reports the configured token bucket of one client.
func (d *QueryDeduper) Quota(clientID string) (float64, int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	quota, ok := d.quotas[clientID]
	if !ok {
		return 0, 0, false
	}
	return quota.QPS, quota.Burst, true
}

// Snapshot serialises the cache entries and the quota windows.
func (d *QueryDeduper) Snapshot() ([]byte, error) {
	d.mu.Lock()
	snapshot := d.snapshotLocked()
	d.mu.Unlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return data, nil
}

// Restore replaces the cache and quota tables with a snapshot. Decoding and
// validation finish before any live state is changed, so a bad snapshot has
// no effect.
func (d *QueryDeduper) Restore(data []byte) error {
	snapshot, err := decodeSnapshot(data)
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.cache = snapshot.cache
	d.lru = snapshot.lru
	d.quotas = snapshot.quotas
	d.enforceCapacityLocked()
	d.mu.Unlock()
	return nil
}

type restoredSnapshot struct {
	cache  map[cacheKey]*list.Element
	lru    *list.List
	quotas map[string]*quotaState
}

func decodeSnapshot(data []byte) (restoredSnapshot, error) {
	var file snapshotFile
	if len(strings.TrimSpace(string(data))) == 0 {
		return restoredSnapshot{}, fmt.Errorf("%w: empty snapshot", ErrBadSnapshot)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return restoredSnapshot{}, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if file.Version != snapshotVersion {
		return restoredSnapshot{}, fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, file.Version)
	}

	result := restoredSnapshot{
		cache:  make(map[cacheKey]*list.Element, len(file.Entries)),
		lru:    list.New(),
		quotas: make(map[string]*quotaState, len(file.Quotas)),
	}

	for _, item := range file.Entries {
		name := canonicalName(item.Name)
		if name == "" || item.Type == 0 || item.ExpiresAt.IsZero() {
			return restoredSnapshot{}, fmt.Errorf("%w: invalid cache entry", ErrBadSnapshot)
		}
		if item.Answer.TTL < 0 || !knownRCode(item.Answer.RCode) {
			return restoredSnapshot{}, fmt.Errorf("%w: invalid answer", ErrBadSnapshot)
		}

		key := cacheKey{name: name, qtype: item.Type}
		if _, exists := result.cache[key]; exists {
			return restoredSnapshot{}, fmt.Errorf("%w: duplicate cache key", ErrBadSnapshot)
		}

		answer := cloneAnswer(item.Answer)
		answer.Name = name
		answer.Type = item.Type
		answer.ExpiresAt = item.ExpiresAt
		entry := &cachedEntry{
			key:       key,
			answer:    answer,
			expiresAt: item.ExpiresAt,
		}
		result.cache[key] = result.lru.PushBack(entry)
	}

	for clientID, quota := range file.Quotas {
		if isBlank(clientID) || !validRate(quota.QPS) || quota.Burst < 1 {
			return restoredSnapshot{}, fmt.Errorf("%w: invalid quota", ErrBadSnapshot)
		}
		if math.IsNaN(quota.Tokens) || quota.Tokens < 0 || quota.Tokens > float64(quota.Burst) {
			return restoredSnapshot{}, fmt.Errorf("%w: invalid token window", ErrBadSnapshot)
		}
		quotaCopy := quota
		result.quotas[clientID] = &quotaCopy
	}

	return result, nil
}

func (d *QueryDeduper) snapshotLocked() snapshotFile {
	file := snapshotFile{
		Version: snapshotVersion,
		Entries: make([]snapshotEntry, 0, d.lru.Len()),
		Quotas:  make(map[string]quotaState, len(d.quotas)),
	}

	for element := d.lru.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*cachedEntry)
		file.Entries = append(file.Entries, snapshotEntry{
			Name:      entry.key.name,
			Type:      entry.key.qtype,
			Answer:    cloneAnswer(entry.answer),
			ExpiresAt: entry.expiresAt,
		})
	}

	for clientID, quota := range d.quotas {
		file.Quotas[clientID] = *quota
	}
	return file
}

func (d *QueryDeduper) runFlight(group *flight) {
	rawAnswer, upstreamErr := d.callUpstream(group.q)

	d.mu.Lock()
	defer d.mu.Unlock()

	answer, err := normalizeResult(rawAnswer, upstreamErr, group.q, group.now, d.opts)
	group.answer = answer
	group.err = err

	if err == nil || errors.Is(err, ErrNXDomain) || errors.Is(err, ErrServfail) {
		d.storeLocked(group.key, answer)
	}

	delete(d.flights, group.key)
	close(group.done)
}

func (d *QueryDeduper) callUpstream(q Question) (answer Answer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			answer = Answer{}
			err = fmt.Errorf("%w: panic: %v", ErrUpstream, recovered)
		}
	}()
	if d.upstream == nil {
		return Answer{}, fmt.Errorf("%w: nil upstream function", ErrUpstream)
	}
	return d.upstream(q)
}

func normalizeResult(raw Answer, upstreamErr error, q Question, now time.Time, opts DeduperOptions) (Answer, error) {
	if upstreamErr != nil {
		return Answer{}, fmt.Errorf("%w: %w", ErrUpstream, upstreamErr)
	}

	answer := cloneAnswer(raw)
	if answer.Name == "" {
		answer.Name = q.Name
	}
	if answer.Type == 0 {
		answer.Type = q.Type
	}

	switch answer.RCode {
	case RCodeSuccess:
		answer.TTL = minimumRecordTTL(answer.Records)
		answer.ExpiresAt = now.Add(answer.TTL)
		return answer, nil
	case RCodeNXDomain:
		answer.TTL = opts.NegativeTTL
		answer.ExpiresAt = now.Add(opts.NegativeTTL)
		return answer, ErrNXDomain
	case RCodeServfail:
		answer.TTL = opts.ServfailTTL
		answer.ExpiresAt = now.Add(opts.ServfailTTL)
		return answer, ErrServfail
	default:
		answer.TTL = 0
		answer.ExpiresAt = time.Time{}
		return answer, fmt.Errorf("%w: unexpected rcode %d", ErrUpstream, answer.RCode)
	}
}

func minimumRecordTTL(records []Record) time.Duration {
	if len(records) == 0 {
		return 0
	}

	minimum := time.Duration(math.MaxInt64)
	for _, record := range records {
		ttl := time.Duration(record.TTL) * time.Second
		if ttl < minimum {
			minimum = ttl
		}
	}
	return minimum
}

func waitForFlight(group *flight, timeout time.Duration) (Answer, error) {
	select {
	case <-group.done:
		return group.answer, group.err
	default:
	}

	if timeout <= 0 {
		return Answer{}, ErrTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-group.done:
		return group.answer, group.err
	case <-timer.C:
		return Answer{}, ErrTimeout
	}
}

func (d *QueryDeduper) allowLocked(clientID string, now time.Time) bool {
	quota, configured := d.quotas[clientID]
	if !configured {
		return true
	}

	if now.After(quota.Last) {
		elapsed := now.Sub(quota.Last).Seconds()
		quota.Tokens = math.Min(
			float64(quota.Burst),
			quota.Tokens+quota.QPS*elapsed,
		)
		quota.Last = now
	}

	if quota.Tokens < 1 {
		return false
	}

	quota.Tokens--
	return true
}

func (d *QueryDeduper) cachedLocked(key cacheKey, now time.Time) (Answer, error, bool) {
	element, ok := d.cache[key]
	if !ok {
		return Answer{}, nil, false
	}

	entry := element.Value.(*cachedEntry)
	if !now.Before(entry.expiresAt) {
		d.removeElementLocked(element)
		return Answer{}, nil, false
	}

	d.lru.MoveToFront(element)
	answer := cloneAnswer(entry.answer)
	return answer, rcodeError(answer.RCode), true
}

func (d *QueryDeduper) storeLocked(key cacheKey, answer Answer) {
	stored := &cachedEntry{
		key:       key,
		answer:    cloneAnswer(answer),
		expiresAt: answer.ExpiresAt,
	}

	if element, ok := d.cache[key]; ok {
		element.Value = stored
		d.lru.MoveToFront(element)
	} else {
		d.cache[key] = d.lru.PushFront(stored)
	}
	d.enforceCapacityLocked()
}

func (d *QueryDeduper) enforceCapacityLocked() {
	if d.opts.MaxCacheEntries <= 0 {
		return
	}
	for d.lru.Len() > d.opts.MaxCacheEntries {
		oldest := d.lru.Back()
		if oldest == nil {
			break
		}
		d.removeElementLocked(oldest)
	}
}

func (d *QueryDeduper) removeElementLocked(element *list.Element) {
	entry := element.Value.(*cachedEntry)
	delete(d.cache, entry.key)
	d.lru.Remove(element)
}

func cloneAnswer(answer Answer) Answer {
	if answer.Records != nil {
		records := make([]Record, len(answer.Records))
		copy(records, answer.Records)
		answer.Records = records
	}
	return answer
}

func rcodeError(rcode int) error {
	switch rcode {
	case RCodeNXDomain:
		return ErrNXDomain
	case RCodeServfail:
		return ErrServfail
	default:
		return nil
	}
}

func knownRCode(rcode int) bool {
	return rcode == RCodeSuccess || rcode == RCodeNXDomain || rcode == RCodeServfail
}

func validRate(qps float64) bool {
	return qps > 0 && !math.IsNaN(qps) && !math.IsInf(qps, 0)
}

func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}

func canonicalName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, ".")
	return strings.ToLower(name)
}
