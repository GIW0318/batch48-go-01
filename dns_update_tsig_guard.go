package task

import "errors"

// errNotImplemented marks behaviour that still has to be filled in.
var errNotImplemented = errors.New("not implemented")

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

// TsigAuthzGuard validates TSIG signed DNS dynamic updates.
type TsigAuthzGuard struct {
	opts TsigOptions
}

// NewTsigAuthzGuard builds a guard with the supplied limits.
func NewTsigAuthzGuard(opts TsigOptions) *TsigAuthzGuard {
	return &TsigAuthzGuard{opts: opts}
}

// BindKey registers a TSIG key.
func (g *TsigAuthzGuard) BindKey(spec TsigKeySpec) error {
	return errNotImplemented
}

// RevokeKey revokes a bound key immediately.
func (g *TsigAuthzGuard) RevokeKey(name string) error {
	return errNotImplemented
}

// RotateKey advances the key generation and switches the algorithm.
func (g *TsigAuthzGuard) RotateKey(name, algorithm string) (TsigKeySpec, error) {
	return TsigKeySpec{}, errNotImplemented
}

// SetZoneGrant replaces the authorisation of one zone.
func (g *TsigAuthzGuard) SetZoneGrant(grant ZoneGrant) error {
	return errNotImplemented
}

// Verify checks key selection, time window, digest and zone ACL.
func (g *TsigAuthzGuard) Verify(update DnsUpdate, now int64) (VerifiedUpdate, error) {
	return VerifiedUpdate{}, errNotImplemented
}

// ApplyUpdate commits one verified update and returns the new SOA serial.
func (g *TsigAuthzGuard) ApplyUpdate(v VerifiedUpdate) (int64, error) {
	return 0, errNotImplemented
}

// Serial returns the current SOA serial of a zone.
func (g *TsigAuthzGuard) Serial(zone string) int64 {
	return 0
}

// Pending returns the number of requests accepted but not yet applied.
func (g *TsigAuthzGuard) Pending() int {
	return 0
}

// KeyFailures returns the failure counter of one key.
func (g *TsigAuthzGuard) KeyFailures(name string) int {
	return 0
}

// Snapshot serialises keys, grants, serials and the dedup window.
func (g *TsigAuthzGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard from a snapshot.
func (g *TsigAuthzGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *TsigAuthzGuard) Stats() TsigStats {
	return TsigStats{}
}
