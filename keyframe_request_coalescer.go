package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Reasons reported on an upstream keyframe request.
const (
	ReasonCoalesced = "coalesced"
	ReasonEscalated = "escalated"
)

// Errors reported by the requester.
var (
	ErrRequestField = errors.New("request field is empty")
	ErrKeyRequired  = errors.New("request idempotency key is required")
	ErrThrottled    = errors.New("subscriber is inside its throttle window")
	ErrPendingCap   = errors.New("pending keyframe request cap reached")
	ErrStateCorrupt = errors.New("keyframe requester state is corrupt")
)

const (
	snapshotFormat    = "keyframe-requester-snapshot"
	snapshotVersion   = 1
	defaultPriority   = 1
	escalatedPriority = 2
)

// Request is one subscriber asking for a keyframe of one stream.
type Request struct {
	Subscriber string
	Stream     string
	Key        string
	StartSeq   uint64
	NowMs      int64
}

// UpstreamRequest is the coalesced request handed to the publisher.
type UpstreamRequest struct {
	Stream     string
	Seq        uint64
	Requesters int
	Priority   int
	Reason     string
	DeadlineMs int64
}

// Config configures throttling, timeouts and pending caps.
type Config struct {
	ThrottleMs          int64
	TimeoutMs           int64
	MaxPendingPerStream int
	MaxPendingTotal     int
}

// RequesterStats reports the counters of the requester.
type RequesterStats struct {
	Requests      uint64
	Coalesced     uint64
	Throttled     uint64
	LimitRejected uint64
	Upstream      uint64
	Satisfied     uint64
	Escalated     uint64
	Cancelled     uint64
	Pending       int
}

type pendingItem struct {
	Subscriber string `json:"subscriber"`
	Stream     string `json:"stream"`
	Key        string `json:"key"`
	StartSeq   uint64 `json:"start_seq"`
	RequestAt  int64  `json:"request_at_ms"`
}

type throttleKey struct {
	Subscriber string
	Stream     string
}

type dispatchState struct {
	LastDispatchSeq uint64 `json:"last_dispatch_seq"`
	DispatchedAtMs  int64  `json:"dispatched_at_ms"`
	DeadlineMs      int64  `json:"deadline_ms"`
	Priority        int    `json:"priority"`
	DispatchCount   uint64 `json:"dispatch_count"`
	Escalated       bool   `json:"escalated"`
}

type throttleSnapshot struct {
	Subscriber string `json:"subscriber"`
	Stream     string `json:"stream"`
	LastAtMs   int64  `json:"last_at_ms"`
}

type snapshotData struct {
	Format     string                   `json:"format"`
	Version    int                      `json:"version"`
	Config     Config                   `json:"config"`
	LastSeq    uint64                   `json:"last_seq"`
	Stats      RequesterStats           `json:"stats"`
	Throttles  []throttleSnapshot       `json:"throttles"`
	Pending    []pendingItem            `json:"pending"`
	SeenKeys   map[string]struct{}      `json:"seen_keys"`
	Dispatches map[string]dispatchState `json:"dispatches"`
}

// KeyframeRequester coalesces keyframe requests from many subscribers.
type KeyframeRequester struct {
	mu sync.Mutex

	cfg Config

	pending     map[string]*pendingItem
	streamIndex map[string]map[string]struct{}
	subIndex    map[string]map[string]struct{}
	throttles   map[throttleKey]int64
	seenKeys    map[string]struct{}
	dispatches  map[string]*dispatchState

	lastSeq uint64
	stats   RequesterStats
}

// NewKeyframeRequester builds a requester with the given configuration.
func NewKeyframeRequester(cfg Config) *KeyframeRequester {
	if cfg.ThrottleMs < 0 {
		cfg.ThrottleMs = 0
	}
	if cfg.TimeoutMs < 0 {
		cfg.TimeoutMs = 0
	}
	return &KeyframeRequester{
		cfg:         cfg,
		pending:     make(map[string]*pendingItem),
		streamIndex: make(map[string]map[string]struct{}),
		subIndex:    make(map[string]map[string]struct{}),
		throttles:   make(map[throttleKey]int64),
		seenKeys:    make(map[string]struct{}),
		dispatches:  make(map[string]*dispatchState),
	}
}

func (r *KeyframeRequester) ensureStateLocked() {
	if r.pending == nil {
		r.pending = make(map[string]*pendingItem)
	}
	if r.streamIndex == nil {
		r.streamIndex = make(map[string]map[string]struct{})
	}
	if r.subIndex == nil {
		r.subIndex = make(map[string]map[string]struct{})
	}
	if r.throttles == nil {
		r.throttles = make(map[throttleKey]int64)
	}
	if r.seenKeys == nil {
		r.seenKeys = make(map[string]struct{})
	}
	if r.dispatches == nil {
		r.dispatches = make(map[string]*dispatchState)
	}
}

