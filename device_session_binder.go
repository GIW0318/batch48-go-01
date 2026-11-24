// Package task binds account login sessions to a terminal device image and
// grades fingerprint drift reported in the middle of a session.
package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Device binding states.
const (
	DeviceActive    = "active"
	DeviceSuspended = "suspended"
	DeviceRevoked   = "revoked"
)

// Verdict actions returned by ReportFingerprint.
const (
	ActionAllow   = "allow"
	ActionObserve = "observe"
	ActionKick    = "kick"
)

// Reason codes attached to a verdict and to a recorded drift event.
const (
	ReasonStable          = "reason_stable"
	ReasonMinorDrift      = "reason_minor_drift"
	ReasonSuspiciousDrift = "reason_suspicious_drift"
	ReasonHardDrift       = "reason_hard_drift"
	ReasonDeviceRevoked   = "reason_device_revoked"
)

// Fingerprint scoring anchors.
const (
	FingerprintMaxScore = 100
	ScoreAllowMin       = 85
	ScoreObserveMin     = 40
)

// Errors returned by the binder.
var (
	ErrBlankIdentity   = errors.New("user id and device id are required")
	ErrBadReason       = errors.New("device action reason is required")
	ErrDeviceNotFound  = errors.New("device binding not found")
	ErrDeviceLimit     = errors.New("device limit reached for this user")
	ErrSessionLimit    = errors.New("session limit reached for this device")
	ErrUnknownSession  = errors.New("unknown session")
	ErrDeviceSuspended = errors.New("device is suspended")
	ErrDeviceRevoked   = errors.New("device is revoked")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Fingerprint is the terminal image captured for a login attempt. Any field
// may come back empty when the client runs in a privacy restricted mode.
type Fingerprint struct {
	UserAgent  string `json:"user_agent"`
	AcceptLang string `json:"accept_lang"`
	Screen     string `json:"screen"`
	TimeZone   string `json:"time_zone"`
	Canvas     string `json:"canvas"`
	TLSHash    string `json:"tls_hash"`
	PluginHash string `json:"plugin_hash"`
}

// Session is a live login session bound to one device.
type Session struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	Score    int    `json:"score"`
	Seq      int64  `json:"seq"`
}

// DeviceBinding is the per user per device record that survives a restart.
type DeviceBinding struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	State    string `json:"state"`
	Sessions int    `json:"sessions"`
	Score    int    `json:"score"`
	Reason   string `json:"reason"`
	Failures int    `json:"failures"`
	Seq      int64  `json:"seq"`
}

// DriftEvent is one graded report appended to the per user audit log.
type DriftEvent struct {
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	Action    string `json:"action"`
	Score     int    `json:"score"`
	Reason    string `json:"reason"`
	Seq       int64  `json:"seq"`
}

// Verdict is the explainable outcome of one report.
type Verdict struct {
	SessionID string   `json:"session_id"`
	DeviceID  string   `json:"device_id"`
	Action    string   `json:"action"`
	Score     int      `json:"score"`
	Reason    string   `json:"reason"`
	Drift     []string `json:"drift"`
	Seq       int64    `json:"seq"`
}

// Limits bounds the live tables of the binder.
type Limits struct {
	MaxDevicesPerUser    int
	MaxSessionsPerDevice int
	MaxEvents            int
}

// DeviceSessionBinder owns the binding table, the event log and the counters.
type DeviceSessionBinder struct {
	limits Limits
}

// NewDeviceSessionBinder builds a binder with the given caps.
func NewDeviceSessionBinder(limits Limits) *DeviceSessionBinder {
	return &DeviceSessionBinder{limits: limits}
}

// BeginSession binds a new session to a device image.
func (b *DeviceSessionBinder) BeginSession(userID, deviceID string, fp Fingerprint) (Session, error) {
	return Session{}, errNotImplemented
}

// EndSession releases one live session and its device slot.
func (b *DeviceSessionBinder) EndSession(sessionID string) error {
	return errNotImplemented
}

// ReportFingerprint grades a mid session report against the bound image.
func (b *DeviceSessionBinder) ReportFingerprint(sessionID string, fp Fingerprint) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// SuspendDevice keeps the binding but closes its sessions.
func (b *DeviceSessionBinder) SuspendDevice(userID, deviceID, reason string) error {
	return errNotImplemented
}

// RevokeDevice fences the device off for good and keeps its watermark.
func (b *DeviceSessionBinder) RevokeDevice(userID, deviceID string) error {
	return errNotImplemented
}

// ActiveDevices lists the usable devices of one user, ordered by device id.
func (b *DeviceSessionBinder) ActiveDevices(userID string) []DeviceBinding {
	return nil
}

// Binding reads one binding record.
func (b *DeviceSessionBinder) Binding(userID, deviceID string) (DeviceBinding, bool) {
	return DeviceBinding{}, false
}

// Events returns a copy of the per user drift log, oldest first.
func (b *DeviceSessionBinder) Events(userID string) []DriftEvent {
	return nil
}

// Snapshot serialises bindings, the monotonic counter and the drift log.
func (b *DeviceSessionBinder) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole state with a snapshot, all or nothing.
func (b *DeviceSessionBinder) Restore(data []byte) error {
	return errNotImplemented
}
