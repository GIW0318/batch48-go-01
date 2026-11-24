// Package task sits in front of a recursive resolver, coalesces concurrent
// identical DNS questions into one upstream lookup, caches negative answers
// with their own TTL and keeps per client token buckets isolated.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

type cacheEntry struct {
	Answer    Answer
	ExpiresAt time.Time
}

type flight struct {
	Done   chan struct{}
	Answer Answer
	Err    error
}

type quotaState struct {
	QPS    float64
	Burst  int
	Tokens float64
	Last   time.Time
}

// QueryDeduper owns the cache, the single flight groups and the client
// quotas.
type QueryDeduper struct {
	mu       sync.Mutex
	upstream UpstreamFunc
	opts     DeduperOptions
	cache    map[string]cacheEntry
	flights  map[string]*flight
	quotas   map[string]*quotaState
	stats    DeduperStats
}

// NewQueryDeduper builds a deduper around one upstream resolver.
func NewQueryDeduper(upstream UpstreamFunc, opts DeduperOptions) *QueryDeduper {
	return &QueryDeduper{
		upstream: upstream,
		opts:     opts,
		cache:    make(map[string]cacheEntry),
		flights:  make(map[string]*flight),
		quotas:   make(map[string]*quotaState),
	}
}

// Resolve answers one question, sharing in flight upstream lookups with every
// caller that asks the same name and type.
func (d *QueryDeduper) Resolve(q Question, clientID string, now time.Time) (Answer, error) {
	return Answer{}, errNotImplemented
}

// Invalidate removes every cached type of one name.
func (d *QueryDeduper) Invalidate(qname string) {
}

// SetQuota replaces the token bucket of one client.
func (d *QueryDeduper) SetQuota(clientID string, qps float64, burst int) error {
	return errNotImplemented
}

// Sweep removes expired cache entries and reports how many were removed.
func (d *QueryDeduper) Sweep(now time.Time) int {
	return 0
}

// CacheSize reports the number of cached entries.
func (d *QueryDeduper) CacheSize() int {
	return 0
}

// Stats reports cache, flight and upstream counters.
func (d *QueryDeduper) Stats() DeduperStats {
	return DeduperStats{}
}

// Quota reports the configured token bucket of one client.
func (d *QueryDeduper) Quota(clientID string) (float64, int, bool) {
	return 0, 0, false
}

// Snapshot serialises the cache entries and the quota windows.
func (d *QueryDeduper) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the cache and quota tables with a snapshot.
func (d *QueryDeduper) Restore(data []byte) error {
	return errNotImplemented
}
