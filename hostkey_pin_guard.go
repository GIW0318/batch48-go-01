package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sentinel errors reported by HostKeyPinGuard.
var (
	ErrPinInput            = errors.New("hostkey: invalid pin request")
	ErrTrustRequired       = errors.New("hostkey: explicit first use trust required")
	ErrHostUnknown         = errors.New("hostkey: host has no pinned key")
	ErrFingerprintMismatch = errors.New("hostkey: fingerprint does not match the pin")
	ErrEntryLimit          = errors.New("hostkey: tenant pin table full")
	ErrPinnedAlready       = errors.New("hostkey: address already pinned")
	ErrHostRevoked         = errors.New("hostkey: host key revoked")
	ErrHopBudget           = errors.New("hostkey: hop budget invalid")
	ErrRotationActive      = errors.New("hostkey: rotation already in progress")
	ErrNoRotation          = errors.New("hostkey: no rotation in progress")
	ErrPinSnapshot         = errors.New("hostkey: corrupt snapshot")
)

// PinRequest is a first use trust decision for one host.
type PinRequest struct {
	Tenant       string
	Hostname     string
	Port         int
	Addresses    []string
	Fingerprints map[string]string
	Trust        bool
	At           int64
}

// VerifyRequest is one host key verification.
type VerifyRequest struct {
	Tenant    string
	Hostname  string
	Port      int
	Presented map[string]string
	At        int64
}

// PinDecision is the outcome of a pin or verify call.
type PinDecision struct {
	Allowed     bool
	Reason      string
	Fingerprint string
	NewHost     bool
}

// HostKeyEntry is one pinned address of a host.
type HostKeyEntry struct {
	Tenant      string
	Hostname    string
	Port        int
	Address     string
	Fingerprint string
	PinnedAt    int64
	Status      string
}

// PinAudit is one recorded decision.
type PinAudit struct {
	Action   string
	Hostname string
	Port     int
	Address  string
	Reason   string
}

// PinOptions carries the guard limits.
type PinOptions struct {
	MaxEntriesPerTenant int
	OverlapSeconds      int
	MaxHops             int
}

// PinStats summarises guard activity.
type PinStats struct {
	Pinned     int
	Verified   int
	Denied     int
	Revoked    int
	Propagated int
	Rotations  int
}

type hostKey struct {
	tenant   string
	hostname string
	port     int
}

type addressKey struct {
	hostKey
	address string
}

type rotationState struct {
	newFingerprint string
	startedAt      int64
	deadline       int64
	observedOld    bool
	observedNew    bool
}

type revocationState struct {
	revokedAt int64
}

// HostKeyPinGuard maintains host key pins per tenant, host and port.
type HostKeyPinGuard struct {
	mu        sync.Mutex
	opts      PinOptions
	pins      map[addressKey]HostKeyEntry
	rotations map[hostKey]*rotationState
	revoked   map[hostKey]revocationState
	audits    []PinAudit
	stats     PinStats
}

// NewHostKeyPinGuard builds a guard with the supplied limits.
func NewHostKeyPinGuard(opts PinOptions) *HostKeyPinGuard {
	return &HostKeyPinGuard{
		opts:      opts,
		pins:      make(map[addressKey]HostKeyEntry),
		rotations: make(map[hostKey]*rotationState),
		revoked:   make(map[hostKey]revocationState),
	}
}

func (g *HostKeyPinGuard) ensureLocked() {
	if g.pins == nil {
		g.pins = make(map[addressKey]HostKeyEntry)
	}
	if g.rotations == nil {
		g.rotations = make(map[hostKey]*rotationState)
	}
	if g.revoked == nil {
		g.revoked = make(map[hostKey]revocationState)
	}
}

func (g *HostKeyPinGuard) auditLocked(action, hostname string, port int, address, reason string) {
	g.audits = append(g.audits, PinAudit{
		Action:   action,
		Hostname: hostname,
		Port:     port,
		Address:  address,
		Reason:   reason,
	})
}

