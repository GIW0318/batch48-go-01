package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// KeyframeRequester coalesces keyframe requests from many subscribers.
type KeyframeRequester struct {
	mu sync.Mutex
}

// NewKeyframeRequester builds a requester with the given configuration.
func NewKeyframeRequester(cfg Config) *KeyframeRequester {
	return &KeyframeRequester{}
}

// Request records one subscriber request and merges it with the pending batch.
func (r *KeyframeRequester) Request(req Request) error {
	return errNotImplemented
}

// Upstream returns the next upstream request that should be sent.
func (r *KeyframeRequester) Upstream(nowMs int64) (UpstreamRequest, bool) {
	return UpstreamRequest{}, false
}

// OnKeyframe reports a keyframe delivered for one stream and returns how many
// pending requests it satisfied.
func (r *KeyframeRequester) OnKeyframe(stream string, seq uint64) int {
	return 0
}

// OnSubscriberLeave cancels every pending request of one subscriber.
func (r *KeyframeRequester) OnSubscriberLeave(subscriber string) int {
	return 0
}

// Pending reports the number of pending subscriber requests.
func (r *KeyframeRequester) Pending() int {
	return 0
}

// PendingFor reports the number of pending requests of one stream.
func (r *KeyframeRequester) PendingFor(stream string) int {
	return 0
}

// Snapshot serialises throttle timestamps, pending items and counters.
func (r *KeyframeRequester) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the requester state from a snapshot.
func (r *KeyframeRequester) Restore(payload []byte) error {
	return errNotImplemented
}

// Stats reports the requester counters.
func (r *KeyframeRequester) Stats() RequesterStats {
	return RequesterStats{}
}

// Reset clears every pending item and counter.
func (r *KeyframeRequester) Reset() error {
	return errNotImplemented
}
