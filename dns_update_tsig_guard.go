package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors reported by TsigAuthzGuard.
var (
	ErrKeyConfig         = errors.New("tsig: invalid key specification")
	ErrKeyExists         = errors.New("tsig: key name already bound")
	ErrKeyUnknown        = errors.New("tsig: key name not bound")
	ErrKeyCapacity       = errors.New("tsig: key table full")
	ErrKeyRevoked        = errors.New("tsig: key has been revoked")
	ErrAlgorithmMismatch = errors.New("tsig: algorithm does not match the key")
	ErrGenerationStale   = errors.New("tsig: key generation is stale")
	ErrDigestMismatch    = errors.New("tsig: request digest mismatch")
	ErrSkewTooLarge      = errors.New("tsig: request time outside the allowed window")
	ErrZoneNotGranted    = errors.New("tsig: zone not granted to the key")
	ErrTypeNotGranted    = errors.New("tsig: record type not granted")
	ErrNameNotOwned      = errors.New("tsig: name is outside the zone")
	ErrConditionUnmet    = errors.New("tsig: update prerequisite not met")
	ErrRequestReplay     = errors.New("tsig: request id already seen")
	ErrGrantConfig       = errors.New("tsig: invalid zone grant")
	ErrPendingCapacity   = errors.New("tsig: pending request table full")
	ErrTsigSnapshot      = errors.New("tsig: corrupt snapshot")
)

// TsigKeySpec is one bound TSIG key.
type TsigKeySpec struct {
	Name       string
	Algorithm  string
	DigestTag  string
	Generation int
}

// ZoneGrant authorises keys and record types for one zone.
type ZoneGrant struct {
	Zone  string
	Keys  []string
	Types []string
}

// DnsUpdate is one dynamic update request.
type DnsUpdate struct {
	RequestID    string
	KeyName      string
	Algorithm    string
	Generation   int
	Zone         string
	Name         string
	Type         string
	At           int64
	DigestTag    string
	ExpectExists bool
}

// VerifiedUpdate is the token issued for an accepted request.
type VerifiedUpdate struct {
	RequestID string
	Zone      string
	Name      string
	Type      string
	DigestTag string
}

// TsigOptions carries the guard limits.
type TsigOptions struct {
	WindowSeconds int
	MaxPending    int
	MaxKeys       int
}

// TsigStats summarises guard activity.
type TsigStats struct {
	Keys     int
	Verified int
	Applied  int
	Rejected int
	Pending  int
	Failures map[string]int
}

type keyState struct {
	spec     TsigKeySpec
	revoked  bool
	failures int
}

type grantState struct {
	keys  map[string]struct{}
	types map[string]struct{}
}

type seenUpdate struct {
	verified     VerifiedUpdate
	keyName      string
	expectExists bool
	applied      bool
}

// TsigAuthzGuard validates TSIG signed DNS dynamic updates.
type TsigAuthzGuard struct {
	mu sync.Mutex

	opts    TsigOptions
	keys    map[string]*keyState
	grants  map[string]*grantState
	serials map[string]int64
	names   map[string]map[string]struct{}
	seen    map[string]*seenUpdate

	verified int
	applied  int
	rejected int
}

// NewTsigAuthzGuard builds a guard with the supplied limits.
func NewTsigAuthzGuard(opts TsigOptions) *TsigAuthzGuard {
	g := &TsigAuthzGuard{opts: opts}
	g.initLocked()
	return g
}

func (g *TsigAuthzGuard) initLocked() {
	if g.keys == nil {
		g.keys = make(map[string]*keyState)
	}
	if g.grants == nil {
		g.grants = make(map[string]*grantState)
	}
	if g.serials == nil {
		g.serials = make(map[string]int64)
	}
	if g.names == nil {
		g.names = make(map[string]map[string]struct{})
	}
	if g.seen == nil {
		g.seen = make(map[string]*seenUpdate)
	}
}

func canonicalName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".")
	if s == "" {
		return ""
	}
	return s + "."
}

func canonicalZone(s string) string {
	return canonicalName(s)
}

func canonicalKeyName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimRight(s, ".")
}