func (g *HostKeyPinGuard) tenantEntryCountLocked(tenant string) int {
	count := 0
	for key := range g.pins {
		if key.tenant == tenant {
			count++
		}
	}
	return count
}

func (g *HostKeyPinGuard) hostEntryCountLocked(hk hostKey) int {
	count := 0
	for key := range g.pins {
		if key.hostKey == hk {
			count++
		}
	}
	return count
}

func (g *HostKeyPinGuard) hostEntriesLocked(hk hostKey) []HostKeyEntry {
	entries := make([]HostKeyEntry, 0)
	for key, entry := range g.pins {
		if key.hostKey == hk {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Address < entries[j].Address
	})
	return entries
}

func (g *HostKeyPinGuard) commitRotationLocked(hk hostKey, rotation *rotationState) {
	for key, entry := range g.pins {
		if key.hostKey != hk {
			continue
		}
		entry.Fingerprint = rotation.newFingerprint
		g.pins[key] = entry
	}
	delete(g.rotations, hk)
}

func (g *HostKeyPinGuard) rollbackRotationLocked(hk hostKey) {
	delete(g.rotations, hk)
}

func (g *HostKeyPinGuard) deleteHostEntryLocked(hk hostKey) {
	for key := range g.pins {
		if key.hostKey == hk {
			delete(g.pins, key)
		}
	}
}

func validatePinRequest(req PinRequest) (string, bool) {
	if req.Tenant == "" || req.Hostname == "" || req.Port <= 0 || len(req.Addresses) == 0 {
		return "", false
	}
	if len(req.Fingerprints) != len(req.Addresses) {
		return "", false
	}

	seen := make(map[string]struct{}, len(req.Addresses))
	for _, address := range req.Addresses {
		if address == "" {
			return address, false
		}
		if _, ok := seen[address]; ok {
			return address, false
		}
		seen[address] = struct{}{}

		fingerprint, ok := req.Fingerprints[address]
		if !ok || fingerprint == "" {
			return address, false
		}
	}
	return "", true
}