// Request records one subscriber request and merges it with the pending batch.
func (r *KeyframeRequester) Request(req Request) error {
	if req.Subscriber == "" {
		return ErrRequestField
	}
	if req.Stream == "" {
		return ErrRequestField
	}
	if req.Key == "" {
		return ErrKeyRequired
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStateLocked()

	if _, exists := r.seenKeys[req.Key]; exists {
		r.stats.Coalesced++
		return nil
	}

	throttle := throttleKey{Subscriber: req.Subscriber, Stream: req.Stream}
	if last, ok := r.throttles[throttle]; ok && req.NowMs-last < r.cfg.ThrottleMs {
		r.stats.Throttled++
		return ErrThrottled
	}

	streamPending := len(r.streamIndex[req.Stream])
	totalPending := len(r.pending)
	if r.cfg.MaxPendingPerStream > 0 && streamPending >= r.cfg.MaxPendingPerStream {
		r.stats.LimitRejected++
		return ErrPendingCap
	}
	if r.cfg.MaxPendingTotal > 0 && totalPending >= r.cfg.MaxPendingTotal {
		r.stats.LimitRejected++
		return ErrPendingCap
	}

	item := &pendingItem{
		Subscriber: req.Subscriber,
		Stream:     req.Stream,
		Key:        req.Key,
		StartSeq:   req.StartSeq,
		RequestAt:  req.NowMs,
	}
	r.pending[req.Key] = item
	r.seenKeys[req.Key] = struct{}{}

	if r.streamIndex[req.Stream] == nil {
		r.streamIndex[req.Stream] = make(map[string]struct{})
	}
	r.streamIndex[req.Stream][req.Key] = struct{}{}

	if r.subIndex[req.Subscriber] == nil {
		r.subIndex[req.Subscriber] = make(map[string]struct{})
	}
	r.subIndex[req.Subscriber][req.Key] = struct{}{}

	r.throttles[throttle] = req.NowMs
	r.stats.Requests++
	return nil
}

// Upstream returns the next upstream request that should be sent.
func (r *KeyframeRequester) Upstream(nowMs int64) (UpstreamRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStateLocked()

	bestStream := ""
	bestEarliest := int64(0)
	haveCandidate := false

	for stream, keys := range r.streamIndex {
		if len(keys) == 0 {
			continue
		}

		dispatch := r.dispatches[stream]
		if dispatch != nil && nowMs < dispatch.DeadlineMs {
			continue
		}

		earliest, ok := r.earliestRequestLocked(keys)
		if !ok {
			continue
		}
		if !haveCandidate || earliest < bestEarliest || (earliest == bestEarliest && stream < bestStream) {
			bestStream = stream
			bestEarliest = earliest
			haveCandidate = true
		}
	}

	if !haveCandidate {
		return UpstreamRequest{}, false
	}

	r.lastSeq++
	seq := r.lastSeq
	dispatch := r.dispatches[bestStream]
	reason := ReasonCoalesced
	priority := defaultPriority

	if dispatch == nil {
		dispatch = &dispatchState{Priority: defaultPriority}
		r.dispatches[bestStream] = dispatch
	} else {
		reason = ReasonEscalated
		if !dispatch.Escalated {
			dispatch.Escalated = true
			dispatch.Priority = escalatedPriority
			r.stats.Escalated++
		}
		priority = dispatch.Priority
	}

	deadline := nowMs + r.cfg.TimeoutMs
	dispatch.LastDispatchSeq = seq
	dispatch.DispatchedAtMs = nowMs
	dispatch.DeadlineMs = deadline
	dispatch.Priority = priority
	dispatch.DispatchCount++

	r.stats.Upstream++

	return UpstreamRequest{
		Stream:     bestStream,
		Seq:        seq,
		Requesters: len(r.streamIndex[bestStream]),
		Priority:   priority,
		Reason:     reason,
		DeadlineMs: deadline,
	}, true
}

func (r *KeyframeRequester) earliestRequestLocked(keys map[string]struct{}) (int64, bool) {
	earliest := int64(0)
	found := false
	for key := range keys {
		item := r.pending[key]
		if item == nil {
			continue
		}
		if !found || item.RequestAt < earliest {
			earliest = item.RequestAt
			found = true
		}
	}
	return earliest, found
}

// OnKeyframe reports a keyframe delivered for one stream and returns how many
// pending requests it satisfied.
func (r *KeyframeRequester) OnKeyframe(stream string, seq uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStateLocked()

	keys := r.streamIndex[stream]
	satisfied := 0
	for key := range keys {
		item := r.pending[key]
		if item == nil || seq <= item.StartSeq {
			continue
		}
		r.removePendingLocked(key)
		satisfied++
	}

	if satisfied > 0 {
		r.stats.Satisfied += uint64(satisfied)
	}
	return satisfied
}

// OnSubscriberLeave cancels every pending request of one subscriber.
func (r *KeyframeRequester) OnSubscriberLeave(subscriber string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStateLocked()

	keys := r.subIndex[subscriber]
	cancelled := len(keys)
	if cancelled == 0 {
		return 0
	}

	for key := range keys {
		r.removePendingLocked(key)
	}
	r.stats.Cancelled += uint64(cancelled)
	return cancelled
}

func (r *KeyframeRequester) removePendingLocked(key string) {
	item, ok := r.pending[key]
	if !ok || item == nil {
		return
	}

	delete(r.pending, key)

	if keys := r.streamIndex[item.Stream]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 {
			delete(r.streamIndex, item.Stream)
			delete(r.dispatches, item.Stream)
		}
	}

	if keys := r.subIndex[item.Subscriber]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 {
			delete(r.subIndex, item.Subscriber)
		}
	}
}

