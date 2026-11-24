package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

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
	mu         sync.Mutex
	opts       UsmOptions
	users      map[string]*usmRecord
	views      map[string][]ViewEntry
	targets    map[string]TrapTarget
	boots      int
	engineTime int
	locked     bool
	stats      UsmStats
}

type usmRecord struct {
	user       UsmUser
	generation int
}

// maxEngineTime is the RFC 3411 snmpEngineTime wrap point (2^31-1 seconds).
const maxEngineTime = 2147483647

const snapshotVersion = 1

var supportedAuthProtos = map[string]bool{
	"sha1":   true,
	"sha224": true,
	"sha256": true,
	"sha384": true,
	"sha512": true,
}

var supportedPrivProtos = map[string]bool{
	"des":    true,
	"aes128": true,
	"aes192": true,
	"aes256": true,
}

// NewUsmGuard builds a guard with the supplied limits.
func NewUsmGuard(opts UsmOptions) *UsmGuard {
	return &UsmGuard{
		opts:    opts,
		users:   make(map[string]*usmRecord),
		views:   make(map[string][]ViewEntry),
		targets: make(map[string]TrapTarget),
		boots:   1,
	}
}

// AddUser registers a USM user.
func (g *UsmGuard) AddUser(u UsmUser) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u.Name == "" {
		return ErrUserConfig
	}
	needsAuth := u.Level == "authNoPriv" || u.Level == "authPriv"
	needsPriv := u.Level == "authPriv"
	switch u.Level {
	case "noAuthNoPriv", "authNoPriv", "authPriv":
	default:
		return ErrUserConfig
	}
	if u.AuthProto != "" && !supportedAuthProtos[u.AuthProto] {
		return ErrUserConfig
	}
	if u.PrivProto != "" && !supportedPrivProtos[u.PrivProto] {
		return ErrUserConfig
	}
	if needsAuth {
		if u.AuthProto == "" || !supportedAuthProtos[u.AuthProto] {
			return ErrUserConfig
		}
		if u.AuthPass == "" {
			return ErrUserConfig
		}
	}
	if needsPriv {
		if u.PrivProto == "" || !supportedPrivProtos[u.PrivProto] {
			return ErrUserConfig
		}
		if u.PrivPass == "" {
			return ErrUserConfig
		}
	}
	if _, ok := g.users[u.Name]; ok {
		return ErrUserExists
	}
	if g.opts.MaxUsers > 0 && len(g.users) >= g.opts.MaxUsers {
		return ErrUserLimit
	}
	g.users[u.Name] = &usmRecord{user: u, generation: 1}
	return nil
}

// RemoveUser removes a USM user and invalidates its sessions.
func (g *UsmGuard) RemoveUser(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.users[name]; !ok {
		return ErrUserUnknown
	}
	delete(g.users, name)
	return nil
}

// deriveKey deterministically localizes a passphrase to this engine.
func deriveKey(scope, engineID, user, pass string, generation int) string {
	h := sha256.New()
	fields := []string{scope, engineID, user, pass, strconv.Itoa(generation)}
	for _, f := range fields {
		io.WriteString(h, f)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Localize derives the engine scoped auth and privacy keys of a user.
func (g *UsmGuard) Localize(user string) (UsmKeys, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.users[user]
	if !ok {
		return UsmKeys{}, ErrUserUnknown
	}
	keys := UsmKeys{
		User:       user,
		EngineID:   g.opts.EngineID,
		Generation: rec.generation,
	}
	if rec.user.AuthPass != "" {
		keys.AuthKey = deriveKey("auth", g.opts.EngineID, user, rec.user.AuthPass, rec.generation)
	}
	if rec.user.PrivPass != "" {
		keys.PrivKey = deriveKey("priv", g.opts.EngineID, user, rec.user.PrivPass, rec.generation)
	}
	return keys, nil
}

// RotateUserKeys advances the localized key generation of a user.
func (g *UsmGuard) RotateUserKeys(user string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.users[user]
	if !ok {
		return 0, ErrUserUnknown
	}
	rec.generation++
	return rec.generation, nil
}

// VerifyMessage validates one inbound SNMPv3 message.
func (g *UsmGuard) VerifyMessage(msg UsmMessage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.verifyLocked(msg)
	if err != nil {
		g.stats.Rejected++
		return err
	}
	g.stats.Verified++
	return nil
}

func (g *UsmGuard) verifyLocked(msg UsmMessage) error {
	rec, ok := g.users[msg.User]
	if !ok {
		return ErrUserUnknown
	}
	if msg.EngineID != g.opts.EngineID {
		return ErrEngineMismatch
	}
	if g.locked {
		return ErrTimeWindow
	}
	if msg.Boots < g.boots {
		return ErrTimeWindow
	}
	if g.opts.TimeWindow > 0 {
		delta := msg.EngineTime - g.engineTime
		if delta < 0 {
			delta = -delta
		}
		if delta > g.opts.TimeWindow {
			return ErrTimeWindow
		}
	}
	if msg.Generation != rec.generation {
		return ErrKeyGeneration
	}
	level := rec.user.Level
	if (level == "authNoPriv" || level == "authPriv") && !msg.AuthOK {
		return ErrAuthProof
	}
	if level == "authPriv" && !msg.PrivOK {
		return ErrPrivProof
	}
	return nil
}

// SetView registers an OID view.
func (g *UsmGuard) SetView(name string, entries []ViewEntry) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if name == "" || len(entries) == 0 {
		return ErrViewConfig
	}
	for _, e := range entries {
		if e.OID == "" {
			return ErrViewConfig
		}
	}
	cp := make([]ViewEntry, len(entries))
	copy(cp, entries)
	g.views[name] = cp
	return nil
}

// AddTrapTarget registers a notification receiver.
func (g *UsmGuard) AddTrapTarget(t TrapTarget) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.Name == "" {
		return ErrTargetConfig
	}
	if _, ok := g.views[t.View]; !ok {
		return ErrViewUnknown
	}
	if _, ok := g.users[t.User]; !ok {
		return ErrUserUnknown
	}
	if _, ok := g.targets[t.Name]; ok {
		return ErrTargetConfig
	}
	if g.opts.MaxTargets > 0 && len(g.targets) >= g.opts.MaxTargets {
		return ErrTargetLimit
	}
	g.targets[t.Name] = t
	return nil
}

