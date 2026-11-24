// Package task keeps an adaptive concurrency window for one upstream together
// with the circuit breaker that protects it. The window grows additively on
// successful samples, shrinks multiplicatively on timeouts and exposes the
// whole state through a lock so concurrent calls observe one consistent view.
package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"
)

// Errors returned by the governor.
var (
	ErrQueueFull   = errors.New("wait queue full")
	ErrCircuitOpen = errors.New("circuit open")
	ErrBadBounds   = errors.New("invalid window bounds")
	ErrBadTicket   = errors.New("invalid ticket")
	ErrBadSnapshot = errors.New("bad snapshot")
)

// State is the circuit breaker state.
type State string

const (
	// StateClosed lets normal traffic through.
	StateClosed State = "closed"
	// StateOpen fails fast until the cooldown elapses.
	StateOpen State = "open"
	// StateHalfOpen lets a single probe through.
	StateHalfOpen State = "half_open"
)

// GovernorOptions configures the window, the breaker and the wait queue.
type GovernorOptions struct {
	InitialWindow      int
	MinWindow          int
	MaxWindow          int
	SampleWindow       int
	GrowthStep         int
	ShrinkFactor       float64
	ErrorRateThreshold float64
	Timeout            time.Duration
	OpenCooldown       time.Duration
	ProbeBackoff       time.Duration
	QueueLimit         int
	Now                func() time.Time
}

// Ticket is one admitted upstream slot.
type Ticket struct {
	ID       uint64
	Probe    bool
	Admitted time.Time
}

// GovernorStats reports the live counters of the governor.
type GovernorStats struct {
	Window    int
	Inflight  int
	Queued    int
	Samples   int
	ErrorRate float64
	Opens     int
	Probes    int
}

type sample struct {
	timeout     bool
	businessErr bool
	latency     time.Duration
}

type acquireResult struct {
	ticket Ticket
	err    error
}

type waiter struct {
	ch      chan acquireResult
	ticket  Ticket
	granted bool
	closed  bool
}

// ConcurrencyGovernor owns the adaptive window, the wait queue and the
// circuit breaker.
type ConcurrencyGovernor struct {
	mu sync.Mutex

	opts   GovernorOptions
	window int
	state  State

	inflight int
	queue    []*waiter

	samples      []sample
	successBatch int
	timeoutBatch int

	tickets map[uint64]Ticket
	nextID  uint64

	openedAt    time.Time
	lastProbeAt time.Time
	preShrink   int
	probes      int
	opens       int
	probeActive bool
}

// NewConcurrencyGovernor builds a governor from the supplied options.
func NewConcurrencyGovernor(opts GovernorOptions) *ConcurrencyGovernor {
	opts = normalizeOptions(opts)

	window := clampWindow(opts.InitialWindow, opts.MinWindow, opts.MaxWindow)
	if window < 1 {
		window = 1
	}

	return &ConcurrencyGovernor{
		opts:    opts,
		window:  window,
		state:   StateClosed,
		tickets: make(map[uint64]Ticket),
	}
}