// Pending reports the number of pending subscriber requests.
func (r *KeyframeRequester) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// PendingFor reports the number of pending requests of one stream.
func (r *KeyframeRequester) PendingFor(stream string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streamIndex[stream])
}

// Snapshot serialises throttle timestamps, pending items and counters.
func (r *KeyframeRequester) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStateLocked()

	data := snapshotData{
		Format:     snapshotFormat,
		Version:    snapshotVersion,
		Config:     r.cfg,
		LastSeq:    r.lastSeq,
		Stats:      r.stats,
		Throttles:  make([]throttleSnapshot, 0, len(r.throttles)),
		Pending:    make([]pendingItem, 0, len(r.pending)),
		SeenKeys:   make(map[string]struct{}, len(r.seenKeys)),
		Dispatches: make(map[string]dispatchState, len(r.dispatches)),
	}
	data.Stats.Pending = len(r.pending)

	for key, at := range r.throttles {
		data.Throttles = append(data.Throttles, throttleSnapshot{
			Subscriber: key.Subscriber,
			Stream:     key.Stream,
			LastAtMs:   at,
		})
	}
	sort.Slice(data.Throttles, func(i, j int) bool {
		if data.Throttles[i].Subscriber != data.Throttles[j].Subscriber {
			return data.Throttles[i].Subscriber < data.Throttles[j].Subscriber
		}
		return data.Throttles[i].Stream < data.Throttles[j].Stream
	})

	for _, item := range r.pending {
		data.Pending = append(data.Pending, *item)
	}
	sort.Slice(data.Pending, func(i, j int) bool {
		if data.Pending[i].RequestAt != data.Pending[j].RequestAt {
			return data.Pending[i].RequestAt < data.Pending[j].RequestAt
		}
		if data.Pending[i].Stream != data.Pending[j].Stream {
			return data.Pending[i].Stream < data.Pending[j].Stream
		}
		return data.Pending[i].Key < data.Pending[j].Key
	})

	for key := range r.seenKeys {
		data.SeenKeys[key] = struct{}{}
	}
	for stream, dispatch := range r.dispatches {
		if dispatch != nil {
			data.Dispatches[stream] = *dispatch
		}
	}

	payload, err := json.Marshal(&data)
	if err != nil {
		return nil, fmt.Errorf("encode keyframe requester snapshot: %w", err)
	}
	return payload, nil
}

// Restore rebuilds the requester state from a snapshot.
func (r *KeyframeRequester) Restore(payload []byte) error {
	data, err := decodeSnapshot(payload)
	if err != nil {
		return err
	}
	if err := validateSnapshot(data); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.cfg = data.Config
	if r.cfg.ThrottleMs < 0 {
		r.cfg.ThrottleMs = 0
	}
	if r.cfg.TimeoutMs < 0 {
		r.cfg.TimeoutMs = 0
	}

	r.pending = make(map[string]*pendingItem, len(data.Pending))
	r.streamIndex = make(map[string]map[string]struct{})
	r.subIndex = make(map[string]map[string]struct{})
	r.throttles = make(map[throttleKey]int64, len(data.Throttles))
	r.seenKeys = make(map[string]struct{}, len(data.SeenKeys))
	r.dispatches = make(map[string]*dispatchState, len(data.Dispatches))
	r.lastSeq = data.LastSeq
	r.stats = data.Stats
	r.stats.Pending = len(data.Pending)

	for key := range data.SeenKeys {
		r.seenKeys[key] = struct{}{}
	}

	for i := range data.Pending {
		item := data.Pending[i]
		r.pending[item.Key] = &item

		if r.streamIndex[item.Stream] == nil {
			r.streamIndex[item.Stream] = make(map[string]struct{})
		}
		r.streamIndex[item.Stream][item.Key] = struct{}{}

		if r.subIndex[item.Subscriber] == nil {
			r.subIndex[item.Subscriber] = make(map[string]struct{})
		}
		r.subIndex[item.Subscriber][item.Key] = struct{}{}
	}

	for _, throttle := range data.Throttles {
		r.throttles[throttleKey{Subscriber: throttle.Subscriber, Stream: throttle.Stream}] = throttle.LastAtMs
	}

	for stream, dispatch := range data.Dispatches {
		dispatchCopy := dispatch
		r.dispatches[stream] = &dispatchCopy
	}

	return nil
}

