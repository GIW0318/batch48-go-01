package task

import "errors"

// errNotImplemented is returned by behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the audit router.
var (
	ErrInvalidEvent    = errors.New("audit_event_router: invalid event")
	ErrInvalidSeverity = errors.New("audit_event_router: invalid severity")
	ErrUnknownChannel  = errors.New("audit_event_router: unknown channel")
	ErrChannelExists   = errors.New("audit_event_router: channel already open")
	ErrInvalidCapacity = errors.New("audit_event_router: invalid capacity")
	ErrChannelDraining = errors.New("audit_event_router: channel is draining")
	ErrBadSnapshot     = errors.New("audit_event_router: bad snapshot")
)

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

// AuditRouter projects, redacts and routes audit events into bounded channels.
type AuditRouter struct {
	configSize int
}

// NewAuditRouter builds a router with an extra list of sensitive field names.
func NewAuditRouter(redactFields []string) *AuditRouter {
	return &AuditRouter{}
}

// OpenChannel creates a bounded delivery channel.
func (r *AuditRouter) OpenChannel(name string, capacity int) error {
	return errNotImplemented
}

// CloseChannel drops one channel.
func (r *AuditRouter) CloseChannel(name string) error {
	return errNotImplemented
}

// Submit projects and routes one raw event.
func (r *AuditRouter) Submit(evt RawEvent) (SubmitResult, error) {
	return SubmitResult{}, errNotImplemented
}

// Project builds the redacted copy of one event.
func (r *AuditRouter) Project(evt RawEvent) (ProjectedEvent, error) {
	return ProjectedEvent{}, errNotImplemented
}

// BeginDrain stops a channel from accepting new events.
func (r *AuditRouter) BeginDrain(channel string) error {
	return errNotImplemented
}

// DrainChannel returns every queued event of one channel.
func (r *AuditRouter) DrainChannel(channel string) ([]ProjectedEvent, error) {
	return nil, errNotImplemented
}

// Pending reports how many events wait in one channel.
func (r *AuditRouter) Pending(channel string) (int, error) {
	return 0, errNotImplemented
}

// Gaps reports the sequence ranges dropped from one channel.
func (r *AuditRouter) Gaps(channel string) ([]GapRange, error) {
	return nil, errNotImplemented
}

// SetSeverityFloor raises every event below the floor to it.
func (r *AuditRouter) SetSeverityFloor(channel string, sev Severity) error {
	return errNotImplemented
}

// Channels lists the open channel names.
func (r *AuditRouter) Channels() []string {
	return nil
}

// Snapshot captures queue metadata and counters, never event bodies.
func (r *AuditRouter) Snapshot() (RouterSnapshot, error) {
	return RouterSnapshot{}, errNotImplemented
}

// Restore replaces router metadata and counters with a snapshot.
func (r *AuditRouter) Restore(snap RouterSnapshot) error {
	return errNotImplemented
}

// Stats aggregates router counters.
func (r *AuditRouter) Stats() RouterStats {
	return RouterStats{}
}
