package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by PairwiseSubjectGuard.
var (
	ErrInvalidTenant      = errors.New("invalid tenant id")
	ErrInvalidSector      = errors.New("invalid sector")
	ErrDuplicateSectorURI = errors.New("sector uri already registered")
	ErrSectorLimit        = errors.New("tenant sector limit reached")
	ErrUnknownSector      = errors.New("unknown sector")
	ErrUnknownClient      = errors.New("unknown client")
	ErrInvalidClient      = errors.New("invalid client id")
	ErrAlreadyAssigned    = errors.New("client already assigned to another sector")
	ErrClientLimit        = errors.New("sector client limit reached")
	ErrInvalidUser        = errors.New("invalid local user id")
	ErrUnknownSubject     = errors.New("unknown subject")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// Sector is a registered pairwise sector of one tenant.
type Sector struct {
	ID  string `json:"id"`
	URI string `json:"uri"`
}

// SubjectRemap records one re-derived pairwise subject of a moved client.
type SubjectRemap struct {
	LocalUserID string `json:"local_user_id"`
	OldSubject  string `json:"old_subject"`
	NewSubject  string `json:"new_subject"`
}

// Remap is the outcome of moving one client to another sector.
type Remap struct {
	TenantID   string         `json:"tenant_id"`
	ClientID   string         `json:"client_id"`
	FromSector string         `json:"from_sector"`
	ToSector   string         `json:"to_sector"`
	Items      []SubjectRemap `json:"items"`
}

// Options bounds the guard.
type Options struct {
	MaxSectorsPerTenant int
	MaxClientsPerSector int
}

// PairwiseSubjectGuard issues and tracks pairwise subject identifiers.
type PairwiseSubjectGuard struct {
	mu         sync.Mutex
	maxSectors int
	maxClients int
}

// NewPairwiseSubjectGuard builds an empty guard.
func NewPairwiseSubjectGuard(opts Options) *PairwiseSubjectGuard {
	return &PairwiseSubjectGuard{
		maxSectors: opts.MaxSectorsPerTenant,
		maxClients: opts.MaxClientsPerSector,
	}
}

// RegisterSector registers the sector identifier URI of one tenant.
func (g *PairwiseSubjectGuard) RegisterSector(tenantID, sectorID, uri string) error {
	return errNotImplemented
}

// AssignClient binds a client to a sector of the tenant.
func (g *PairwiseSubjectGuard) AssignClient(tenantID, clientID, sectorID string) (Sector, error) {
	return Sector{}, errNotImplemented
}

// SubjectFor returns the pairwise subject of one local user under a client.
func (g *PairwiseSubjectGuard) SubjectFor(tenantID, clientID, localUserID string) (string, error) {
	return "", errNotImplemented
}

// MoveClient moves one client to another registered sector of the same tenant.
func (g *PairwiseSubjectGuard) MoveClient(tenantID, clientID, newSector string) (Remap, error) {
	return Remap{}, errNotImplemented
}

// Tombstone retires one subject and makes it unusable for new sessions.
func (g *PairwiseSubjectGuard) Tombstone(tenantID, subject string) error {
	return errNotImplemented
}

// Resolve maps a retired subject to the subject that replaced it.
func (g *PairwiseSubjectGuard) Resolve(tenantID, subject string) (string, bool) {
	return "", false
}

// RotateSectorKey rotates the derivation key of one sector.
func (g *PairwiseSubjectGuard) RotateSectorKey(tenantID, sectorID string) error {
	return errNotImplemented
}

// KeyEpoch reports how many times the sector key was rotated.
func (g *PairwiseSubjectGuard) KeyEpoch(tenantID, sectorID string) (int, error) {
	return 0, errNotImplemented
}

// SectorCount reports the registered sectors of a tenant.
func (g *PairwiseSubjectGuard) SectorCount(tenantID string) int {
	return 0
}

// ClientCount reports the clients attached to one sector of a tenant.
func (g *PairwiseSubjectGuard) ClientCount(tenantID, sectorID string) int {
	return 0
}

// TombstoneCount reports how many subjects were retired for a tenant.
func (g *PairwiseSubjectGuard) TombstoneCount(tenantID string) int {
	return 0
}

// Snapshot serialises the whole guard state.
func (g *PairwiseSubjectGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard state from a snapshot.
func (g *PairwiseSubjectGuard) Restore(data []byte) error {
	return errNotImplemented
}
