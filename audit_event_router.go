package task

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors reported by the audit router.
var (
	ErrInvalidEvent        = errors.New("audit_event_router: invalid event")
	ErrInvalidSeverity     = errors.New("audit_event_router: invalid severity")
	ErrUnknownChannel      = errors.New("audit_event_router: unknown channel")
	ErrChannelExists       = errors.New("audit_event_router: channel already open")
	ErrInvalidCapacity     = errors.New("audit_event_router: invalid capacity")
	ErrChannelDraining     = errors.New("audit_event_router: channel is draining")
	ErrBadSnapshot         = errors.New("audit_event_router: bad snapshot")
	ErrInvalidChannel      = errors.New("audit_event_router: invalid channel")
	ErrTooManyRedactFields = errors.New("audit_event_router: too many redact fields")
)

const (
	redactedValue = "[redacted]"

	// maxChannelCapacity and maxRedactFields are hard limits: callers get an
	// identifiable error instead of the router silently growing without bound.
	maxChannelCapacity = 1 << 20
	maxRedactFields    = 256
)

var defaultRedactFields = []string{
	"password",
	"token",
	"secret",
	"authorization",
	"cookie",
}

// Severity is the ordered severity of an audit event.
type Severity int

// Severity levels from least to most urgent.
const (
	SeverityDebug Severity = iota
	SeverityInfo
	SeverityWarn
	SeverityError
	SeverityCritical
)

// RawEvent is the event as it arrives from an emitter.
type RawEvent struct {
	EventID  string
	Tenant   string
	Channel  string
	Severity Severity
	Fields   map[string]string
}

// ProjectedEvent is the redacted copy handed to a delivery channel.
type ProjectedEvent struct {
	EventID  string
	Tenant   string
	Channel  string
	Severity Severity
	Sequence uint64
	Fields   map[string]string
}

// SubmitResult reports what happened to one submission.
type SubmitResult struct {
	Accepted  bool
	Duplicate bool
	Dropped   bool
	Channel   string
	Sequence  uint64
}

// GapRange is a contiguous run of sequence numbers that never reached a channel.
type GapRange struct {
	From uint64
	To   uint64
}

// ChannelSnapshot is the queue metadata of one channel.
type ChannelSnapshot struct {
	Name     string
	Capacity int
	Sequence uint64
	Pending  int
	Draining bool
}

// RouterSnapshot is the portable form of a whole router.
type RouterSnapshot struct {
	Channels []ChannelSnapshot
	Counters RouterStats
}

// RouterStats aggregates router counters.
type RouterStats struct {
	Submitted  int
	Accepted   int
	Duplicates int
	Dropped    int
	Projected  int
	Drained    int
	Redacted   int
}

type seenEvent struct {
	channel  string
	sequence uint64
}

type auditChannel struct {
	name          string
	capacity      int
	queue         []ProjectedEvent
	sequence      uint64
	draining      bool
	severityFloor Severity
	gaps          []GapRange
}

func (c *auditChannel) recordGap(sequence uint64) {
	if n := len(c.gaps); n > 0 {
		last := &c.gaps[n-1]
		if last.To != ^uint64(0) && last.To+1 == sequence {
			last.To = sequence
			return
		}
	}
	c.gaps = append(c.gaps, GapRange{From: sequence, To: sequence})
}

// AuditRouter projects, redacts and routes audit events into bounded channels.
type AuditRouter struct {
	mu         sync.Mutex
	channels   map[string]*auditChannel
	seen       map[string]seenEvent
	redactKeys map[string]struct{}
	configErr  error
	counters   RouterStats
}

// NewAuditRouter builds a router with an extra list of sensitive field names.
func NewAuditRouter(redactFields []string) *AuditRouter {
	r := &AuditRouter{
		channels:   make(map[string]*auditChannel),
		seen:       make(map[string]seenEvent),
		redactKeys: make(map[string]struct{}, len(defaultRedactFields)+len(redactFields)),
	}
	for _, field := range defaultRedactFields {
		r.redactKeys[field] = struct{}{}
	}
	for _, field := range redactFields {
		key := normalizeFieldName(field)
		if key == "" {
			continue
		}
		if _, exists := r.redactKeys[key]; exists {
			continue
		}
		if len(r.redactKeys) >= maxRedactFields {
			r.configErr = ErrTooManyRedactFields
			break
		}
		r.redactKeys[key] = struct{}{}
	}
	return r
}

func (r *AuditRouter) ensureInitLocked() {
	if r.channels == nil {
		r.channels = make(map[string]*auditChannel)
	}
	if r.seen == nil {
		r.seen = make(map[string]seenEvent)
	}
	if r.redactKeys == nil {
		r.redactKeys = make(map[string]struct{}, len(defaultRedactFields))
		for _, field := range defaultRedactFields {
			r.redactKeys[field] = struct{}{}
		}
	}
}