func normalizeOptions(opts GovernorOptions) GovernorOptions {
	if opts.MinWindow < 1 {
		opts.MinWindow = 1
	}
	if opts.MaxWindow < opts.MinWindow {
		if opts.MaxWindow <= 0 && opts.InitialWindow > opts.MinWindow {
			opts.MaxWindow = opts.InitialWindow
		} else {
			opts.MaxWindow = opts.MinWindow
		}
	}
	if opts.SampleWindow < 1 {
		opts.SampleWindow = 1
	}
	if opts.QueueLimit < 0 {
		opts.QueueLimit = 0
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func clampWindow(window, min, max int) int {
	if window < min {
		return min
	}
	if window > max {
		return max
	}
	return window
}

func (g *ConcurrencyGovernor) nowLocked() time.Time {
	return g.opts.Now()
}

// Acquire reserves one upstream slot, waiting in the bounded queue while the
// window is full and failing fast while the breaker is open.
func (g *ConcurrencyGovernor) Acquire(ctx context.Context) (Ticket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Ticket{}, err
	}

	g.mu.Lock()
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return Ticket{}, err
	}

	now := g.nowLocked()
	g.refreshStateLocked(now)

	switch g.state {
	case StateOpen:
		g.mu.Unlock()
		return Ticket{}, ErrCircuitOpen
	case StateHalfOpen:
		if g.probeActive || !g.probeAllowedLocked(now) || g.inflight >= g.window {
			g.mu.Unlock()
			return Ticket{}, ErrCircuitOpen
		}
		ticket := g.issueTicketLocked(true, now)
		g.probeActive = true
		g.lastProbeAt = now
		g.probes++
		g.mu.Unlock()
		return ticket, nil
	case StateClosed:
		// Drain any waiters that became runnable before admitting directly.
		if len(g.queue) > 0 {
			g.dispatchLocked(now)
		}
		if g.inflight < g.window {
			ticket := g.issueTicketLocked(false, now)
			g.mu.Unlock()
			return ticket, nil
		}
		if len(g.queue) >= g.opts.QueueLimit {
			g.mu.Unlock()
			return Ticket{}, ErrQueueFull
		}

		w := &waiter{ch: make(chan acquireResult, 1)}
		g.queue = append(g.queue, w)
		g.mu.Unlock()
		return g.wait(ctx, w)
	default:
		g.mu.Unlock()
		return Ticket{}, ErrCircuitOpen
	}
}

func (g *ConcurrencyGovernor) wait(ctx context.Context, w *waiter) (Ticket, error) {
	select {
	case result := <-w.ch:
		if result.err != nil {
			if err := ctx.Err(); err != nil {
				return Ticket{}, err
			}
			return Ticket{}, result.err
		}
		if err := ctx.Err(); err != nil {
			g.mu.Lock()
			g.revokeTicketLocked(result.ticket, g.nowLocked())
			g.mu.Unlock()
			return Ticket{}, err
		}
		return result.ticket, nil
	case <-ctx.Done():
		g.mu.Lock()
		if !w.granted {
			g.removeWaiterLocked(w)
		} else {
			g.revokeTicketLocked(w.ticket, g.nowLocked())
		}
		g.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return Ticket{}, err
		}
		return Ticket{}, context.Canceled
	}
}

func (g *ConcurrencyGovernor) issueTicketLocked(probe bool, now time.Time) Ticket {
	g.nextID++
	ticket := Ticket{
		ID:       g.nextID,
		Probe:    probe,
		Admitted: now,
	}
	g.tickets[ticket.ID] = ticket
	g.inflight++
	return ticket
}

func (g *ConcurrencyGovernor) grantLocked(w *waiter, now time.Time) {
	ticket := g.issueTicketLocked(false, now)
	w.ticket = ticket
	w.granted = true
	w.ch <- acquireResult{ticket: ticket}
}

func (g *ConcurrencyGovernor) dispatchLocked(now time.Time) {
	if g.state != StateClosed {
		return
	}
	for len(g.queue) > 0 && g.inflight < g.window {
		w := g.queue[0]
		g.queue = g.queue[1:]
		if w.closed {
			continue
		}
		g.grantLocked(w, now)
	}
	if len(g.queue) == 0 {
		g.queue = nil
	}
}

func (g *ConcurrencyGovernor) removeWaiterLocked(target *waiter) {
	for i, w := range g.queue {
		if w != target {
			continue
		}
		w.closed = true
		copy(g.queue[i:], g.queue[i+1:])
		g.queue[len(g.queue)-1] = nil
		g.queue = g.queue[:len(g.queue)-1]
		return
	}
}

func (g *ConcurrencyGovernor) rejectWaitersLocked(err error) {
	waiters := g.queue
	g.queue = nil
	for _, w := range waiters {
		if w.closed || w.granted {
			continue
		}
		w.closed = true
		w.ch <- acquireResult{err: err}
	}
}

func (g *ConcurrencyGovernor) revokeTicketLocked(ticket Ticket, now time.Time) bool {
	stored, ok := g.tickets[ticket.ID]
	if !ok {
		return false
	}
	delete(g.tickets, ticket.ID)
	if g.inflight > 0 {
		g.inflight--
	}
	if stored.Probe && g.state == StateHalfOpen {
		g.probeActive = false
	}
	if g.state == StateClosed {
		g.dispatchLocked(now)
	}
	return true
}