func canonicalAlgorithm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func canonicalType(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func validAlgorithm(s string) bool {
	return s == "hmac-sha256" || s == "hmac-sha512"
}

func inZone(name, zone string) bool {
	if name == "" || zone == "" {
		return false
	}
	return name == zone || strings.HasSuffix(name, "."+zone)
}

func (g *TsigAuthzGuard) failKeyLocked(name string) {
	if k := g.keys[name]; k != nil {
		k.failures++
	}
}

func (g *TsigAuthzGuard) rejectLocked(keyName string, countFailure bool) {
	g.rejected++
	if countFailure {
		g.failKeyLocked(keyName)
	}
}

func (g *TsigAuthzGuard) pendingLocked() int {
	n := 0
	for _, entry := range g.seen {
		if !entry.applied {
			n++
		}
	}
	return n
}

// BindKey registers a TSIG key.
func (g *TsigAuthzGuard) BindKey(spec TsigKeySpec) error {
	name := canonicalKeyName(spec.Name)
	algorithm := canonicalAlgorithm(spec.Algorithm)
	digest := strings.TrimSpace(spec.DigestTag)
	if name == "" || digest == "" || !validAlgorithm(algorithm) {
		return ErrKeyConfig
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	if _, ok := g.keys[name]; ok {
		return ErrKeyExists
	}
	if g.opts.MaxKeys > 0 && len(g.keys) >= g.opts.MaxKeys {
		return ErrKeyCapacity
	}

	g.keys[name] = &keyState{
		spec: TsigKeySpec{
			Name:       name,
			Algorithm:  algorithm,
			DigestTag:  digest,
			Generation: 1,
		},
	}
	return nil
}

// RevokeKey revokes a bound key immediately.
func (g *TsigAuthzGuard) RevokeKey(name string) error {
	name = canonicalKeyName(name)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	k, ok := g.keys[name]
	if !ok {
		return ErrKeyUnknown
	}
	k.revoked = true
	return nil
}

// RotateKey advances the key generation and switches the algorithm.
func (g *TsigAuthzGuard) RotateKey(name, algorithm string) (TsigKeySpec, error) {
	name = canonicalKeyName(name)
	algorithm = canonicalAlgorithm(algorithm)
	if !validAlgorithm(algorithm) {
		return TsigKeySpec{}, ErrKeyConfig
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	k, ok := g.keys[name]
	if !ok {
		return TsigKeySpec{}, ErrKeyUnknown
	}
	if k.revoked {
		return TsigKeySpec{}, ErrKeyRevoked
	}
	k.spec.Generation++
	k.spec.Algorithm = algorithm
	return k.spec, nil
}

// SetZoneGrant replaces the authorisation of one zone.
func (g *TsigAuthzGuard) SetZoneGrant(grant ZoneGrant) error {
	zone := canonicalZone(grant.Zone)
	if zone == "" || len(grant.Keys) == 0 || len(grant.Types) == 0 {
		return ErrGrantConfig
	}

	keys := make(map[string]struct{}, len(grant.Keys))
	for _, raw := range grant.Keys {
		name := canonicalKeyName(raw)
		if name == "" {
			return ErrGrantConfig
		}
		keys[name] = struct{}{}
	}
	types := make(map[string]struct{}, len(grant.Types))
	for _, raw := range grant.Types {
		typ := canonicalType(raw)
		if typ == "" {
			return ErrGrantConfig
		}
		types[typ] = struct{}{}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	g.grants[zone] = &grantState{keys: keys, types: types}
	return nil
}

// Verify checks key selection, time window, digest and zone ACL.
func (g *TsigAuthzGuard) Verify(update DnsUpdate, now int64) (VerifiedUpdate, error) {
	keyName := canonicalKeyName(update.KeyName)
	algorithm := canonicalAlgorithm(update.Algorithm)
	zone := canonicalZone(update.Zone)
	name := canonicalName(update.Name)
	typ := canonicalType(update.Type)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	k, ok := g.keys[keyName]
	if !ok {
		g.rejectLocked("", false)
		return VerifiedUpdate{}, ErrKeyUnknown
	}
	if k.revoked {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrKeyRevoked
	}
	if update.Generation != k.spec.Generation {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrGenerationStale
	}
	if algorithm != k.spec.Algorithm {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrAlgorithmMismatch
	}
	if update.DigestTag != k.spec.DigestTag {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrDigestMismatch
	}

	window := int64(g.opts.WindowSeconds)
	if window < 0 {
		window = 0
	}
	var delta int64
	if now >= update.At {
		delta = now - update.At
	} else {
		delta = update.At - now
	}
	if delta > window {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrSkewTooLarge
	}

	if _, exists := g.seen[update.RequestID]; exists {
		g.rejectLocked(keyName, false)
		return VerifiedUpdate{}, ErrRequestReplay
	}

	grant := g.grants[zone]
	if grant == nil {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrZoneNotGranted
	}
	if _, allowed := grant.keys[keyName]; !allowed {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrZoneNotGranted
	}
	if _, allowed := grant.types[typ]; !allowed {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrTypeNotGranted
	}
	if !inZone(name, zone) {
		g.rejectLocked(keyName, true)
		return VerifiedUpdate{}, ErrNameNotOwned
	}
	if update.ExpectExists {
		if _, exists := g.names[zone][name]; !exists {
			g.rejectLocked(keyName, true)
			return VerifiedUpdate{}, ErrConditionUnmet
		}
	}
	if g.opts.MaxPending > 0 && g.pendingLocked() >= g.opts.MaxPending {
		g.rejectLocked(keyName, false)
		return VerifiedUpdate{}, ErrPendingCapacity
	}

	verified := VerifiedUpdate{
		RequestID: update.RequestID,
		Zone:      update.Zone,
		Name:      update.Name,
		Type:      update.Type,
		DigestTag: update.DigestTag,
	}
	g.seen[update.RequestID] = &seenUpdate{
		verified:     verified,
		keyName:      keyName,
		expectExists: update.ExpectExists,
	}
	g.verified++
	return verified, nil
}

// ApplyUpdate commits one verified update and returns the new SOA serial.
func (g *TsigAuthzGuard) ApplyUpdate(v VerifiedUpdate) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	entry, ok := g.seen[v.RequestID]
	if !ok || entry.verified != v {
		g.rejected++
		return 0, ErrRequestReplay
	}
	if entry.applied {
		g.rejected++
		return 0, ErrRequestReplay
	}

	k := g.keys[entry.keyName]
	if k == nil {
		g.rejected++
		return 0, ErrKeyUnknown
	}
	if k.revoked {
		g.rejected++
		return 0, ErrKeyRevoked
	}

	zone := canonicalZone(v.Zone)
	name := canonicalName(v.Name)
	if entry.expectExists {
		if _, exists := g.names[zone][name]; !exists {
			g.rejected++
			return 0, ErrConditionUnmet
		}
	}

	entry.applied = true
	g.serials[zone]++
	if g.names[zone] == nil {
		g.names[zone] = make(map[string]struct{})
	}
	g.names[zone][name] = struct{}{}
	g.applied++
	return g.serials[zone], nil
}

// Serial returns the current SOA serial of a zone.
func (g *TsigAuthzGuard) Serial(zone string) int64 {
	zone = canonicalZone(zone)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.serials[zone]
}

// Pending returns the number of requests accepted but not yet applied.
func (g *TsigAuthzGuard) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.pendingLocked()
}

// KeyFailures returns the failure counter of one key.
func (g *TsigAuthzGuard) KeyFailures(name string) int {
	name = canonicalKeyName(name)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if k := g.keys[name]; k != nil {
		return k.failures
	}
	return 0
}

const (
	snapshotMagic   = "gsb-tsig-authz-guard"
	snapshotVersion = 1
)

type snapshotKey struct {
	Name       string `json:"name"`
	Algorithm  string `json:"algorithm"`
	DigestTag  string `json:"digest_tag"`
	Generation int    `json:"generation"`
	Revoked    bool   `json:"revoked"`
	Failures   int    `json:"failures"`
}

type snapshotGrant struct {
	Zone  string   `json:"zone"`
	Keys  []string `json:"keys"`
	Types []string `json:"types"`
}

type snapshotRequest struct {
	RequestID    string `json:"request_id"`
	Zone         string `json:"zone"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	DigestTag    string `json:"digest_tag"`
	KeyName      string `json:"key_name"`
	ExpectExists bool   `json:"expect_exists"`
	Applied      bool   `json:"applied"`
}

type snapshotPayload struct {
	Version  int                 `json:"version"`
	Options  TsigOptions         `json:"options"`
	Keys     []snapshotKey       `json:"keys"`
	Grants   []snapshotGrant     `json:"grants"`
	Serials  map[string]int64    `json:"serials"`
	Names    map[string][]string `json:"names"`
	Requests []snapshotRequest   `json:"requests"`
	Verified int                 `json:"verified"`
	Applied  int                 `json:"applied"`
	Rejected int                 `json:"rejected"`
}

type snapshotEnvelope struct {
	Magic   string          `json:"magic"`
	Version int             `json:"version"`
	Digest  string          `json:"digest"`
	Payload json.RawMessage `json:"payload"`
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]struct{}) []string {
	return sortedKeys(m)
}

// Snapshot serialises keys, grants, serials and the dedup window.
func (g *TsigAuthzGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	payload := snapshotPayload{
		Version:  snapshotVersion,
		Options:  g.opts,
		Serials:  make(map[string]int64, len(g.serials)),
		Names:    make(map[string][]string, len(g.names)),
		Verified: g.verified,
		Applied:  g.applied,
		Rejected: g.rejected,
	}

	for _, name := range sortedKeys(g.keys) {
		k := g.keys[name]
		payload.Keys = append(payload.Keys, snapshotKey{
			Name:       k.spec.Name,
			Algorithm:  k.spec.Algorithm,
			DigestTag:  k.spec.DigestTag,
			Generation: k.spec.Generation,
			Revoked:    k.revoked,
			Failures:   k.failures,
		})
	}
	for _, zone := range sortedKeys(g.grants) {
		grant := g.grants[zone]
		payload.Grants = append(payload.Grants, snapshotGrant{
			Zone:  zone,
			Keys:  sortedSet(grant.keys),
			Types: sortedSet(grant.types),
		})
	}
	for zone, serial := range g.serials {
		payload.Serials[zone] = serial
	}
	for zone, names := range g.names {
		payload.Names[zone] = sortedSet(names)
	}
	for _, id := range sortedKeys(g.seen) {
		entry := g.seen[id]
		payload.Requests = append(payload.Requests, snapshotRequest{
			RequestID:    entry.verified.RequestID,
			Zone:         entry.verified.Zone,
			Name:         entry.verified.Name,
			Type:         entry.verified.Type,
			DigestTag:    entry.verified.DigestTag,
			KeyName:      entry.keyName,
			ExpectExists: entry.expectExists,
			Applied:      entry.applied,
		})
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	env := snapshotEnvelope{
		Magic:   snapshotMagic,
		Version: snapshotVersion,
		Digest:  hex.EncodeToString(sum[:]),
		Payload: data,
	}
	return json.Marshal(env)
}

// Restore rebuilds the guard from a snapshot.
func (g *TsigAuthzGuard) Restore(data []byte) error {
	var env snapshotEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ErrTsigSnapshot
	}
	if env.Magic != snapshotMagic || env.Version != snapshotVersion {
		return ErrTsigSnapshot
	}
	want, err := hex.DecodeString(env.Digest)
	if err != nil || len(want) != sha256.Size {
		return ErrTsigSnapshot
	}
	sum := sha256.Sum256(env.Payload)
	if subtle.ConstantTimeCompare(want, sum[:]) != 1 {
		return ErrTsigSnapshot
	}

	var payload snapshotPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return ErrTsigSnapshot
	}
	if payload.Version != snapshotVersion || payload.Verified < 0 || payload.Applied < 0 || payload.Rejected < 0 {
		return ErrTsigSnapshot
	}

	keys := make(map[string]*keyState, len(payload.Keys))
	for _, sk := range payload.Keys {
		name := canonicalKeyName(sk.Name)
		algorithm := canonicalAlgorithm(sk.Algorithm)
		digest := strings.TrimSpace(sk.DigestTag)
		if name == "" || digest == "" || !validAlgorithm(algorithm) || sk.Generation < 1 || sk.Failures < 0 {
			return ErrTsigSnapshot
		}
		if _, exists := keys[name]; exists {
			return ErrTsigSnapshot
		}
		keys[name] = &keyState{
			spec: TsigKeySpec{
				Name:       name,
				Algorithm:  algorithm,
				DigestTag:  digest,
				Generation: sk.Generation,
			},
			revoked:  sk.Revoked,
			failures: sk.Failures,
		}
	}

	grants := make(map[string]*grantState, len(payload.Grants))
	for _, sg := range payload.Grants {
		zone := canonicalZone(sg.Zone)
		if zone == "" || len(sg.Keys) == 0 || len(sg.Types) == 0 {
			return ErrTsigSnapshot
		}
		grant := &grantState{
			keys:  make(map[string]struct{}, len(sg.Keys)),
			types: make(map[string]struct{}, len(sg.Types)),
		}
		for _, raw := range sg.Keys {
			name := canonicalKeyName(raw)
			if name == "" {
				return ErrTsigSnapshot
			}
			grant.keys[name] = struct{}{}
		}
		for _, raw := range sg.Types {
			typ := canonicalType(raw)
			if typ == "" {
				return ErrTsigSnapshot
			}
			grant.types[typ] = struct{}{}
		}
		if _, exists := grants[zone]; exists {
			return ErrTsigSnapshot
		}
		grants[zone] = grant
	}

	serials := make(map[string]int64, len(payload.Serials))
	for rawZone, serial := range payload.Serials {
		zone := canonicalZone(rawZone)
		if zone == "" || serial < 0 {
			return ErrTsigSnapshot
		}
		if _, exists := serials[zone]; exists {
			return ErrTsigSnapshot
		}
		serials[zone] = serial
	}

	names := make(map[string]map[string]struct{}, len(payload.Names))
	for rawZone, rawNames := range payload.Names {
		zone := canonicalZone(rawZone)
		if zone == "" {
			return ErrTsigSnapshot
		}
		if _, exists := names[zone]; exists {
			return ErrTsigSnapshot
		}
		nameSet := make(map[string]struct{}, len(rawNames))
		for _, rawName := range rawNames {
			name := canonicalName(rawName)
			if !inZone(name, zone) {
				return ErrTsigSnapshot
			}
			nameSet[name] = struct{}{}
		}
		names[zone] = nameSet
	}

	seen := make(map[string]*seenUpdate, len(payload.Requests))
	for _, sr := range payload.Requests {
		keyName := canonicalKeyName(sr.KeyName)
		zone := canonicalZone(sr.Zone)
		name := canonicalName(sr.Name)
		if _, exists := seen[sr.RequestID]; exists || keyName == "" || zone == "" || !inZone(name, zone) {
			return ErrTsigSnapshot
		}
		if _, exists := keys[keyName]; !exists {
			return ErrTsigSnapshot
		}
		seen[sr.RequestID] = &seenUpdate{
			verified: VerifiedUpdate{
				RequestID: sr.RequestID,
				Zone:      sr.Zone,
				Name:      sr.Name,
				Type:      sr.Type,
				DigestTag: sr.DigestTag,
			},
			keyName:      keyName,
			expectExists: sr.ExpectExists,
			applied:      sr.Applied,
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.opts = payload.Options
	g.keys = keys
	g.grants = grants
	g.serials = serials
	g.names = names
	g.seen = seen
	g.verified = payload.Verified
	g.applied = payload.Applied
	g.rejected = payload.Rejected
	return nil
}

// Stats returns the activity counters.
func (g *TsigAuthzGuard) Stats() TsigStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	failures := make(map[string]int, len(g.keys))
	for name, k := range g.keys {
		failures[name] = k.failures
	}
	return TsigStats{
		Keys:     len(g.keys),
		Verified: g.verified,
		Applied:  g.applied,
		Rejected: g.rejected,
		Pending:  g.pendingLocked(),
		Failures: failures,
	}
}
