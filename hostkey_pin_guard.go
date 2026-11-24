package task

import "errors"

// errNotImplemented marks behaviour that still has to be filled in.
var errNotImplemented = errors.New("not implemented")

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

// HostKeyPinGuard maintains host key pins per tenant, host and port.
type HostKeyPinGuard struct {
	opts PinOptions
}

// NewHostKeyPinGuard builds a guard with the supplied limits.
func NewHostKeyPinGuard(opts PinOptions) *HostKeyPinGuard {
	return &HostKeyPinGuard{opts: opts}
}

// Pin records the explicit first use trust of a host.
func (g *HostKeyPinGuard) Pin(req PinRequest) (PinDecision, error) {
	return PinDecision{}, errNotImplemented
}

// Verify checks a presented host key against the pinned fingerprints.
func (g *HostKeyPinGuard) Verify(req VerifyRequest) (PinDecision, error) {
	return PinDecision{}, errNotImplemented
}

// BeginRotation opens the dual fingerprint overlap window of a host.
func (g *HostKeyPinGuard) BeginRotation(tenant, host string, port int, newFingerprint string, at int64) error {
	return errNotImplemented
}

// CompleteRotation retires the old fingerprint and keeps the new one.
func (g *HostKeyPinGuard) CompleteRotation(tenant, host string, port int, at int64) error {
	return errNotImplemented
}

// Revoke invalidates every pin of a host and leaves a propagation tombstone.
func (g *HostKeyPinGuard) Revoke(tenant, host string, port int) error {
	return errNotImplemented
}

// Propagate pushes pins and revocation tombstones downstream.
func (g *HostKeyPinGuard) Propagate(hops int) (int, error) {
	return 0, errNotImplemented
}

// Entries returns the pins of a tenant in host, port and address order.
func (g *HostKeyPinGuard) Entries(tenant string) []HostKeyEntry {
	return nil
}

// Lookup returns the pin of one host address.
func (g *HostKeyPinGuard) Lookup(tenant, host string, port int) (HostKeyEntry, bool) {
	return HostKeyEntry{}, false
}

// AuditLog returns decisions in occurrence order.
func (g *HostKeyPinGuard) AuditLog() []PinAudit {
	return nil
}

// Snapshot serialises pins, rotations, tombstones and statistics.
func (g *HostKeyPinGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard from a snapshot.
func (g *HostKeyPinGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *HostKeyPinGuard) Stats() PinStats {
	return PinStats{}
}
