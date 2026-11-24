package task

import "errors"

// errNotImplemented is returned by behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the signing service.
var (
	ErrInvalidEnvironment = errors.New("artifact_signing_service: invalid environment")
	ErrInvalidDigest      = errors.New("artifact_signing_service: invalid digest")
	ErrInvalidArtifact    = errors.New("artifact_signing_service: invalid artifact")
	ErrProvenanceMismatch = errors.New("artifact_signing_service: provenance does not bind the artifact")
	ErrUnknownPartition   = errors.New("artifact_signing_service: unknown key partition")
	ErrStaleGeneration    = errors.New("artifact_signing_service: stale key generation")
	ErrInvalidKey         = errors.New("artifact_signing_service: invalid key registration")
	ErrQuotaExhausted     = errors.New("artifact_signing_service: signing quota exhausted")
	ErrReceiptNotFound    = errors.New("artifact_signing_service: receipt not found")
	ErrRevocationPending  = errors.New("artifact_signing_service: revocation still pending")
	ErrInvalidRevocation  = errors.New("artifact_signing_service: no revocation in progress")
	ErrBadSnapshot        = errors.New("artifact_signing_service: bad snapshot")
)

// Environment selects the key partition used for signing.
type Environment string

// Supported signing environments.
const (
	EnvProd    Environment = "prod"
	EnvStaging Environment = "staging"
	EnvDev     Environment = "dev"
)

// Provenance is the build attestation bound to an artifact digest.
type Provenance struct {
	ProvenanceDigest string
	ArtifactDigest   string
	Builder          string
}

// SignRequest is one artifact signing request.
type SignRequest struct {
	Artifact       string
	ArtifactDigest string
	Environment    Environment
	Provenance     Provenance
}

// Receipt is the signed record of one artifact.
type Receipt struct {
	SignID           string
	Artifact         string
	ArtifactDigest   string
	ProvenanceDigest string
	Environment      Environment
	Generation       int
	Algorithm        string
	Revoked          bool
	ResignOf         string
}

// KeySpec registers one key generation of a partition.
type KeySpec struct {
	Partition  Environment
	Generation int
	KeyID      string
	Algorithm  string
	Quota      int
}

// RevokeReport describes one revocation step.
type RevokeReport struct {
	Partition  Environment
	Generation int
	Pending    int
	Revoked    int
	Settled    bool
}

// ResignReport describes one batch re-sign.
type ResignReport struct {
	Requested       int
	Resigned        int
	Skipped         int
	AlreadyResigned int
	Failed          int
	NewSignIDs      []string
}

// LedgerEntry is one append-only migration record.
type LedgerEntry struct {
	Seq         int
	Kind        string
	SignID      string
	Environment Environment
	Generation  int
}

// PartitionStatus is the observable state of one key partition.
type PartitionStatus struct {
	Partition  Environment
	Generation int
	Algorithm  string
	Quota      int
	Used       int
	Remaining  int
	Revoking   bool
	Receipts   int
}

// PartitionSnapshot is the portable form of one key partition.
type PartitionSnapshot struct {
	Partition        Environment
	Generation       int
	KeyID            string
	Algorithm        string
	Quota            int
	Used             int
	Revoking         bool
	RevokeGeneration int
}

// SigningStats aggregates service counters.
type SigningStats struct {
	Partitions    int
	Receipts      int
	Signed        int
	Revoked       int
	Resigned      int
	QuotaRejected int
	LedgerEntries int
}

// SigningSnapshot is the portable form of the whole service.
type SigningSnapshot struct {
	Partitions []PartitionSnapshot
	Receipts   []Receipt
	Ledger     []LedgerEntry
	Counters   SigningStats
}

// SigningService signs artifacts under environment-scoped key partitions.
type SigningService struct {
	implicit int
}

// NewSigningService builds an empty signing service.
func NewSigningService() *SigningService {
	return &SigningService{}
}

// RegisterKey installs the next key generation of one partition.
func (s *SigningService) RegisterKey(spec KeySpec) error {
	return errNotImplemented
}

// Sign validates the provenance binding and issues a receipt.
func (s *SigningService) Sign(req SignRequest) (Receipt, error) {
	return Receipt{}, errNotImplemented
}

// BeginRevocation starts revoking every receipt of one partition generation.
func (s *SigningService) BeginRevocation(partition Environment, generation int) (RevokeReport, error) {
	return RevokeReport{}, errNotImplemented
}

// SettleRevocation finishes the revocation and marks the receipts revoked.
func (s *SigningService) SettleRevocation(partition Environment) (RevokeReport, error) {
	return RevokeReport{}, errNotImplemented
}

// ResignBatch re-signs revoked receipts, idempotent per original sign id.
func (s *SigningService) ResignBatch(originalIDs []string) (ResignReport, error) {
	return ResignReport{}, errNotImplemented
}

// QueryReceipt returns one receipt by sign id.
func (s *SigningService) QueryReceipt(signID string) (Receipt, error) {
	return Receipt{}, errNotImplemented
}

// SetQuota changes the signing quota of one partition.
func (s *SigningService) SetQuota(partition Environment, quota int) error {
	return errNotImplemented
}

// QuotaRemaining reports the unused signing quota of one partition.
func (s *SigningService) QuotaRemaining(partition Environment) (int, error) {
	return 0, errNotImplemented
}

// PartitionStatus reports one key partition.
func (s *SigningService) PartitionStatus(partition Environment) (PartitionStatus, error) {
	return PartitionStatus{}, errNotImplemented
}

// Generation reports the current key generation of one partition.
func (s *SigningService) Generation(partition Environment) (int, error) {
	return 0, errNotImplemented
}

// Ledger returns the migration ledger in occurrence order.
func (s *SigningService) Ledger() []LedgerEntry {
	return nil
}

// Snapshot captures partitions, receipts, ledger and counters.
func (s *SigningService) Snapshot() (SigningSnapshot, error) {
	return SigningSnapshot{}, errNotImplemented
}

// Restore replaces the service contents with a snapshot.
func (s *SigningService) Restore(snap SigningSnapshot) error {
	return errNotImplemented
}

// Stats aggregates service counters.
func (s *SigningService) Stats() SigningStats {
	return SigningStats{}
}
