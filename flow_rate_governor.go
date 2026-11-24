// Package task limits inbound traffic with three chained token buckets, one
// per tenant, route and connection, and keeps the shared budget honest across
// borrowings and restarts.
package task

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

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

// errNotImplemented is retained as the scaffold's internal sentinel.
var errNotImplemented = errors.New("not implemented")

// ErrBucketLimit is returned when a level has reached its bucket cap. It wraps
// ErrBadLimit so callers may classify it either way.
var ErrBucketLimit = fmt.Errorf("%w: bucket count limit reached", ErrBadLimit)

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

// MaxBucketsPerLevel bounds how many distinct buckets each level may hold.
// New keys past the cap are rejected instead of partially written.
const MaxBucketsPerLevel = 1000

// tokenEpsilon absorbs floating point dust from elapsed-time refill math so an
// exactly refilled bucket is not rejected or recorded with a phantom debt.
const tokenEpsilon = 1e-9

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

// refundLeg records the exact mutation one admission applied to one level so a
// refund can reverse it at the buckets that originally paid.
type refundLeg struct {
	Level    Level
	Key      string
	Tokens   float64
	Borrowed uint64
}

// refundReceipt is the server side detail behind a Ticket.
type refundReceipt struct {
	ticket Ticket
	legs   []refundLeg
}

// FlowRateGovernor owns every bucket of every level.
type FlowRateGovernor struct {
	mu            sync.Mutex
	buckets       map[Level]map[string]*bucket
	tickets       map[uint64]Ticket
	receipts      map[uint64]*refundReceipt
	nextTicket    uint64
	refundRejects uint64
	clock         time.Time
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
		tickets:  make(map[uint64]Ticket),
		receipts: make(map[uint64]*refundReceipt),
	}
}