// Release returns one slot and records the latency sample that may adjust the
// window or the breaker.
func (g *ConcurrencyGovernor) Release(t Ticket, latency time.Duration, ok bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	stored, exists := g.tickets[t.ID]
	if !exists {
		return ErrBadTicket
	}
	delete(g.tickets, t.ID)
	if g.inflight > 0 {
		g.inflight--
	}

	now := g.nowLocked()
	if stored.Probe {
		if g.state == StateHalfOpen && g.probeActive {
			g.probeActive = false
			if g.successfulSampleLocked(latency, ok) {
				g.closeAfterProbeLocked()
			} else {
				g.tripLocked(now, true)
			}
		}
	} else if g.state == StateClosed {
		g.observeLocked(latency, ok, now)
	}

	if g.state == StateClosed {
		g.dispatchLocked(now)
	}
	return nil
}

func (g *ConcurrencyGovernor) successfulSampleLocked(latency time.Duration, ok bool) bool {
	if !ok {
		return false
	}
	if g.opts.Timeout > 0 && latency >= g.opts.Timeout {
		return false
	}
	return true
}

func (g *ConcurrencyGovernor) observeLocked(latency time.Duration, ok bool, now time.Time) {
	timeout := g.opts.Timeout > 0 && latency >= g.opts.Timeout
	businessErr := !ok && !timeout
	success := ok && !timeout

	g.samples = append(g.samples, sample{
		timeout:     timeout,
		businessErr: businessErr,
		latency:     latency,
	})
	if len(g.samples) > g.opts.SampleWindow {
		excess := len(g.samples) - g.opts.SampleWindow
		copy(g.samples, g.samples[excess:])
		g.samples = g.samples[:g.opts.SampleWindow]
	}

	shrank := false
	switch {
	case timeout:
		g.timeoutBatch++
		if g.timeoutBatch >= g.opts.SampleWindow {
			g.timeoutBatch = 0
			shrank = g.shrinkLocked()
		}
	case success:
		g.successBatch++
		if g.successBatch >= g.opts.SampleWindow {
			g.successBatch = 0
			g.growLocked()
		}
	}

	if len(g.samples) >= g.opts.SampleWindow &&
		g.errorRateLocked() > g.opts.ErrorRateThreshold {
		g.tripLocked(now, false)
		return
	}

	if shrank {
		// Requests that were queued under the old window must not be admitted
		// one by one after the window has contracted.
		g.dispatchLocked(now)
		g.rejectWaitersLocked(ErrQueueFull)
		return
	}
	g.dispatchLocked(now)
}

func (g *ConcurrencyGovernor) growLocked() {
	step := g.opts.GrowthStep
	if step < 0 {
		step = 0
	}
	g.window = clampWindow(g.window+step, g.opts.MinWindow, g.opts.MaxWindow)
}

func (g *ConcurrencyGovernor) shrinkLocked() bool {
	old := g.window
	if old > g.preShrink {
		g.preShrink = old
	}
	next := int(math.Floor(float64(g.window) * g.opts.ShrinkFactor))
	g.window = clampWindow(next, g.opts.MinWindow, g.opts.MaxWindow)
	return g.window < old
}

func (g *ConcurrencyGovernor) errorRateLocked() float64 {
	if len(g.samples) == 0 {
		return 0
	}
	failures := 0
	for _, s := range g.samples {
		if s.timeout || s.businessErr {
			failures++
		}
	}
	return float64(failures) / float64(len(g.samples))
}

func (g *ConcurrencyGovernor) tripLocked(now time.Time, afterProbe bool) {
	if g.state != StateOpen {
		g.opens++
	}
	g.state = StateOpen
	g.openedAt = now
	g.probeActive = false
	if afterProbe {
		// Space the next probe from the failed probe rather than letting a
		// stream of callers all become probes immediately after the cooldown.
		g.lastProbeAt = now
	} else {
		g.lastProbeAt = time.Time{}
	}
	g.rejectWaitersLocked(ErrCircuitOpen)
}