func normalizeFieldName(field string) string {
	return strings.ToLower(strings.TrimSpace(field))
}

func validSeverity(severity Severity) bool {
	return severity >= SeverityDebug && severity <= SeverityCritical
}

func dedupeKey(tenant, eventID string) string {
	return tenant + "\x00" + eventID
}

// OpenChannel creates a bounded delivery channel.
func (r *AuditRouter) OpenChannel(name string, capacity int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	if name == "" {
		return ErrInvalidChannel
	}
	if capacity <= 0 || capacity > maxChannelCapacity {
		return ErrInvalidCapacity
	}
	if _, exists := r.channels[name]; exists {
		return ErrChannelExists
	}
	r.channels[name] = &auditChannel{
		name:          name,
		capacity:      capacity,
		severityFloor: SeverityDebug,
	}
	return nil
}

// CloseChannel drops one channel.
func (r *AuditRouter) CloseChannel(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	if _, exists := r.channels[name]; !exists {
		return ErrUnknownChannel
	}
	delete(r.channels, name)
	for key, seen := range r.seen {
		if seen.channel == name {
			delete(r.seen, key)
		}
	}
	return nil
}

// Submit projects and routes one raw event.
func (r *AuditRouter) Submit(evt RawEvent) (SubmitResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	r.counters.Submitted++
	if r.configErr != nil {
		return SubmitResult{}, r.configErr
	}
	if evt.EventID == "" || evt.Tenant == "" {
		return SubmitResult{}, ErrInvalidEvent
	}
	if !validSeverity(evt.Severity) {
		return SubmitResult{}, ErrInvalidSeverity
	}

	channel, exists := r.channels[evt.Channel]
	if !exists {
		return SubmitResult{}, ErrUnknownChannel
	}
	if channel.draining {
		return SubmitResult{Channel: evt.Channel}, ErrChannelDraining
	}

	key := dedupeKey(evt.Tenant, evt.EventID)
	if previous, duplicate := r.seen[key]; duplicate {
		r.counters.Duplicates++
		return SubmitResult{
			Duplicate: true,
			Channel:   previous.channel,
			Sequence:  previous.sequence,
		}, nil
	}

	channel.sequence++
	sequence := channel.sequence
	projected, redacted := r.projectLocked(evt, channel)
	projected.Sequence = sequence
	r.counters.Projected++
	r.counters.Redacted += redacted

	if len(channel.queue) >= channel.capacity {
		r.counters.Dropped++
		channel.recordGap(sequence)
		return SubmitResult{
			Dropped:  true,
			Channel:  evt.Channel,
			Sequence: sequence,
		}, nil
	}

	channel.queue = append(channel.queue, projected)
	r.seen[key] = seenEvent{channel: evt.Channel, sequence: sequence}
	r.counters.Accepted++
	return SubmitResult{
		Accepted: true,
		Channel:  evt.Channel,
		Sequence: sequence,
	}, nil
}

// Project builds the redacted copy of one event.
func (r *AuditRouter) Project(evt RawEvent) (ProjectedEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	if r.configErr != nil {
		return ProjectedEvent{}, r.configErr
	}
	if !validSeverity(evt.Severity) {
		return ProjectedEvent{}, ErrInvalidSeverity
	}

	channel := r.channels[evt.Channel]
	projected, redacted := r.projectLocked(evt, channel)
	r.counters.Projected++
	r.counters.Redacted += redacted
	return projected, nil
}

func (r *AuditRouter) projectLocked(evt RawEvent, channel *auditChannel) (ProjectedEvent, int) {
	var fields map[string]string
	redacted := 0
	if evt.Fields != nil {
		fields = make(map[string]string, len(evt.Fields))
		for key, value := range evt.Fields {
			if _, sensitive := r.redactKeys[normalizeFieldName(key)]; sensitive {
				fields[key] = redactedValue
				redacted++
			} else {
				fields[key] = value
			}
		}
	}

	severity := evt.Severity
	if channel != nil && severity < channel.severityFloor {
		severity = channel.severityFloor
	}
	return ProjectedEvent{
		EventID:  evt.EventID,
		Tenant:   evt.Tenant,
		Channel:  evt.Channel,
		Severity: severity,
		Fields:   fields,
	}, redacted
}

// BeginDrain stops a channel from accepting new events.
func (r *AuditRouter) BeginDrain(channel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	ch, exists := r.channels[channel]
	if !exists {
		return ErrUnknownChannel
	}
	ch.draining = true
	return nil
}

