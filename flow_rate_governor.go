// Package task limits inbound traffic with three chained token buckets, one
// per tenant, route and connection, and keeps the shared budget honest across
// borrowings and restarts.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the governor.
var (
	ErrBlankTenant    = errors.New("blank tenant")
	ErrBlankRoute     = errors.New("blank route")
	ErrBlankConn      = errors.New("blank connection")
	ErrBlankKey       = errors.New("blank key")
	ErrBadLevel       = errors.New("unknown level")
	ErrBadLimit       = errors.New("invalid limit")
	ErrZeroCost       = errors.New("zero cost")
	ErrNoLimit        = errors.New("no limit configured")
	ErrUnknownTicket  = errors.New("unknown ticket")
	ErrTicketMismatch = errors.New("ticket does not belong to this bucket")
	ErrBadSnapshot    = errors.New("snapshot rejected")
)

// Level names one of the three chained buckets.
type Level int

// The three chained bucket levels.
const (
	LevelTenant Level = iota
	LevelRoute
	LevelConn
)

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// borrowWindow is how long a borrowed amount may stay outstanding.
const borrowWindow = time.Second

// Decision is the outcome of one admission check.
type Decision struct {
	Allowed     bool
	Wait        time.Duration
	Remaining   uint64
	NeededBurst uint64
	Ticket      Ticket
}

// Ticket is the receipt of one admitted request and the only handle that can
// refund it.
type Ticket struct {
	ID     uint64
	Level  Level
	Key    string
	Cost   uint64
	Window int64
}

// LimitEntry is one configured bucket in a snapshot.
type LimitEntry struct {
	Level Level
	Key   string
	Rate  float64
	Burst uint64
}

// RemainderEntry is one bucket remainder in a snapshot.
type RemainderEntry struct {
	Level    Level
	Key      string
	Tokens   uint64
	Borrowed uint64
}

// GovernorSnapshot is the serialisable form of the whole governor.
type GovernorSnapshot struct {
	Version       int
	TakenAt       time.Time
	Limits        []LimitEntry
	Remainders    []RemainderEntry
	RefundRejects uint64
}

// bucket is one token bucket.
type bucket struct {
	rate     float64
	burst    uint64
	tokens   float64
	last     time.Time
	borrowed uint64
	due      time.Time
}

// FlowRateGovernor owns every bucket of every level.
type FlowRateGovernor struct {
	mu            sync.Mutex
	buckets       map[Level]map[string]*bucket
	tickets       map[uint64]Ticket
	nextTicket    uint64
	refundRejects uint64
}

// RouteKey builds the bucket key of a route below a tenant.
func RouteKey(tenant, route string) string {
	return tenant + "\x00" + route
}

// ConnKey builds the bucket key of a connection below a tenant and a route.
func ConnKey(tenant, route, conn string) string {
	return tenant + "\x00" + route + "\x00" + conn
}

// NewFlowRateGovernor builds an empty governor.
func NewFlowRateGovernor() *FlowRateGovernor {
	return &FlowRateGovernor{
		buckets: map[Level]map[string]*bucket{
			LevelTenant: {},
			LevelRoute:  {},
			LevelConn:   {},
		},
		tickets: make(map[uint64]Ticket),
	}
}

// SetLimit installs or updates the rate and burst of one bucket.
func (g *FlowRateGovernor) SetLimit(level Level, key string, rate float64, burst uint64) error {
	return errNotImplemented
}

// RemoveLimit drops one configured bucket.
func (g *FlowRateGovernor) RemoveLimit(level Level, key string) error {
	return errNotImplemented
}

// Allow admits or rejects one request and reports the retry delay on rejection.
func (g *FlowRateGovernor) Allow(tenant, route, conn string, cost uint64, now time.Time) (Decision, error) {
	return Decision{}, errNotImplemented
}

// Refund returns the cost of one admitted request to its own level.
func (g *FlowRateGovernor) Refund(t Ticket) error {
	return errNotImplemented
}

// Sweep refills every bucket and settles the borrowings that are due.
func (g *FlowRateGovernor) Sweep(now time.Time) {
}

// Snapshot captures limits and remainders for a later restart.
func (g *FlowRateGovernor) Snapshot() GovernorSnapshot {
	return GovernorSnapshot{}
}

// Restore rebuilds the governor from a snapshot after a restart.
func (g *FlowRateGovernor) Restore(s GovernorSnapshot) error {
	return errNotImplemented
}

// Remaining reports the tokens currently held by one bucket.
func (g *FlowRateGovernor) Remaining(level Level, key string) uint64 {
	return 0
}

// Borrowed reports the amount one bucket still owes from its own burst.
func (g *FlowRateGovernor) Borrowed(level Level, key string) uint64 {
	return 0
}

// RefundRejects reports how many refunds were rejected as cross level or unknown.
func (g *FlowRateGovernor) RefundRejects() uint64 {
	return 0
}

// BucketCount reports how many buckets one level currently holds.
func (g *FlowRateGovernor) BucketCount(level Level) int {
	return 0
}