func (g *ConcurrencyGovernor) closeAfterProbeLocked() {
	g.state = StateClosed
	g.probeActive = false
	g.openedAt = time.Time{}
	g.lastProbeAt = time.Time{}
	g.samples = nil
	g.successBatch = 0
	g.timeoutBatch = 0

	step := g.opts.GrowthStep
	if step < 0 {
		step = 0
	}
	target := g.window
	if g.preShrink > target {
		target = g.preShrink
	}
	target += step
	g.window = clampWindow(target, g.opts.MinWindow, g.opts.MaxWindow)
	g.preShrink = 0
}

func (g *ConcurrencyGovernor) refreshStateLocked(now time.Time) {
	if g.state != StateOpen {
		return
	}
	if now.Before(g.openedAt.Add(g.opts.OpenCooldown)) {
		return
	}
	g.state = StateHalfOpen
	g.rejectWaitersLocked(ErrCircuitOpen)
}

func (g *ConcurrencyGovernor) probeAllowedLocked(now time.Time) bool {
	if g.opts.ProbeBackoff <= 0 || g.lastProbeAt.IsZero() {
		return true
	}
	return now.Sub(g.lastProbeAt) >= g.opts.ProbeBackoff
}

// State reports the current breaker state.
func (g *ConcurrencyGovernor) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshStateLocked(g.nowLocked())
	return g.state
}

// Window reports the current concurrency window.
func (g *ConcurrencyGovernor) Window() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.window
}

// Inflight reports how many tickets are currently held.
func (g *ConcurrencyGovernor) Inflight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight
}

// Queued reports how many callers are waiting for a slot.
func (g *ConcurrencyGovernor) Queued() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

// SetBounds replaces the hard window bounds.
func (g *ConcurrencyGovernor) SetBounds(min, max int) error {
	if min < 1 || max < min {
		return ErrBadBounds
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	oldWindow := g.window
	g.opts.MinWindow = min
	g.opts.MaxWindow = max
	g.window = clampWindow(g.window, min, max)

	now := g.nowLocked()
	if g.state == StateClosed {
		g.dispatchLocked(now)
		if g.window < oldWindow {
			g.rejectWaitersLocked(ErrQueueFull)
		}
	} else if g.window < oldWindow {
		g.rejectWaitersLocked(ErrCircuitOpen)
	}
	return nil
}

// Stats reports the live counters of the governor.
func (g *ConcurrencyGovernor) Stats() GovernorStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return GovernorStats{
		Window:    g.window,
		Inflight:  g.inflight,
		Queued:    len(g.queue),
		Samples:   len(g.samples),
		ErrorRate: g.errorRateLocked(),
		Opens:     g.opens,
		Probes:    g.probes,
	}
}

const snapshotVersion = 1

type snapshotSample struct {
	Timeout     bool          `json:"timeout"`
	BusinessErr bool          `json:"business_err"`
	Latency     time.Duration `json:"latency"`
}

type governorSnapshot struct {
	Version      int              `json:"version"`
	Window       int              `json:"window"`
	MinWindow    int              `json:"min_window"`
	MaxWindow    int              `json:"max_window"`
	SampleWindow int              `json:"sample_window"`
	State        State            `json:"state"`
	OpenedAt     time.Time        `json:"opened_at"`
	LastProbeAt  time.Time        `json:"last_probe_at"`
	PreShrink    int              `json:"pre_shrink"`
	Samples      []snapshotSample `json:"samples"`
	SuccessBatch int              `json:"success_batch"`
	TimeoutBatch int              `json:"timeout_batch"`
	Opens        int              `json:"opens"`
	Probes       int              `json:"probes"`
	NextID       uint64           `json:"next_id"`
}

type snapshotEnvelope struct {
	Payload  json.RawMessage `json:"payload"`
	Checksum string          `json:"checksum"`
}

