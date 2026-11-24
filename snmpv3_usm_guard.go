package task

import "errors"

// errNotImplemented marks behaviour that still has to be filled in.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by UsmGuard.
var (
	ErrUserConfig       = errors.New("usm: invalid user configuration")
	ErrUserExists       = errors.New("usm: user already exists")
	ErrUserUnknown      = errors.New("usm: user not found")
	ErrUserLimit        = errors.New("usm: user table full")
	ErrEngineMismatch   = errors.New("usm: engine id mismatch")
	ErrTimeWindow       = errors.New("usm: message outside the timeliness window")
	ErrAuthProof        = errors.New("usm: authentication proof missing")
	ErrPrivProof        = errors.New("usm: privacy proof missing")
	ErrKeyGeneration    = errors.New("usm: localized key generation is stale")
	ErrViewConfig       = errors.New("usm: invalid view definition")
	ErrViewUnknown      = errors.New("usm: view not found")
	ErrTargetConfig     = errors.New("usm: invalid trap target")
	ErrTargetUnknown    = errors.New("usm: trap target not found")
	ErrTargetLimit      = errors.New("usm: trap target table full")
	ErrBootsResetDenied = errors.New("usm: engine boots reset denied")
	ErrUsmSnapshot      = errors.New("usm: corrupt snapshot")
)

// UsmUser is one SNMPv3 USM user.
type UsmUser struct {
	Name      string
	AuthProto string
	PrivProto string
	Level     string
	AuthPass  string
	PrivPass  string
}

// UsmKeys are the localized auth and privacy keys of one user.
type UsmKeys struct {
	User       string
	EngineID   string
	AuthKey    string
	PrivKey    string
	Generation int
}

// UsmMessage is one inbound SNMPv3 message.
type UsmMessage struct {
	User       string
	EngineID   string
	Boots      int
	EngineTime int
	AuthOK     bool
	PrivOK     bool
	Generation int
	RequestID  int
}

// ViewEntry is one OID subtree rule of a view.
type ViewEntry struct {
	OID      string
	Included bool
}

// TrapTarget is one notification receiver bound to a view and user.
type TrapTarget struct {
	Name    string
	Address string
	View    string
	User    string
}

// TrapNotification is the projected payload sent to a target.
type TrapNotification struct {
	Target   string
	Varbinds []string
	Dropped  int
	Sent     bool
}

// UsmOptions carries the guard limits.
type UsmOptions struct {
	EngineID   string
	TimeWindow int
	MaxBoots   int
	MaxUsers   int
	MaxTargets int
}

// UsmStats summarises guard activity.
type UsmStats struct {
	Users    int
	Verified int
	Rejected int
	Traps    int
	Filtered int
}

// UsmGuard manages SNMPv3 USM users, keys and trap views.
type UsmGuard struct {
	opts UsmOptions
}

// NewUsmGuard builds a guard with the supplied limits.
func NewUsmGuard(opts UsmOptions) *UsmGuard {
	return &UsmGuard{opts: opts}
}

// AddUser registers a USM user.
func (g *UsmGuard) AddUser(u UsmUser) error {
	return errNotImplemented
}

// RemoveUser removes a USM user and invalidates its sessions.
func (g *UsmGuard) RemoveUser(name string) error {
	return errNotImplemented
}

// Localize derives the engine scoped auth and privacy keys of a user.
func (g *UsmGuard) Localize(user string) (UsmKeys, error) {
	return UsmKeys{}, errNotImplemented
}

// RotateUserKeys advances the localized key generation of a user.
func (g *UsmGuard) RotateUserKeys(user string) (int, error) {
	return 0, errNotImplemented
}

// VerifyMessage validates one inbound SNMPv3 message.
func (g *UsmGuard) VerifyMessage(msg UsmMessage) error {
	return errNotImplemented
}

// SetView registers an OID view.
func (g *UsmGuard) SetView(name string, entries []ViewEntry) error {
	return errNotImplemented
}

// AddTrapTarget registers a notification receiver.
func (g *UsmGuard) AddTrapTarget(t TrapTarget) error {
	return errNotImplemented
}

// ProjectTrap projects varbinds through the view of a trap target.
func (g *UsmGuard) ProjectTrap(target string, varbinds []string) (TrapNotification, error) {
	return TrapNotification{}, errNotImplemented
}

// ResetEngineBoots performs the explicit administrator reset of the engine boots counter.
func (g *UsmGuard) ResetEngineBoots(operator string) error {
	return errNotImplemented
}

// AdvanceEngineTime advances the local engine time and returns the new value.
func (g *UsmGuard) AdvanceEngineTime(delta int) int {
	return 0
}

// EngineBoots returns the current engine boots counter.
func (g *UsmGuard) EngineBoots() int {
	return 0
}

// Snapshot serialises users, keys, engine state, views and targets.
func (g *UsmGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard from a snapshot.
func (g *UsmGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *UsmGuard) Stats() UsmStats {
	return UsmStats{}
}
