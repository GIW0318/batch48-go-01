package task

import (
	"errors"
	"time"
)

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors returned by the scheduler.
var (
	ErrInvalidTarget  = errors.New("scan_lease_scheduler: invalid target")
	ErrDuplicateTarget = errors.New("scan_lease_scheduler: duplicate target")
	ErrTargetLimit    = errors.New("scan_lease_scheduler: pending shard limit reached")
	ErrNoWork         = errors.New("scan_lease_scheduler: no pending shard")
	ErrUnknownLease   = errors.New("scan_lease_scheduler: unknown lease")
	ErrLeaseExpired   = errors.New("scan_lease_scheduler: lease expired")
	ErrInvalidResult  = errors.New("scan_lease_scheduler: result does not match lease")
	ErrBadSnapshot    = errors.New("scan_lease_scheduler: bad snapshot")
)

// Target is one scan destination split into shards.
type Target struct {
	ID       string
	Host     string
	Units    int
	Shards   int
	Priority int
}

// Shard is one dispatchable slice of a target.
type Shard struct {
	TargetID string
	Index    int
	Begin    int
	End      int
	Seq      uint64
	LeaseID  uint64
	Worker   string
	Expires  time.Time
}

// ShardResult is the report produced by a worker for one shard.
type ShardResult struct {
	TargetID string
	Index    int
	Seq      uint64
	Findings int
	Digest   string
}

// SchedulerOptions tunes lease lifetime and table capacities.
type SchedulerOptions struct {
	LeaseTTL           time.Duration
	MaxShardsPerTarget int
	MaxPending         int
	Now                func() time.Time
}

// SchedulerStats is a consistent view of the scheduling counters.
type SchedulerStats struct {
	Pending   int
	Leased    int
	Completed int
	Reclaimed int
	Enqueued  int
}

// ScanLeaseScheduler hands out exclusive leases over scan shards.
type ScanLeaseScheduler struct {
	opts SchedulerOptions
}

// NewScanLeaseScheduler builds a scheduler with the given options.
func NewScanLeaseScheduler(opts SchedulerOptions) *ScanLeaseScheduler {
	return &ScanLeaseScheduler{opts: opts}
}

// Enqueue registers a target and splits it into pending shards.
func (s *ScanLeaseScheduler) Enqueue(t Target) error {
	return errNotImplemented
}

// Lease hands the highest priority pending shard to a worker.
func (s *ScanLeaseScheduler) Lease(worker string) (*Shard, error) {
	return nil, errNotImplemented
}

// Renew extends the lease of an in-flight shard.
func (s *ScanLeaseScheduler) Renew(leaseID uint64) error {
	return errNotImplemented
}

// Complete records the result of a leased shard.
func (s *ScanLeaseScheduler) Complete(leaseID uint64, res ShardResult) error {
	return errNotImplemented
}

// Reclaim requeues every lease that expired at or before now.
func (s *ScanLeaseScheduler) Reclaim(now time.Time) int {
	return 0
}

// Reported returns the recorded result of a shard.
func (s *ScanLeaseScheduler) Reported(targetID string, index int) (ShardResult, bool) {
	return ShardResult{}, false
}

// Pending returns the number of shards waiting for a worker.
func (s *ScanLeaseScheduler) Pending() int {
	return 0
}

// InFlight returns the number of currently leased shards.
func (s *ScanLeaseScheduler) InFlight() int {
	return 0
}

// Results returns every recorded result ordered by target and index.
func (s *ScanLeaseScheduler) Results() []ShardResult {
	return nil
}

// Stats returns a consistent snapshot of the counters.
func (s *ScanLeaseScheduler) Stats() SchedulerStats {
	return SchedulerStats{}
}

// Snapshot serialises the scheduler state.
func (s *ScanLeaseScheduler) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a state produced by Snapshot.
func (s *ScanLeaseScheduler) Restore(data []byte) error {
	return errNotImplemented
}