// Snapshot serialises the window, the breaker state and the sampling window.
func (g *ConcurrencyGovernor) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	samples := make([]snapshotSample, len(g.samples))
	for i, s := range g.samples {
		samples[i] = snapshotSample{
			Timeout:     s.timeout,
			BusinessErr: s.businessErr,
			Latency:     s.latency,
		}
	}

	snapshot := governorSnapshot{
		Version:      snapshotVersion,
		Window:       g.window,
		MinWindow:    g.opts.MinWindow,
		MaxWindow:    g.opts.MaxWindow,
		SampleWindow: g.opts.SampleWindow,
		State:        g.state,
		OpenedAt:     g.openedAt,
		LastProbeAt:  g.lastProbeAt,
		PreShrink:    g.preShrink,
		Samples:      samples,
		SuccessBatch: g.successBatch,
		TimeoutBatch: g.timeoutBatch,
		Opens:        g.opens,
		Probes:       g.probes,
		NextID:       g.nextID,
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	return json.Marshal(snapshotEnvelope{
		Payload:  payload,
		Checksum: hex.EncodeToString(sum[:]),
	})
}

// Restore replaces the governor state with a snapshot.
func (g *ConcurrencyGovernor) Restore(data []byte) error {
	snapshot, err := decodeSnapshot(data)
	if err != nil {
		return ErrBadSnapshot
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// A restore replaces the whole control state. Any old waiters cannot be
	// carried across because their tickets do not exist in the new state.
	waiters := g.queue
	g.queue = nil
	for _, w := range waiters {
		if w.closed || w.granted {
			continue
		}
		w.closed = true
		w.ch <- acquireResult{err: ErrCircuitOpen}
	}

	g.opts.MinWindow = snapshot.MinWindow
	g.opts.MaxWindow = snapshot.MaxWindow
	g.opts.SampleWindow = snapshot.SampleWindow
	g.window = snapshot.Window
	g.state = snapshot.State
	g.openedAt = snapshot.OpenedAt
	g.lastProbeAt = snapshot.LastProbeAt
	g.preShrink = snapshot.PreShrink
	g.successBatch = snapshot.SuccessBatch
	g.timeoutBatch = snapshot.TimeoutBatch
	g.opens = snapshot.Opens
	g.probes = snapshot.Probes
	g.nextID = snapshot.NextID
	g.probeActive = false
	g.inflight = 0
	g.tickets = make(map[uint64]Ticket)

	g.samples = make([]sample, len(snapshot.Samples))
	for i, s := range snapshot.Samples {
		g.samples[i] = sample{
			timeout:     s.Timeout,
			businessErr: s.BusinessErr,
			latency:     s.Latency,
		}
	}
	return nil
}

func decodeSnapshot(data []byte) (governorSnapshot, error) {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return governorSnapshot{}, err
	}
	if len(envelope.Payload) == 0 {
		return governorSnapshot{}, ErrBadSnapshot
	}
	sum := sha256.Sum256(envelope.Payload)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return governorSnapshot{}, ErrBadSnapshot
	}

	var snapshot governorSnapshot
	if err := json.Unmarshal(envelope.Payload, &snapshot); err != nil {
		return governorSnapshot{}, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return governorSnapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshot(snapshot governorSnapshot) error {
	if snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if snapshot.MinWindow < 1 || snapshot.MaxWindow < snapshot.MinWindow {
		return ErrBadSnapshot
	}
	if snapshot.Window < snapshot.MinWindow || snapshot.Window > snapshot.MaxWindow {
		return ErrBadSnapshot
	}
	if snapshot.SampleWindow < 1 ||
		len(snapshot.Samples) > snapshot.SampleWindow ||
		snapshot.SuccessBatch < 0 ||
		snapshot.SuccessBatch >= snapshot.SampleWindow ||
		snapshot.TimeoutBatch < 0 ||
		snapshot.TimeoutBatch >= snapshot.SampleWindow ||
		snapshot.PreShrink < 0 ||
		snapshot.Opens < 0 ||
		snapshot.Probes < 0 {
		return ErrBadSnapshot
	}
	switch snapshot.State {
	case StateClosed:
	case StateOpen, StateHalfOpen:
		if snapshot.OpenedAt.IsZero() {
			return ErrBadSnapshot
		}
	default:
		return ErrBadSnapshot
	}
	for _, s := range snapshot.Samples {
		if s.Timeout && s.BusinessErr {
			return ErrBadSnapshot
		}
	}
	return nil
}
