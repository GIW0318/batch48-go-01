// Package task turns a set of SRV records into a backend selector: weighted
// round robin inside the preferred priority, health suppression with a single
// half open probe, TTL staleness while waiting for a new record set and sticky
// keys that survive as long as the record set does.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the balancer.
var (
	ErrEmptyRecordSet     = errors.New("empty record set")
	ErrDuplicateTarget    = errors.New("duplicate target")
	ErrInvalidRecord      = errors.New("invalid srv record")
	ErrInvalidPolicy      = errors.New("invalid balancer policy")
	ErrNoRecords          = errors.New("no record set loaded")
	ErrNoHealthyBackend   = errors.New("no healthy backend")
	ErrUnknownTarget      = errors.New("unknown target")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// Target is one backend endpoint.
type Target struct {
	Host string
	Port uint16
}

// String renders the target as host:port.
func (t Target) String() string {
	return ""
}

// SRVRecord is one record of the service discovery set.
type SRVRecord struct {
	Host     string
	Port     uint16
	Priority uint16
	Weight   uint16
	TTL      time.Duration
}

// Policy controls health handling and the sticky key table.
type Policy struct {
	FailThreshold int
	Cooldown      time.Duration
	MaxKeys       int
}

// BalancerStats counts what the balancer did so far.
type BalancerStats struct {
	Records         int
	Picks           int
	Updates         int
	Stale           bool
	Suppressed      int
	Probes          int
	Migrations      int
	StickyKeys      int
	WeightZeroPicks int
}

// entry is the mutable side of one record.
type entry struct {
	target Target
}

// SRVBalancer serialises every selection of one record set.
type SRVBalancer struct {
	mu sync.Mutex
}

// NewSRVBalancer validates the policy and builds the balancer.
func NewSRVBalancer(p Policy) (*SRVBalancer, error) {
	return nil, errNotImplemented
}

// Update replaces the record set with a validated one.
func (b *SRVBalancer) Update(records []SRVRecord) error {
	return errNotImplemented
}

// Pick selects one backend for the caller key.
func (b *SRVBalancer) Pick(key string) (Target, error) {
	return Target{}, errNotImplemented
}

// ReportHealth records the outcome of one request against a target.
func (b *SRVBalancer) ReportHealth(target Target, ok bool) error {
	return errNotImplemented
}

// Expire advances the balancer clock and marks the record set stale when its
// TTL has passed.
func (b *SRVBalancer) Expire(now time.Time) {
}

// SetPolicy replaces the health policy.
func (b *SRVBalancer) SetPolicy(p Policy) error {
	return errNotImplemented
}

// Targets lists the loaded targets ordered by priority, host and port.
func (b *SRVBalancer) Targets() []Target {
	return nil
}

// Healthy reports whether the target is currently selectable.
func (b *SRVBalancer) Healthy(target Target) bool {
	return false
}

// Stats returns a copy of the balancer counters.
func (b *SRVBalancer) Stats() BalancerStats {
	return BalancerStats{}
}

// Snapshot serialises records, rotation state, health state and sticky keys.
func (b *SRVBalancer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the balancer state with a snapshot.
func (b *SRVBalancer) Restore(data []byte) error {
	return errNotImplemented
}