// DrainChannel returns every queued event of one channel.
func (r *AuditRouter) DrainChannel(channel string) ([]ProjectedEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	ch, exists := r.channels[channel]
	if !exists {
		return nil, ErrUnknownChannel
	}
	if len(ch.queue) == 0 {
		ch.queue = nil
		return nil, nil
	}

	drained := make([]ProjectedEvent, len(ch.queue))
	copy(drained, ch.queue)
	for i := range drained {
		drained[i].Fields = cloneFields(drained[i].Fields)
	}
	r.counters.Drained += len(drained)
	ch.queue = nil
	return drained, nil
}

func cloneFields(fields map[string]string) map[string]string {
	if fields == nil {
		return nil
	}
	clone := make(map[string]string, len(fields))
	for key, value := range fields {
		clone[key] = value
	}
	return clone
}

// Pending reports how many events wait in one channel.
func (r *AuditRouter) Pending(channel string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	ch, exists := r.channels[channel]
	if !exists {
		return 0, ErrUnknownChannel
	}
	return len(ch.queue), nil
}

// Gaps reports the sequence ranges dropped from one channel.
func (r *AuditRouter) Gaps(channel string) ([]GapRange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	ch, exists := r.channels[channel]
	if !exists {
		return nil, ErrUnknownChannel
	}
	if len(ch.gaps) == 0 {
		return nil, nil
	}
	gaps := make([]GapRange, len(ch.gaps))
	copy(gaps, ch.gaps)
	return gaps, nil
}

// SetSeverityFloor raises every event below the floor to it.
func (r *AuditRouter) SetSeverityFloor(channel string, sev Severity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	ch, exists := r.channels[channel]
	if !exists {
		return ErrUnknownChannel
	}
	if !validSeverity(sev) {
		return ErrInvalidSeverity
	}
	ch.severityFloor = sev
	return nil
}

// Channels lists the open channel names.
func (r *AuditRouter) Channels() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	names := make([]string, 0, len(r.channels))
	for name := range r.channels {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Snapshot captures queue metadata and counters, never event bodies.
func (r *AuditRouter) Snapshot() (RouterSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	names := make([]string, 0, len(r.channels))
	for name := range r.channels {
		names = append(names, name)
	}
	sort.Strings(names)

	snapshot := RouterSnapshot{
		Channels: make([]ChannelSnapshot, 0, len(names)),
		Counters: r.counters,
	}
	for _, name := range names {
		ch := r.channels[name]
		snapshot.Channels = append(snapshot.Channels, ChannelSnapshot{
			Name:     ch.name,
			Capacity: ch.capacity,
			Sequence: ch.sequence,
			Pending:  len(ch.queue),
			Draining: ch.draining,
		})
	}
	return snapshot, nil
}

// Restore replaces router metadata and counters with a snapshot.
func (r *AuditRouter) Restore(snap RouterSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureInitLocked()

	if err := validateSnapshot(snap); err != nil {
		return err
	}

	channels := make(map[string]*auditChannel, len(snap.Channels))
	for _, channel := range snap.Channels {
		channels[channel.Name] = &auditChannel{
			name:          channel.Name,
			capacity:      channel.Capacity,
			sequence:      channel.Sequence,
			draining:      channel.Draining,
			severityFloor: SeverityDebug,
		}
	}
	r.channels = channels
	r.seen = make(map[string]seenEvent)
	r.counters = snap.Counters
	return nil
}

func validateSnapshot(snapshot RouterSnapshot) error {
	if len(snapshot.Channels) == 0 {
		return ErrBadSnapshot
	}
	if !validStats(snapshot.Counters) {
		return ErrBadSnapshot
	}

	seen := make(map[string]struct{}, len(snapshot.Channels))
	for _, channel := range snapshot.Channels {
		if channel.Name == "" {
			return ErrBadSnapshot
		}
		if channel.Capacity <= 0 || channel.Capacity > maxChannelCapacity {
			return ErrBadSnapshot
		}
		if channel.Pending < 0 || channel.Pending > channel.Capacity {
			return ErrBadSnapshot
		}
		if _, duplicate := seen[channel.Name]; duplicate {
			return ErrBadSnapshot
		}
		seen[channel.Name] = struct{}{}
	}
	return nil
}

func validStats(stats RouterStats) bool {
	return stats.Submitted >= 0 &&
		stats.Accepted >= 0 &&
		stats.Duplicates >= 0 &&
		stats.Dropped >= 0 &&
		stats.Projected >= 0 &&
		stats.Drained >= 0 &&
		stats.Redacted >= 0
}

// Stats aggregates router counters.
func (r *AuditRouter) Stats() RouterStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters
}