func decodeSnapshot(payload []byte) (*snapshotData, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, fmt.Errorf("%w: empty snapshot", ErrStateCorrupt)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	var data snapshotData
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("%w: decode snapshot: %v", ErrStateCorrupt, err)
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%w: multiple JSON values", ErrStateCorrupt)
		}
		return nil, fmt.Errorf("%w: decode trailing snapshot data: %v", ErrStateCorrupt, err)
	}

	return &data, nil
}

func validateSnapshot(data *snapshotData) error {
	if data == nil {
		return fmt.Errorf("%w: missing snapshot", ErrStateCorrupt)
	}
	if data.Format != snapshotFormat {
		return fmt.Errorf("%w: unexpected snapshot format", ErrStateCorrupt)
	}
	if data.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported snapshot version", ErrStateCorrupt)
	}
	if data.Stats.Pending != len(data.Pending) {
		return fmt.Errorf("%w: pending stats mismatch", ErrStateCorrupt)
	}

	seenPendingKeys := make(map[string]struct{}, len(data.Pending))
	streamCounts := make(map[string]int, len(data.Pending))
	for _, item := range data.Pending {
		if item.Subscriber == "" || item.Stream == "" || item.Key == "" {
			return fmt.Errorf("%w: incomplete pending item", ErrStateCorrupt)
		}
		if _, duplicate := seenPendingKeys[item.Key]; duplicate {
			return fmt.Errorf("%w: duplicate pending key", ErrStateCorrupt)
		}
		seenPendingKeys[item.Key] = struct{}{}
		streamCounts[item.Stream]++

		if _, known := data.SeenKeys[item.Key]; !known {
			return fmt.Errorf("%w: pending key missing idempotency record", ErrStateCorrupt)
		}
	}

	for key := range data.SeenKeys {
		if key == "" {
			return fmt.Errorf("%w: empty idempotency key", ErrStateCorrupt)
		}
	}

	throttlePairs := make(map[throttleKey]struct{}, len(data.Throttles))
	for _, throttle := range data.Throttles {
		if throttle.Subscriber == "" || throttle.Stream == "" {
			return fmt.Errorf("%w: incomplete throttle record", ErrStateCorrupt)
		}
		pair := throttleKey{Subscriber: throttle.Subscriber, Stream: throttle.Stream}
		if _, duplicate := throttlePairs[pair]; duplicate {
			return fmt.Errorf("%w: duplicate throttle record", ErrStateCorrupt)
		}
		throttlePairs[pair] = struct{}{}
	}

	for stream, dispatch := range data.Dispatches {
		if stream == "" {
			return fmt.Errorf("%w: empty dispatched stream", ErrStateCorrupt)
		}
		if streamCounts[stream] == 0 {
			return fmt.Errorf("%w: dispatch without pending items", ErrStateCorrupt)
		}
		if dispatch.DispatchCount == 0 || dispatch.LastDispatchSeq == 0 {
			return fmt.Errorf("%w: invalid dispatch sequence", ErrStateCorrupt)
		}
		if dispatch.LastDispatchSeq > data.LastSeq {
			return fmt.Errorf("%w: dispatch sequence exceeds global sequence", ErrStateCorrupt)
		}
		if dispatch.Priority < defaultPriority {
			return fmt.Errorf("%w: invalid dispatch priority", ErrStateCorrupt)
		}
	}

	return nil
}

// Stats reports the requester counters.
func (r *KeyframeRequester) Stats() RequesterStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending != nil {
		r.stats.Pending = len(r.pending)
	}
	return r.stats
}

// Reset clears every pending item and counter.
func (r *KeyframeRequester) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pending = make(map[string]*pendingItem)
	r.streamIndex = make(map[string]map[string]struct{})
	r.subIndex = make(map[string]map[string]struct{})
	r.throttles = make(map[throttleKey]int64)
	r.seenKeys = make(map[string]struct{})
	r.dispatches = make(map[string]*dispatchState)
	r.lastSeq = 0
	r.stats = RequesterStats{}
	return nil
}