// oidCovers reports whether prefix is a dot-boundary aware prefix of oid.
func oidCovers(prefix, oid string) bool {
	return oid == prefix || strings.HasPrefix(oid, prefix+".")
}

// viewAllows applies the longest matching view entry to one varbind OID.
func viewAllows(entries []ViewEntry, oid string) bool {
	best := -1
	allowed := false
	for _, e := range entries {
		if oidCovers(e.OID, oid) && len(e.OID) > best {
			best = len(e.OID)
			allowed = e.Included
		}
	}
	return allowed
}

// ProjectTrap projects varbinds through the view of a trap target.
func (g *UsmGuard) ProjectTrap(target string, varbinds []string) (TrapNotification, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.targets[target]
	if !ok {
		return TrapNotification{}, ErrTargetUnknown
	}
	entries := g.views[t.View]
	note := TrapNotification{Target: target, Varbinds: []string{}}
	for _, vb := range varbinds {
		if viewAllows(entries, vb) {
			note.Varbinds = append(note.Varbinds, vb)
		} else {
			note.Dropped++
		}
	}
	note.Sent = true
	g.stats.Traps++
	g.stats.Filtered += note.Dropped
	return note, nil
}

// ResetEngineBoots performs the explicit administrator reset of the engine boots counter.
func (g *UsmGuard) ResetEngineBoots(operator string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if operator == "" {
		return ErrBootsResetDenied
	}
	g.boots = 1
	g.engineTime = 0
	g.locked = false
	return nil
}

// AdvanceEngineTime advances the local engine time and returns the new value.
func (g *UsmGuard) AdvanceEngineTime(delta int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.engineTime += delta
	if g.engineTime < 0 {
		g.engineTime = 0
	}
	for g.engineTime > maxEngineTime {
		g.engineTime -= maxEngineTime
		if g.opts.MaxBoots > 0 && g.boots >= g.opts.MaxBoots {
			// Refuse to wrap the boots counter: lock the guard until an
			// administrator performs an explicit reset.
			g.locked = true
		} else {
			g.boots++
		}
	}
	return g.engineTime
}

// EngineBoots returns the current engine boots counter.
func (g *UsmGuard) EngineBoots() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.boots
}

type usmSnapshotUser struct {
	User       UsmUser
	Generation int
}

type usmSnapshot struct {
	Version    int
	Users      map[string]usmSnapshotUser
	Views      map[string][]ViewEntry
	Targets    map[string]TrapTarget
	Boots      int
	EngineTime int
	Locked     bool
	Stats      UsmStats
}

// Snapshot serialises users, keys, engine state, views and targets.
func (g *UsmGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := usmSnapshot{
		Version:    snapshotVersion,
		Users:      make(map[string]usmSnapshotUser, len(g.users)),
		Views:      make(map[string][]ViewEntry, len(g.views)),
		Targets:    make(map[string]TrapTarget, len(g.targets)),
		Boots:      g.boots,
		EngineTime: g.engineTime,
		Locked:     g.locked,
		Stats:      g.stats,
	}
	snap.Stats.Users = len(g.users)
	for name, rec := range g.users {
		snap.Users[name] = usmSnapshotUser{User: rec.user, Generation: rec.generation}
	}
	for name, entries := range g.views {
		cp := make([]ViewEntry, len(entries))
		copy(cp, entries)
		snap.Views[name] = cp
	}
	for name, t := range g.targets {
		snap.Targets[name] = t
	}
	return json.Marshal(snap)
}

// Restore rebuilds the guard from a snapshot.
func (g *UsmGuard) Restore(data []byte) error {
	var snap usmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrUsmSnapshot
	}
	if snap.Version != snapshotVersion || snap.Users == nil || snap.Views == nil ||
		snap.Targets == nil || snap.Boots < 1 {
		return ErrUsmSnapshot
	}
	users := make(map[string]*usmRecord, len(snap.Users))
	for name, su := range snap.Users {
		if su.Generation < 1 {
			return ErrUsmSnapshot
		}
		users[name] = &usmRecord{user: su.User, generation: su.Generation}
	}
	views := make(map[string][]ViewEntry, len(snap.Views))
	for name, entries := range snap.Views {
		if len(entries) == 0 {
			return ErrUsmSnapshot
		}
		cp := make([]ViewEntry, len(entries))
		copy(cp, entries)
		views[name] = cp
	}
	targets := make(map[string]TrapTarget, len(snap.Targets))
	for name, t := range snap.Targets {
		targets[name] = t
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users = users
	g.views = views
	g.targets = targets
	g.boots = snap.Boots
	g.engineTime = snap.EngineTime
	g.locked = snap.Locked
	g.stats = snap.Stats
	return nil
}

// Stats returns the activity counters.
func (g *UsmGuard) Stats() UsmStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	stats := g.stats
	stats.Users = len(g.users)
	return stats
}
