// Package task binds account login sessions to a terminal device image and
// grades fingerprint drift reported in the middle of a session.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

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

const (
	snapshotMagic   = "gsb-device-session-binder"
	snapshotVersion = 1
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

type bindingKey struct {
	userID   string
	deviceID string
}

type liveSession struct {
	Session
	Fingerprint Fingerprint `json:"fingerprint"`
}

type snapshotState struct {
	Magic    string                  `json:"magic"`
	Version  int                     `json:"version"`
	NextSeq  int64                   `json:"next_seq"`
	Bindings []DeviceBinding         `json:"bindings"`
	Sessions []liveSession           `json:"sessions"`
	Events   map[string][]DriftEvent `json:"events"`
}

// DeviceSessionBinder owns the binding table, the event log and the counters.
type DeviceSessionBinder struct {
	mu       sync.Mutex
	limits   Limits
	nextSeq  int64
	bindings map[bindingKey]*DeviceBinding
	sessions map[string]*liveSession
	events   map[string][]DriftEvent
}

type fingerprintComponent struct {
	name   string
	weight int
	get    func(Fingerprint) string
}

var fingerprintComponents = []fingerprintComponent{
	{name: "accept_lang", weight: 5, get: func(fp Fingerprint) string { return fp.AcceptLang }},
	{name: "canvas", weight: 20, get: func(fp Fingerprint) string { return fp.Canvas }},
	{name: "plugin_hash", weight: 5, get: func(fp Fingerprint) string { return fp.PluginHash }},
	{name: "screen", weight: 10, get: func(fp Fingerprint) string { return fp.Screen }},
	{name: "tls_hash", weight: 30, get: func(fp Fingerprint) string { return fp.TLSHash }},
	{name: "time_zone", weight: 10, get: func(fp Fingerprint) string { return fp.TimeZone }},
	{name: "user_agent", weight: 20, get: func(fp Fingerprint) string { return fp.UserAgent }},
}

// NewDeviceSessionBinder builds a binder with the given caps.
func NewDeviceSessionBinder(limits Limits) *DeviceSessionBinder {
	return &DeviceSessionBinder{
		limits:   limits,
		bindings: make(map[bindingKey]*DeviceBinding),
		sessions: make(map[string]*liveSession),
		events:   make(map[string][]DriftEvent),
	}
}

func validIdentity(userID, deviceID string) bool {
	return strings.TrimSpace(userID) != "" && strings.TrimSpace(deviceID) != ""
}

func (b *DeviceSessionBinder) nextSeqLocked() int64 {
	b.nextSeq++
	return b.nextSeq
}

func (b *DeviceSessionBinder) activeDeviceCountLocked(userID string) int {
	count := 0
	for key, binding := range b.bindings {
		if key.userID == userID && binding.State == DeviceActive {
			count++
		}
	}
	return count
}

func (b *DeviceSessionBinder) closeDeviceSessionsLocked(userID, deviceID string) {
	for id, session := range b.sessions {
		if session.UserID == userID && session.DeviceID == deviceID {
			delete(b.sessions, id)
		}
	}
	if binding := b.bindings[bindingKey{userID: userID, deviceID: deviceID}]; binding != nil {
		binding.Sessions = 0
	}
}

func (b *DeviceSessionBinder) appendEventLocked(userID string, event DriftEvent) {
	if b.limits.MaxEvents <= 0 {
		delete(b.events, userID)
		return
	}
	events := append(b.events[userID], event)
	if len(events) > b.limits.MaxEvents {
		events = append([]DriftEvent(nil), events[len(events)-b.limits.MaxEvents:]...)
	}
	b.events[userID] = events
}

func gradeFingerprint(bound, reported Fingerprint) (int, []string) {
	score := FingerprintMaxScore
	drift := make([]string, 0, len(fingerprintComponents))
	for _, component := range fingerprintComponents {
		boundValue := component.get(bound)
		reportedValue := component.get(reported)
		if boundValue == reportedValue {
			continue
		}
		if boundValue == "" || reportedValue == "" {
			score -= component.weight / 2
			continue
		}
		score -= component.weight
		drift = append(drift, component.name)
	}
	if score < 0 {
		score = 0
	}
	sort.Strings(drift)
	return score, drift
}

func classify(score int, drift []string) (string, string) {
	switch {
	case score >= ScoreAllowMin:
		if score == FingerprintMaxScore && len(drift) == 0 {
			return ActionAllow, ReasonStable
		}
		return ActionAllow, ReasonMinorDrift
	case score >= ScoreObserveMin:
		return ActionObserve, ReasonSuspiciousDrift
	default:
		return ActionKick, ReasonHardDrift
	}
}

// BeginSession binds a new session to a device image.
func (b *DeviceSessionBinder) BeginSession(userID, deviceID string, fp Fingerprint) (Session, error) {
	if !validIdentity(userID, deviceID) {
		return Session{}, ErrBlankIdentity
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	key := bindingKey{userID: userID, deviceID: deviceID}
	binding, exists := b.bindings[key]
	if exists {
		switch binding.State {
		case DeviceRevoked:
			return Session{}, ErrDeviceRevoked
		case DeviceSuspended:
			return Session{}, ErrDeviceSuspended
		}
		if binding.Sessions >= b.limits.MaxSessionsPerDevice {
			return Session{}, ErrSessionLimit
		}
	} else {
		if b.activeDeviceCountLocked(userID) >= b.limits.MaxDevicesPerUser {
			return Session{}, ErrDeviceLimit
		}
		if b.limits.MaxSessionsPerDevice <= 0 {
			return Session{}, ErrSessionLimit
		}
		binding = &DeviceBinding{
			UserID:   userID,
			DeviceID: deviceID,
			State:    DeviceActive,
			Score:    FingerprintMaxScore,
		}
		b.bindings[key] = binding
	}

	seq := b.nextSeqLocked()
	session := Session{
		ID:       fmt.Sprintf("sess-%d", seq),
		UserID:   userID,
		DeviceID: deviceID,
		Score:    FingerprintMaxScore,
		Seq:      seq,
	}
	b.sessions[session.ID] = &liveSession{Session: session, Fingerprint: fp}
	binding.Sessions++
	binding.Seq = seq
	return session, nil
}

// EndSession releases one live session and its device slot.
func (b *DeviceSessionBinder) EndSession(sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	session, ok := b.sessions[sessionID]
	if !ok {
		return ErrUnknownSession
	}
	delete(b.sessions, sessionID)
	if binding := b.bindings[bindingKey{userID: session.UserID, deviceID: session.DeviceID}]; binding != nil {
		if binding.Sessions > 0 {
			binding.Sessions--
		}
	}
	return nil
}

// ReportFingerprint grades a mid session report against the bound image.
func (b *DeviceSessionBinder) ReportFingerprint(sessionID string, fp Fingerprint) (Verdict, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	session, ok := b.sessions[sessionID]
	if !ok {
		return Verdict{}, ErrUnknownSession
	}
	key := bindingKey{userID: session.UserID, deviceID: session.DeviceID}
	binding := b.bindings[key]
	if binding == nil {
		return Verdict{}, ErrUnknownSession
	}
	if binding.State == DeviceRevoked {
		return Verdict{}, ErrDeviceRevoked
	}
	if binding.State == DeviceSuspended {
		return Verdict{}, ErrDeviceSuspended
	}

	score, drift := gradeFingerprint(session.Fingerprint, fp)
	action, reason := classify(score, drift)
	seq := b.nextSeqLocked()

	verdict := Verdict{
		SessionID: sessionID,
		DeviceID:  session.DeviceID,
		Action:    action,
		Score:     score,
		Reason:    reason,
		Drift:     append([]string(nil), drift...),
		Seq:       seq,
	}
	binding.Score = score
	binding.Seq = seq
	b.appendEventLocked(session.UserID, DriftEvent{
		SessionID: sessionID,
		DeviceID:  session.DeviceID,
		Action:    action,
		Score:     score,
		Reason:    reason,
		Seq:       seq,
	})

	if action == ActionKick {
		delete(b.sessions, sessionID)
		if binding.Sessions > 0 {
			binding.Sessions--
		}
		binding.Failures++
	}
	return verdict, nil
}

// SuspendDevice keeps the binding but closes its sessions.
func (b *DeviceSessionBinder) SuspendDevice(userID, deviceID, reason string) error {
	if !validIdentity(userID, deviceID) {
		return ErrBlankIdentity
	}
	if strings.TrimSpace(reason) == "" {
		return ErrBadReason
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	binding := b.bindings[bindingKey{userID: userID, deviceID: deviceID}]
	if binding == nil {
		return ErrDeviceNotFound
	}
	if binding.State == DeviceRevoked {
		return ErrDeviceRevoked
	}
	binding.State = DeviceSuspended
	binding.Reason = reason
	binding.Seq = b.nextSeqLocked()
	b.closeDeviceSessionsLocked(userID, deviceID)
	return nil
}

// RevokeDevice fences the device off for good and keeps its watermark.
func (b *DeviceSessionBinder) RevokeDevice(userID, deviceID string) error {
	if !validIdentity(userID, deviceID) {
		return ErrBlankIdentity
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	binding := b.bindings[bindingKey{userID: userID, deviceID: deviceID}]
	if binding == nil {
		return ErrDeviceNotFound
	}
	if binding.State == DeviceRevoked {
		return nil
	}
	binding.State = DeviceRevoked
	binding.Reason = ReasonDeviceRevoked
	binding.Seq = b.nextSeqLocked()
	b.closeDeviceSessionsLocked(userID, deviceID)
	return nil
}

// ActiveDevices lists the usable devices of one user, ordered by device id.
func (b *DeviceSessionBinder) ActiveDevices(userID string) []DeviceBinding {
	b.mu.Lock()
	defer b.mu.Unlock()

	devices := make([]DeviceBinding, 0)
	for key, binding := range b.bindings {
		if key.userID == userID && binding.State == DeviceActive {
			devices = append(devices, *binding)
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceID < devices[j].DeviceID })
	return devices
}

// Binding reads one binding record.
func (b *DeviceSessionBinder) Binding(userID, deviceID string) (DeviceBinding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	binding, ok := b.bindings[bindingKey{userID: userID, deviceID: deviceID}]
	if !ok {
		return DeviceBinding{}, false
	}
	return *binding, true
}

// Events returns a copy of the per user drift log, oldest first.
func (b *DeviceSessionBinder) Events(userID string) []DriftEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	events := b.events[userID]
	if len(events) == 0 {
		return nil
	}
	return append([]DriftEvent(nil), events...)
}

// Snapshot serialises bindings, the monotonic counter and the drift log.
func (b *DeviceSessionBinder) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state := snapshotState{
		Magic:    snapshotMagic,
		Version:  snapshotVersion,
		NextSeq:  b.nextSeq,
		Bindings: make([]DeviceBinding, 0, len(b.bindings)),
		Sessions: make([]liveSession, 0, len(b.sessions)),
		Events:   make(map[string][]DriftEvent, len(b.events)),
	}
	for _, binding := range b.bindings {
		state.Bindings = append(state.Bindings, *binding)
	}
	sort.Slice(state.Bindings, func(i, j int) bool {
		if state.Bindings[i].UserID != state.Bindings[j].UserID {
			return state.Bindings[i].UserID < state.Bindings[j].UserID
		}
		return state.Bindings[i].DeviceID < state.Bindings[j].DeviceID
	})
	for _, session := range b.sessions {
		state.Sessions = append(state.Sessions, *session)
	}
	sort.Slice(state.Sessions, func(i, j int) bool {
		return state.Sessions[i].ID < state.Sessions[j].ID
	})
	for userID, events := range b.events {
		state.Events[userID] = append([]DriftEvent(nil), events...)
	}

	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func validState(state string) bool {
	return state == DeviceActive || state == DeviceSuspended || state == DeviceRevoked
}

func validAction(action string) bool {
	return action == ActionAllow || action == ActionObserve || action == ActionKick
}

// Restore replaces the whole state with a snapshot, all or nothing.
func (b *DeviceSessionBinder) Restore(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Magic != snapshotMagic || state.Version != snapshotVersion || state.NextSeq < 0 {
		return ErrBadSnapshot
	}

	bindings := make(map[bindingKey]*DeviceBinding, len(state.Bindings))
	var maxSeq int64
	for _, record := range state.Bindings {
		if !validIdentity(record.UserID, record.DeviceID) || !validState(record.State) {
			return ErrBadSnapshot
		}
		if record.Sessions < 0 || record.Failures < 0 || record.Score < 0 || record.Score > FingerprintMaxScore {
			return ErrBadSnapshot
		}
		key := bindingKey{userID: record.UserID, deviceID: record.DeviceID}
		if _, exists := bindings[key]; exists {
			return ErrBadSnapshot
		}
		copy := record
		bindings[key] = &copy
		if record.Seq > maxSeq {
			maxSeq = record.Seq
		}
	}

	sessions := make(map[string]*liveSession, len(state.Sessions))
	liveCounts := make(map[bindingKey]int)
	for _, record := range state.Sessions {
		if strings.TrimSpace(record.ID) == "" || !validIdentity(record.UserID, record.DeviceID) {
			return ErrBadSnapshot
		}
		if record.Score < 0 || record.Score > FingerprintMaxScore || record.Seq < 0 {
			return ErrBadSnapshot
		}
		key := bindingKey{userID: record.UserID, deviceID: record.DeviceID}
		binding, ok := bindings[key]
		if !ok || binding.State != DeviceActive {
			return ErrBadSnapshot
		}
		if _, exists := sessions[record.ID]; exists {
			return ErrBadSnapshot
		}
		copy := record
		sessions[record.ID] = &copy
		liveCounts[key]++
		if record.Seq > maxSeq {
			maxSeq = record.Seq
		}
	}
	for key, binding := range bindings {
		if binding.Sessions != liveCounts[key] {
			return ErrBadSnapshot
		}
	}

	events := make(map[string][]DriftEvent, len(state.Events))
	for userID, records := range state.Events {
		if strings.TrimSpace(userID) == "" {
			return ErrBadSnapshot
		}
		for _, record := range records {
			if strings.TrimSpace(record.SessionID) == "" || strings.TrimSpace(record.DeviceID) == "" ||
				!validAction(record.Action) || record.Score < 0 || record.Score > FingerprintMaxScore {
				return ErrBadSnapshot
			}
			if record.Seq > maxSeq {
				maxSeq = record.Seq
			}
		}
		if b.limits.MaxEvents <= 0 {
			continue
		}
		if len(records) > b.limits.MaxEvents {
			records = records[len(records)-b.limits.MaxEvents:]
		}
		events[userID] = append([]DriftEvent(nil), records...)
	}
	if state.NextSeq < maxSeq {
		return ErrBadSnapshot
	}

	b.nextSeq = state.NextSeq
	b.bindings = bindings
	b.sessions = sessions
	b.events = events
	return nil
}