func validateVerifyRequest(req VerifyRequest) bool {
	if req.Tenant == "" || req.Hostname == "" || req.Port <= 0 || len(req.Presented) == 0 {
		return false
	}
	for address, fingerprint := range req.Presented {
		if address == "" || fingerprint == "" {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Pin records the explicit first use trust of a host.
func (g *HostKeyPinGuard) Pin(req PinRequest) (PinDecision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if address, ok := validatePinRequest(req); !ok {
		g.auditLocked("pin", req.Hostname, req.Port, address, "pin_input")
		if address != "" {
			return PinDecision{}, fmt.Errorf("%w: %s", ErrPinInput, address)
		}
		return PinDecision{}, ErrPinInput
	}
	if !req.Trust {
		g.auditLocked("pin", req.Hostname, req.Port, "", "trust_required")
		return PinDecision{}, ErrTrustRequired
	}

	hk := hostKey{tenant: req.Tenant, hostname: req.Hostname, port: req.Port}
	addresses := append([]string(nil), req.Addresses...)
	sort.Strings(addresses)

	for _, address := range addresses {
		if _, ok := g.pins[addressKey{hostKey: hk, address: address}]; ok {
			g.auditLocked("pin", req.Hostname, req.Port, address, "pinned_already")
			return PinDecision{}, fmt.Errorf("%w: %s", ErrPinnedAlready, address)
		}
	}

	if g.tenantEntryCountLocked(req.Tenant)+len(addresses) > g.opts.MaxEntriesPerTenant {
		g.auditLocked("pin", req.Hostname, req.Port, "", "entry_limit")
		return PinDecision{}, ErrEntryLimit
	}

	newHost := g.hostEntryCountLocked(hk) == 0
	delete(g.revoked, hk)

	for _, address := range addresses {
		entry := HostKeyEntry{
			Tenant:      req.Tenant,
			Hostname:    req.Hostname,
			Port:        req.Port,
			Address:     address,
			Fingerprint: req.Fingerprints[address],
			PinnedAt:    req.At,
			Status:      "pinned",
		}
		g.pins[addressKey{hostKey: hk, address: address}] = entry
		g.stats.Pinned++
		g.auditLocked("pin", req.Hostname, req.Port, address, "trusted_first_use")
	}

	decision := PinDecision{
		Allowed: true,
		Reason:  "trusted_first_use",
		NewHost: newHost,
	}
	if len(addresses) > 0 {
		decision.Fingerprint = req.Fingerprints[addresses[0]]
	}
	return decision, nil
}

// Verify checks a presented host key against the pinned fingerprints.
func (g *HostKeyPinGuard) Verify(req VerifyRequest) (PinDecision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if !validateVerifyRequest(req) {
		g.auditLocked("verify", req.Hostname, req.Port, "", "pin_input")
		g.stats.Denied++
		return PinDecision{}, ErrPinInput
	}

	hk := hostKey{tenant: req.Tenant, hostname: req.Hostname, port: req.Port}
	if _, ok := g.revoked[hk]; ok {
		g.auditLocked("verify", req.Hostname, req.Port, "", "host_revoked")
		g.stats.Denied++
		return PinDecision{}, ErrHostRevoked
	}
	if g.hostEntryCountLocked(hk) == 0 {
		g.auditLocked("verify", req.Hostname, req.Port, "", "host_unknown")
		g.stats.Denied++
		return PinDecision{}, ErrHostUnknown
	}

	rotation := g.rotations[hk]
	addresses := sortedKeys(req.Presented)

	type presentedMatch struct {
		address     string
		fingerprint string
		old         bool
		new         bool
	}
	matches := make([]presentedMatch, 0, len(addresses))
	for _, address := range addresses {
		fingerprint := req.Presented[address]
		entry, ok := g.pins[addressKey{hostKey: hk, address: address}]
		if !ok {
			if rotation != nil && req.At > rotation.deadline {
				if rotation.observedNew {
					g.commitRotationLocked(hk, rotation)
				} else {
					g.rollbackRotationLocked(hk)
				}
			}
			g.auditLocked("verify", req.Hostname, req.Port, address, "fingerprint_mismatch")
			g.stats.Denied++
			return PinDecision{}, fmt.Errorf("%w: %s", ErrFingerprintMismatch, address)
		}

		match := presentedMatch{
			address:     address,
			fingerprint: fingerprint,
			old:         fingerprint == entry.Fingerprint,
			new:         rotation != nil && fingerprint == rotation.newFingerprint,
		}
		if !match.old && !match.new {
			if rotation != nil && req.At > rotation.deadline {
				if rotation.observedNew {
					g.commitRotationLocked(hk, rotation)
				} else {
					g.rollbackRotationLocked(hk)
				}
			}
			g.auditLocked("verify", req.Hostname, req.Port, address, "fingerprint_mismatch")
			g.stats.Denied++
			return PinDecision{}, fmt.Errorf("%w: %s", ErrFingerprintMismatch, address)
		}
		matches = append(matches, match)
	}

	reason := "pinned_match"
	if rotation != nil {
		observedNew := rotation.observedNew
		observedOld := rotation.observedOld
		for _, match := range matches {
			observedNew = observedNew || match.new
			observedOld = observedOld || match.old
		}

		if req.At <= rotation.deadline {
			rotation.observedNew = observedNew
			rotation.observedOld = observedOld
			reason = "rotation_overlap"
		} else if observedNew {
			for _, match := range matches {
				if !match.new {
					g.commitRotationLocked(hk, rotation)
					g.auditLocked("verify", req.Hostname, req.Port, match.address, "fingerprint_mismatch")
					g.stats.Denied++
					return PinDecision{}, fmt.Errorf("%w: %s", ErrFingerprintMismatch, match.address)
				}
			}
			g.commitRotationLocked(hk, rotation)
		} else {
			g.rollbackRotationLocked(hk)
		}
	}

	g.stats.Verified++
	auditAddress := ""
	if len(matches) == 1 {
		auditAddress = matches[0].address
	}
	g.auditLocked("verify", req.Hostname, req.Port, auditAddress, reason)

	decision := PinDecision{
		Allowed: true,
		Reason:  reason,
	}
	if len(matches) > 0 {
		decision.Fingerprint = matches[0].fingerprint
	}
	return decision, nil
}

// BeginRotation opens the dual fingerprint overlap window of a host.
func (g *HostKeyPinGuard) BeginRotation(tenant, host string, port int, newFingerprint string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	hk := hostKey{tenant: tenant, hostname: host, port: port}
	if g.hostEntryCountLocked(hk) == 0 {
		g.auditLocked("rotate", host, port, "", "host_unknown")
		return ErrHostUnknown
	}
	if newFingerprint == "" {
		g.auditLocked("rotate", host, port, "", "pin_input")
		return ErrPinInput
	}
	if _, ok := g.rotations[hk]; ok {
		g.auditLocked("rotate", host, port, "", "rotation_active")
		return ErrRotationActive
	}

	overlap := int64(g.opts.OverlapSeconds)
	if overlap < 0 {
		overlap = 0
	}
	g.rotations[hk] = &rotationState{
		newFingerprint: newFingerprint,
		startedAt:      at,
		deadline:       at + overlap,
	}
	g.stats.Rotations++
	g.auditLocked("rotate", host, port, "", "rotation_started")
	return nil
}

// CompleteRotation retires the old fingerprint and keeps the new one.
func (g *HostKeyPinGuard) CompleteRotation(tenant, host string, port int, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	hk := hostKey{tenant: tenant, hostname: host, port: port}
	rotation, ok := g.rotations[hk]
	if !ok {
		g.auditLocked("rotate", host, port, "", "no_rotation")
		return ErrNoRotation
	}

	g.commitRotationLocked(hk, rotation)
	g.auditLocked("rotate", host, port, "", "rotation_completed")
	return nil
}

// Revoke invalidates every pin of a host and leaves a propagation tombstone.
func (g *HostKeyPinGuard) Revoke(tenant, host string, port int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	hk := hostKey{tenant: tenant, hostname: host, port: port}
	_, hasTombstone := g.revoked[hk]
	_, hasRotation := g.rotations[hk]
	if g.hostEntryCountLocked(hk) == 0 && !hasTombstone && !hasRotation {
		g.auditLocked("revoke", host, port, "", "host_unknown")
		return ErrHostUnknown
	}

	g.deleteHostEntryLocked(hk)
	delete(g.rotations, hk)
	g.revoked[hk] = revocationState{}
	g.stats.Revoked++
	g.auditLocked("revoke", host, port, "", "revoked")
	return nil
}

// Propagate pushes pins and revocation tombstones downstream.
func (g *HostKeyPinGuard) Propagate(hops int) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	if hops < 1 || hops > g.opts.MaxHops {
		return 0, ErrHopBudget
	}

	count := len(g.pins) + len(g.revoked)
	g.stats.Propagated += count
	return count, nil
}

// Entries returns the pins of a tenant in host, port and address order.
func (g *HostKeyPinGuard) Entries(tenant string) []HostKeyEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	entries := make([]HostKeyEntry, 0)
	for _, entry := range g.pins {
		if entry.Tenant == tenant {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Hostname != entries[j].Hostname {
			return entries[i].Hostname < entries[j].Hostname
		}
		if entries[i].Port != entries[j].Port {
			return entries[i].Port < entries[j].Port
		}
		if entries[i].Address != entries[j].Address {
			return entries[i].Address < entries[j].Address
		}
		return entries[i].Fingerprint < entries[j].Fingerprint
	})
	return entries
}

// Lookup returns the pin of one host address.
func (g *HostKeyPinGuard) Lookup(tenant, host string, port int) (HostKeyEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	hk := hostKey{tenant: tenant, hostname: host, port: port}
	entries := g.hostEntriesLocked(hk)
	if len(entries) == 0 {
		return HostKeyEntry{}, false
	}
	return entries[0], true
}

// AuditLog returns decisions in occurrence order.
func (g *HostKeyPinGuard) AuditLog() []PinAudit {
	g.mu.Lock()
	defer g.mu.Unlock()

	audits := make([]PinAudit, len(g.audits))
	copy(audits, g.audits)
	return audits
}

// Snapshot serialises pins, rotations, tombstones and statistics.
func (g *HostKeyPinGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureLocked()

	type rotationSnapshot struct {
		Tenant         string `json:"tenant"`
		Hostname       string `json:"hostname"`
		Port           int    `json:"port"`
		NewFingerprint string `json:"new_fingerprint"`
		StartedAt      int64  `json:"started_at"`
		Deadline       int64  `json:"deadline"`
		ObservedOld    bool   `json:"observed_old"`
		ObservedNew    bool   `json:"observed_new"`
	}
	type revocationSnapshot struct {
		Tenant    string `json:"tenant"`
		Hostname  string `json:"hostname"`
		Port      int    `json:"port"`
		RevokedAt int64  `json:"revoked_at"`
	}
	type snapshotData struct {
		Version   int                  `json:"version"`
		Options   PinOptions           `json:"options"`
		Pins      []HostKeyEntry       `json:"pins"`
		Rotations []rotationSnapshot   `json:"rotations"`
		Revoked   []revocationSnapshot `json:"revoked"`
		Audits    []PinAudit           `json:"audits"`
		Stats     PinStats             `json:"stats"`
	}

	data := snapshotData{
		Version: 1,
		Options: g.opts,
		Pins:    make([]HostKeyEntry, 0, len(g.pins)),
		Audits:  make([]PinAudit, len(g.audits)),
		Stats:   g.stats,
	}
	copy(data.Audits, g.audits)

	for _, entry := range g.pins {
		data.Pins = append(data.Pins, entry)
	}
	sort.Slice(data.Pins, func(i, j int) bool {
		a, b := data.Pins[i], data.Pins[j]
		if a.Tenant != b.Tenant {
			return a.Tenant < b.Tenant
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.Address < b.Address
	})

	for hk, rotation := range g.rotations {
		data.Rotations = append(data.Rotations, rotationSnapshot{
			Tenant:         hk.tenant,
			Hostname:       hk.hostname,
			Port:           hk.port,
			NewFingerprint: rotation.newFingerprint,
			StartedAt:      rotation.startedAt,
			Deadline:       rotation.deadline,
			ObservedOld:    rotation.observedOld,
			ObservedNew:    rotation.observedNew,
		})
	}
	sort.Slice(data.Rotations, func(i, j int) bool {
		a, b := data.Rotations[i], data.Rotations[j]
		if a.Tenant != b.Tenant {
			return a.Tenant < b.Tenant
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.Port < b.Port
	})

	for hk, revocation := range g.revoked {
		data.Revoked = append(data.Revoked, revocationSnapshot{
			Tenant:    hk.tenant,
			Hostname:  hk.hostname,
			Port:      hk.port,
			RevokedAt: revocation.revokedAt,
		})
	}
	sort.Slice(data.Revoked, func(i, j int) bool {
		a, b := data.Revoked[i], data.Revoked[j]
		if a.Tenant != b.Tenant {
			return a.Tenant < b.Tenant
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.Port < b.Port
	})

	blob, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPinSnapshot, err)
	}
	return blob, nil
}

// Restore rebuilds the guard from a snapshot.
func (g *HostKeyPinGuard) Restore(data []byte) error {
	type rotationSnapshot struct {
		Tenant         string `json:"tenant"`
		Hostname       string `json:"hostname"`
		Port           int    `json:"port"`
		NewFingerprint string `json:"new_fingerprint"`
		StartedAt      int64  `json:"started_at"`
		Deadline       int64  `json:"deadline"`
		ObservedOld    bool   `json:"observed_old"`
		ObservedNew    bool   `json:"observed_new"`
	}
	type revocationSnapshot struct {
		Tenant    string `json:"tenant"`
		Hostname  string `json:"hostname"`
		Port      int    `json:"port"`
		RevokedAt int64  `json:"revoked_at"`
	}
	type snapshotData struct {
		Version   int                  `json:"version"`
		Options   PinOptions           `json:"options"`
		Pins      []HostKeyEntry       `json:"pins"`
		Rotations []rotationSnapshot   `json:"rotations"`
		Revoked   []revocationSnapshot `json:"revoked"`
		Audits    []PinAudit           `json:"audits"`
		Stats     PinStats             `json:"stats"`
	}

	var decoded snapshotData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ErrPinSnapshot
	}
	if decoded.Version != 1 {
		return ErrPinSnapshot
	}
	if decoded.Stats.Pinned < 0 ||
		decoded.Stats.Verified < 0 ||
		decoded.Stats.Denied < 0 ||
		decoded.Stats.Revoked < 0 ||
		decoded.Stats.Propagated < 0 ||
		decoded.Stats.Rotations < 0 {
		return ErrPinSnapshot
	}

	pins := make(map[addressKey]HostKeyEntry, len(decoded.Pins))
	for _, entry := range decoded.Pins {
		if entry.Tenant == "" || entry.Hostname == "" || entry.Port <= 0 ||
			entry.Address == "" || entry.Fingerprint == "" {
			return ErrPinSnapshot
		}
		if entry.Status == "" {
			entry.Status = "pinned"
		}
		key := addressKey{
			hostKey: hostKey{
				tenant:   entry.Tenant,
				hostname: entry.Hostname,
				port:     entry.Port,
			},
			address: entry.Address,
		}
		if _, ok := pins[key]; ok {
			return ErrPinSnapshot
		}
		pins[key] = entry
	}

	rotations := make(map[hostKey]*rotationState, len(decoded.Rotations))
	for _, rotation := range decoded.Rotations {
		if rotation.Tenant == "" || rotation.Hostname == "" || rotation.Port <= 0 ||
			rotation.NewFingerprint == "" {
			return ErrPinSnapshot
		}
		key := hostKey{
			tenant:   rotation.Tenant,
			hostname: rotation.Hostname,
			port:     rotation.Port,
		}
		if _, ok := rotations[key]; ok {
			return ErrPinSnapshot
		}
		rotations[key] = &rotationState{
			newFingerprint: rotation.NewFingerprint,
			startedAt:      rotation.StartedAt,
			deadline:       rotation.Deadline,
			observedOld:    rotation.ObservedOld,
			observedNew:    rotation.ObservedNew,
		}
	}

	revoked := make(map[hostKey]revocationState, len(decoded.Revoked))
	for _, revocation := range decoded.Revoked {
		if revocation.Tenant == "" || revocation.Hostname == "" || revocation.Port <= 0 {
			return ErrPinSnapshot
		}
		key := hostKey{
			tenant:   revocation.Tenant,
			hostname: revocation.Hostname,
			port:     revocation.Port,
		}
		if _, ok := revoked[key]; ok {
			return ErrPinSnapshot
		}
		revoked[key] = revocationState{revokedAt: revocation.RevokedAt}
	}

	audits := make([]PinAudit, len(decoded.Audits))
	copy(audits, decoded.Audits)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.opts = decoded.Options
	g.pins = pins
	g.rotations = rotations
	g.revoked = revoked
	g.audits = audits
	g.stats = decoded.Stats
	return nil
}

// Stats returns the activity counters.
func (g *HostKeyPinGuard) Stats() PinStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}