func validLevel(level Level) bool {
	switch level {
	case LevelTenant, LevelRoute, LevelConn:
		return true
	default:
		return false
	}
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func validRate(rate float64) bool {
	return rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

// initLocked makes the governor usable even from a zero value. Callers must
// hold g.mu.
func (g *FlowRateGovernor) initLocked() {
	if g.buckets == nil {
		g.buckets = make(map[Level]map[string]*bucket)
	}
	for _, level := range []Level{LevelTenant, LevelRoute, LevelConn} {
		if g.buckets[level] == nil {
			g.buckets[level] = make(map[string]*bucket)
		}
	}
	if g.tickets == nil {
		g.tickets = make(map[uint64]Ticket)
	}
	if g.receipts == nil {
		g.receipts = make(map[uint64]*refundReceipt)
	}
}

// SetLimit installs or updates the rate and burst of one bucket.
func (g *FlowRateGovernor) SetLimit(level Level, key string, rate float64, burst uint64) error {
	if !validLevel(level) {
		return ErrBadLevel
	}
	if blank(key) {
		return ErrBlankKey
	}
	if !validRate(rate) || burst == 0 {
		return ErrBadLimit
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	levelBuckets := g.buckets[level]
	if levelBuckets == nil {
		levelBuckets = make(map[string]*bucket)
		g.buckets[level] = levelBuckets
	}

	if existing, ok := levelBuckets[key]; ok {
		existing.rate = rate
		existing.burst = burst
		if existing.tokens > float64(burst) {
			existing.tokens = float64(burst)
		}
		if existing.borrowed > burst {
			existing.borrowed = burst
		}
		return nil
	}

	if len(levelBuckets) >= MaxBucketsPerLevel {
		return ErrBucketLimit
	}

	// A freshly installed bucket starts full.
	levelBuckets[key] = &bucket{
		rate:   rate,
		burst:  burst,
		tokens: float64(burst),
	}
	return nil
}

// RemoveLimit drops one configured bucket. It is idempotent for a valid level.
func (g *FlowRateGovernor) RemoveLimit(level Level, key string) error {
	if !validLevel(level) {
		return ErrBadLevel
	}
	if blank(key) {
		return ErrBlankKey
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	delete(g.buckets[level], key)
	return nil
}

// touchClock records the newest caller supplied observation time without ever
// moving the clock backwards.
func (g *FlowRateGovernor) touchClock(now time.Time) {
	if !now.IsZero() && now.After(g.clock) {
		g.clock = now
	}
}

// settleBorrow applies the window boundary rule: tokens on hand repay as much
// debt as possible and any unpaid remainder is forgiven instead of rolling
// into the next window.
func settleBorrow(b *bucket, now time.Time) {
	if b.borrowed == 0 {
		return
	}
	if !b.due.IsZero() && now.Before(b.due) {
		return
	}

	pay := b.tokens
	if pay < 0 {
		pay = 0
	}
	if debt := float64(b.borrowed); pay > debt {
		pay = debt
	}
	b.tokens -= pay
	if b.tokens < 0 {
		b.tokens = 0
	}
	// The unpaid part does not cross the window boundary.
	b.borrowed = 0
	b.due = time.Time{}
}

// advance refills a bucket up to now using the caller's clock. A stale
// timestamp neither mints tokens nor moves the refill origin backwards.
func (g *FlowRateGovernor) advance(b *bucket, now time.Time) {
	if b == nil {
		return
	}
	switch {
	case b.last.IsZero():
		// First controlled observation establishes the baseline while the
		// bucket keeps the balance it was created/restored with.
		b.last = now
	case now.After(b.last):
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 && b.rate > 0 {
			b.tokens += elapsed * b.rate
			if capTokens := float64(b.burst); b.tokens > capTokens {
				b.tokens = capTokens
			}
		}
		b.last = now
	default:
		// now is equal to or before the refill origin: do nothing.
	}
	settleBorrow(b, now)
}

func remainingTokens(b *bucket) uint64 {
	if b == nil || b.tokens <= 0 {
		return 0
	}
	return uint64(math.Floor(b.tokens))
}

type legRef struct {
	level Level
	key   string
	b     *bucket
}

type admissionLeg struct {
	ref    legRef
	spend  float64
	borrow uint64
	wait   time.Duration
	needed uint64
	admit  bool
}

// evaluateLeg refills one leg and decides whether cost fits, including the
// bounded borrowing facility of child levels. It never mutates the bucket
// balance; callers commit the returned plan only after every leg passes.
func evaluateLeg(l legRef, cost uint64, now time.Time) admissionLeg {
	b := l.b
	costTokens := float64(cost)
	plan := admissionLeg{ref: l}

	// Natural refill delay for the raw remainder deficit. Every remainder
	// shortfall contributes and the caller keeps the longest wait.
	if b.tokens+tokenEpsilon < costTokens && b.rate > 0 {
		deficit := costTokens - b.tokens
		if deficit < 0 {
			deficit = 0
		}
		nanos := math.Round((deficit / b.rate) * float64(time.Second))
		if nanos < 1 {
			nanos = 1
		}
		plan.wait = time.Duration(nanos)
	}

	// Tenants are the hard shared budget and never borrow. Route and
	// connection children may owe at most one extra burst in total.
	borrowAllowed := l.level != LevelTenant
	ceiling := b.burst
	if borrowAllowed {
		if b.burst <= (math.MaxUint64 / 2) {
			ceiling = b.burst * 2
		} else {
			ceiling = math.MaxUint64
		}
	}
	if cost > ceiling {
		plan.needed = cost
		return plan
	}

	var headroom uint64
	if borrowAllowed && b.borrowed < b.burst {
		headroom = b.burst - b.borrowed
	}
	if b.tokens+float64(headroom)+tokenEpsilon < costTokens {
		return plan
	}

	// The remainder alone covers the request.
	if b.tokens+tokenEpsilon >= costTokens {
		plan.spend = costTokens
		plan.admit = true
		return plan
	}

	// Spend every available token and put the integer remainder on the
	// borrowing ledger. Rounding the deficit up keeps the ledger integral and
	// never leaves the bucket negative.
	missing := costTokens - b.tokens
	borrow := uint64(math.Ceil(missing - tokenEpsilon))
	if borrow == 0 {
		borrow = 1
	}
	if borrow > headroom {
		return plan
	}
	spend := costTokens - float64(borrow)
	if spend < 0 {
		spend = 0
	}
	if spend > b.tokens {
		spend = b.tokens
	}

	plan.spend = spend
	plan.borrow = borrow
	plan.admit = true
	return plan
}

// Allow admits or rejects one request and reports the retry delay on rejection.
func (g *FlowRateGovernor) Allow(tenant, route, conn string, cost uint64, now time.Time) (Decision, error) {
	if blank(tenant) {
		return Decision{}, ErrBlankTenant
	}
	if blank(route) {
		return Decision{}, ErrBlankRoute
	}
	if blank(conn) {
		return Decision{}, ErrBlankConn
	}
	if cost == 0 {
		return Decision{}, ErrZeroCost
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.touchClock(now)

	tenantKey := tenant
	routeKey := RouteKey(tenant, route)
	connKey := ConnKey(tenant, route, conn)

	refs := [3]legRef{
		{level: LevelTenant, key: tenantKey, b: g.buckets[LevelTenant][tenantKey]},
		{level: LevelRoute, key: routeKey, b: g.buckets[LevelRoute][routeKey]},
		{level: LevelConn, key: connKey, b: g.buckets[LevelConn][connKey]},
	}
	for _, ref := range refs {
		if ref.b == nil {
			return Decision{}, ErrNoLimit
		}
	}

	plans := [3]admissionLeg{}
	allAdmitted := true
	longestWait := time.Duration(0)
	var neededBurst uint64
	for i, ref := range refs {
		g.advance(ref.b, now)
		plan := evaluateLeg(ref, cost, now)
		plans[i] = plan
		if plan.wait > longestWait {
			longestWait = plan.wait
		}
		if plan.needed != 0 {
			neededBurst = cost
		}
		if !plan.admit {
			allAdmitted = false
		}
	}

	tenantRemainder := remainingTokens(refs[0].b)

	if !allAdmitted {
		// No plan is committed: none of the three balances or ledgers change.
		return Decision{
			Allowed:     false,
			Wait:        longestWait,
			Remaining:   tenantRemainder,
			NeededBurst: neededBurst,
		}, nil
	}

	legs := make([]refundLeg, 0, len(plans))
	for _, plan := range plans {
		b := plan.ref.b
		b.tokens -= plan.spend
		if b.tokens < 0 {
			b.tokens = 0
		}
		if plan.borrow > 0 {
			b.borrowed += plan.borrow
			if b.borrowed > b.burst {
				b.borrowed = b.burst
			}
			if b.due.IsZero() {
				b.due = now.Add(borrowWindow)
			}
		}
		legs = append(legs, refundLeg{
			Level:    plan.ref.level,
			Key:      plan.ref.key,
			Tokens:   plan.spend,
			Borrowed: plan.borrow,
		})
	}

	g.nextTicket++
	id := g.nextTicket
	var window int64
	if !now.IsZero() {
		window = now.UnixNano() / int64(borrowWindow)
	}
	ticket := Ticket{
		ID:     id,
		Level:  LevelTenant,
		Key:    tenantKey,
		Cost:   cost,
		Window: window,
	}
	g.tickets[id] = ticket
	g.receipts[id] = &refundReceipt{ticket: ticket, legs: legs}

	return Decision{
		Allowed:   true,
		Remaining: remainingTokens(refs[0].b),
		Ticket:    ticket,
	}, nil
}

// Refund returns the cost of one admitted request to its own levels.
func (g *FlowRateGovernor) Refund(t Ticket) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	stored, ok := g.tickets[t.ID]
	if t.ID == 0 || !ok {
		g.refundRejects++
		return fmt.Errorf("%w: id %d", ErrUnknownTicket, t.ID)
	}

	// A ticket may only be presented for the bucket ownership it was issued
	// with; moving the refund to another level/key is rejected.
	if t.Level != stored.Level || t.Key != stored.Key {
		g.refundRejects++
		return fmt.Errorf("%w: ticket %d presented as level %d key %q",
			ErrTicketMismatch, t.ID, t.Level, t.Key)
	}

	if receipt := g.receipts[t.ID]; receipt != nil {
		for _, leg := range receipt.legs {
			b := g.buckets[leg.Level][leg.Key]
			if b == nil {
				continue
			}
			b.tokens += leg.Tokens
			if capTokens := float64(b.burst); b.tokens > capTokens {
				b.tokens = capTokens
			}
			if b.tokens < 0 {
				b.tokens = 0
			}
			if leg.Borrowed > 0 {
				if b.borrowed >= leg.Borrowed {
					b.borrowed -= leg.Borrowed
				} else {
					b.borrowed = 0
				}
				if b.borrowed == 0 {
					b.due = time.Time{}
				}
			}
		}
	}

	// Consuming the receipt makes a second presentation unknown.
	delete(g.tickets, t.ID)
	delete(g.receipts, t.ID)
	return nil
}

// Sweep refills every bucket and settles the borrowings that are due.
func (g *FlowRateGovernor) Sweep(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.touchClock(now)
	for _, levelBuckets := range g.buckets {
		for _, b := range levelBuckets {
			g.advance(b, now)
		}
	}
}

// Snapshot captures limits and remainders for a later restart.
func (g *FlowRateGovernor) Snapshot() GovernorSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	// Bring every bucket up to the timestamp advertised by the snapshot so the
	// stored remainders and TakenAt are mutually consistent.
	if !g.clock.IsZero() {
		for _, levelBuckets := range g.buckets {
			for _, b := range levelBuckets {
				g.advance(b, g.clock)
			}
		}
	}

	snapshot := GovernorSnapshot{
		Version:       snapshotVersion,
		TakenAt:       g.clock,
		RefundRejects: g.refundRejects,
	}

	for _, level := range []Level{LevelTenant, LevelRoute, LevelConn} {
		levelBuckets := g.buckets[level]
		keys := make([]string, 0, len(levelBuckets))
		for key := range levelBuckets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b := levelBuckets[key]
			snapshot.Limits = append(snapshot.Limits, LimitEntry{
				Level: level,
				Key:   key,
				Rate:  b.rate,
				Burst: b.burst,
			})
			snapshot.Remainders = append(snapshot.Remainders, RemainderEntry{
				Level:    level,
				Key:      key,
				Tokens:   remainingTokens(b),
				Borrowed: b.borrowed,
			})
		}
	}
	return snapshot
}

// Restore rebuilds the governor from a snapshot after a restart. The snapshot
// is fully validated and built locally first; g is only replaced once every
// entry is accepted, so a bad snapshot leaves the running state untouched.
func (g *FlowRateGovernor) Restore(snapshot GovernorSnapshot) error {
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("%w: version %d, want %d",
			ErrBadSnapshot, snapshot.Version, snapshotVersion)
	}

	next := map[Level]map[string]*bucket{
		LevelTenant: {},
		LevelRoute:  {},
		LevelConn:   {},
	}

	for _, entry := range snapshot.Limits {
		if !validLevel(entry.Level) || blank(entry.Key) {
			return fmt.Errorf("%w: invalid limit entry", ErrBadSnapshot)
		}
		if !validRate(entry.Rate) || entry.Burst == 0 {
			return fmt.Errorf("%w: invalid limit for %q", ErrBadSnapshot, entry.Key)
		}
		levelBuckets := next[entry.Level]
		if _, exists := levelBuckets[entry.Key]; !exists &&
			len(levelBuckets) >= MaxBucketsPerLevel {
			return fmt.Errorf("%w: bucket cap at level %d", ErrBadSnapshot, entry.Level)
		}
		levelBuckets[entry.Key] = &bucket{
			rate:   entry.Rate,
			burst:  entry.Burst,
			tokens: float64(entry.Burst),
			last:   snapshot.TakenAt,
		}
	}

	for _, entry := range snapshot.Remainders {
		if !validLevel(entry.Level) || blank(entry.Key) {
			return fmt.Errorf("%w: invalid remainder entry", ErrBadSnapshot)
		}
		b := next[entry.Level][entry.Key]
		if b == nil {
			return fmt.Errorf("%w: remainder without limit for %q", ErrBadSnapshot, entry.Key)
		}
		tokens := entry.Tokens
		if tokens > b.burst {
			// Discard anything above the burst; a long downtime is never
			// converted into an unbounded token balance.
			tokens = b.burst
		}
		b.tokens = float64(tokens)

		borrowed := entry.Borrowed
		if borrowed > b.burst {
			borrowed = b.burst
		}
		b.borrowed = borrowed
		if borrowed > 0 && !snapshot.TakenAt.IsZero() {
			b.due = snapshot.TakenAt.Add(borrowWindow)
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.buckets = next
	g.tickets = make(map[uint64]Ticket)
	g.receipts = make(map[uint64]*refundReceipt)
	g.nextTicket = 0
	g.refundRejects = snapshot.RefundRejects
	g.clock = snapshot.TakenAt
	return nil
}

// Remaining reports the tokens currently held by one bucket. It is a pure
// read: refill is driven by caller supplied observation times.
func (g *FlowRateGovernor) Remaining(level Level, key string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !validLevel(level) {
		return 0
	}
	return remainingTokens(g.buckets[level][key])
}

// Borrowed reports the amount one bucket still owes from its own burst.
func (g *FlowRateGovernor) Borrowed(level Level, key string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !validLevel(level) {
		return 0
	}
	b := g.buckets[level][key]
	if b == nil {
		return 0
	}
	return b.borrowed
}

// RefundRejects reports how many refunds were rejected as cross level or unknown.
func (g *FlowRateGovernor) RefundRejects() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refundRejects
}

// BucketCount reports how many buckets one level currently holds.
func (g *FlowRateGovernor) BucketCount(level Level) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !validLevel(level) {
		return 0
	}
	return len(g.buckets[level])
}
