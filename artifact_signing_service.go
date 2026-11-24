package task

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

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

type partitionState struct {
	generation       int
	keyID            string
	algorithm        string
	quota            int
	used             int
	revoking         bool
	revokeGeneration int
	receipts         int
}

// SigningService signs artifacts under environment-scoped key partitions.
type SigningService struct {
	mu sync.Mutex

	// cond is a barrier: re-sign requests wait while a targeted partition is
	// between BeginRevocation and SettleRevocation.
	cond *sync.Cond

	partitions     map[Environment]*partitionState
	partitionOrder []Environment
	receipts       map[string]Receipt
	receiptOrder   []string
	resignIndex    map[string]string
	ledger         []LedgerEntry
	nextID         int
	quotaRejected  int
}

// NewSigningService builds an empty signing service.
func NewSigningService() *SigningService {
	s := &SigningService{
		partitions:  make(map[Environment]*partitionState),
		receipts:    make(map[string]Receipt),
		resignIndex: make(map[string]string),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *SigningService) initLocked() {
	if s.partitions == nil {
		s.partitions = make(map[Environment]*partitionState)
	}
	if s.receipts == nil {
		s.receipts = make(map[string]Receipt)
	}
	if s.resignIndex == nil {
		s.resignIndex = make(map[string]string)
	}
	if s.cond == nil {
		s.cond = sync.NewCond(&s.mu)
	}
}

func validEnvironment(env Environment) bool {
	switch env {
	case EnvProd, EnvStaging, EnvDev:
		return true
	default:
		return false
	}
}

func validDigest(digest string) bool {
	const prefix = "sha256:"
	if len(digest) != len(prefix)+64 || !strings.HasPrefix(digest, prefix) {
		return false
	}
	for i := len(prefix); i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validLedgerKind(kind string) bool {
	switch kind {
	case "key_registered", "signed", "revocation_begun", "revocation_settled", "resigned":
		return true
	default:
		return false
	}
}

func (s *SigningService) appendLedgerLocked(kind, signID string, env Environment, generation int) {
	s.ledger = append(s.ledger, LedgerEntry{
		Seq:         len(s.ledger) + 1,
		Kind:        kind,
		SignID:      signID,
		Environment: env,
		Generation:  generation,
	})
}

func (s *SigningService) newSignIDLocked() string {
	for {
		s.nextID++
		id := fmt.Sprintf("sig-%d", s.nextID)
		if _, exists := s.receipts[id]; !exists {
			return id
		}
	}
}

func (s *SigningService) issueReceiptLocked(p *partitionState, artifact, artifactDigest, provenanceDigest string, env Environment, resignOf string) Receipt {
	rec := Receipt{
		SignID:           s.newSignIDLocked(),
		Artifact:         artifact,
		ArtifactDigest:   artifactDigest,
		ProvenanceDigest: provenanceDigest,
		Environment:      env,
		Generation:       p.generation,
		Algorithm:        p.algorithm,
		Revoked:          false,
		ResignOf:         resignOf,
	}
	s.receipts[rec.SignID] = rec
	s.receiptOrder = append(s.receiptOrder, rec.SignID)
	p.used++
	p.receipts++
	return rec
}

// RegisterKey installs the next key generation of one partition.
func (s *SigningService) RegisterKey(spec KeySpec) error {
	if !validEnvironment(spec.Partition) {
		return ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[spec.Partition]
	current := 0
	if p != nil {
		current = p.generation
	}
	if spec.Generation < 1 || spec.Generation != current+1 {
		return ErrStaleGeneration
	}
	if spec.KeyID == "" || spec.Algorithm == "" || spec.Quota < 0 {
		return ErrInvalidKey
	}
	if p != nil && spec.Quota < p.used {
		return ErrQuotaExhausted
	}

	if p == nil {
		p = &partitionState{}
		s.partitions[spec.Partition] = p
		s.partitionOrder = append(s.partitionOrder, spec.Partition)
	}
	p.generation = spec.Generation
	p.keyID = spec.KeyID
	p.algorithm = spec.Algorithm
	p.quota = spec.Quota

	s.appendLedgerLocked("key_registered", spec.KeyID, spec.Partition, spec.Generation)
	return nil
}

// Sign validates the provenance binding and issues a receipt.
func (s *SigningService) Sign(req SignRequest) (Receipt, error) {
	if !validEnvironment(req.Environment) {
		return Receipt{}, ErrInvalidEnvironment
	}
	if req.Artifact == "" {
		return Receipt{}, ErrInvalidArtifact
	}
	if !validDigest(req.ArtifactDigest) || !validDigest(req.Provenance.ProvenanceDigest) {
		return Receipt{}, ErrInvalidDigest
	}
	if req.Provenance.ArtifactDigest != req.ArtifactDigest {
		return Receipt{}, ErrProvenanceMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[req.Environment]
	if p == nil {
		return Receipt{}, ErrUnknownPartition
	}
	if p.revoking && p.revokeGeneration == p.generation {
		return Receipt{}, ErrRevocationPending
	}
	if p.used >= p.quota {
		s.quotaRejected++
		return Receipt{}, ErrQuotaExhausted
	}

	rec := s.issueReceiptLocked(p, req.Artifact, req.ArtifactDigest, req.Provenance.ProvenanceDigest, req.Environment, "")
	s.appendLedgerLocked("signed", rec.SignID, rec.Environment, rec.Generation)
	return rec, nil
}

// BeginRevocation starts revoking every receipt of one partition generation.
func (s *SigningService) BeginRevocation(partition Environment, generation int) (RevokeReport, error) {
	if !validEnvironment(partition) {
		return RevokeReport{}, ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return RevokeReport{}, ErrUnknownPartition
	}
	if generation < 1 || generation > p.generation {
		return RevokeReport{}, ErrStaleGeneration
	}
	if p.revoking {
		return RevokeReport{}, ErrRevocationPending
	}

	pending := 0
	for _, id := range s.receiptOrder {
		rec := s.receipts[id]
		if rec.Environment == partition && rec.Generation == generation && !rec.Revoked {
			pending++
		}
	}

	p.revoking = true
	p.revokeGeneration = generation
	s.appendLedgerLocked("revocation_begun", "", partition, generation)

	return RevokeReport{
		Partition:  partition,
		Generation: generation,
		Pending:    pending,
		Revoked:    0,
		Settled:    false,
	}, nil
}

// SettleRevocation finishes the revocation and marks the receipts revoked.
func (s *SigningService) SettleRevocation(partition Environment) (RevokeReport, error) {
	if !validEnvironment(partition) {
		return RevokeReport{}, ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return RevokeReport{}, ErrUnknownPartition
	}
	if !p.revoking {
		return RevokeReport{}, ErrInvalidRevocation
	}

	generation := p.revokeGeneration
	revoked := 0
	for _, id := range s.receiptOrder {
		rec := s.receipts[id]
		if rec.Environment == partition && rec.Generation == generation && !rec.Revoked {
			rec.Revoked = true
			s.receipts[id] = rec
			revoked++
		}
	}

	p.revoking = false
	p.revokeGeneration = 0
	s.appendLedgerLocked("revocation_settled", "", partition, generation)
	s.cond.Broadcast()

	return RevokeReport{
		Partition:  partition,
		Generation: generation,
		Pending:    0,
		Revoked:    revoked,
		Settled:    true,
	}, nil
}

// ResignBatch re-signs revoked receipts, idempotent per original sign id.
func (s *SigningService) ResignBatch(originalIDs []string) (ResignReport, error) {
	report := ResignReport{Requested: len(originalIDs)}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	targets := make(map[Environment]bool)
	for _, id := range originalIDs {
		if rec, ok := s.receipts[id]; ok {
			targets[rec.Environment] = true
		}
	}
	for {
		waiting := false
		for env := range targets {
			if p := s.partitions[env]; p != nil && p.revoking {
				waiting = true
				break
			}
		}
		if !waiting {
			break
		}
		s.cond.Wait()
	}

	for _, id := range originalIDs {
		rec, ok := s.receipts[id]
		if !ok {
			report.Failed++
			continue
		}
		if _, already := s.resignIndex[id]; already {
			report.AlreadyResigned++
			continue
		}
		if !rec.Revoked {
			report.Skipped++
			continue
		}

		p := s.partitions[rec.Environment]
		if p == nil {
			report.Failed++
			continue
		}
		if p.revoking {
			report.Failed++
			continue
		}
		if p.used >= p.quota {
			s.quotaRejected++
			report.Failed++
			continue
		}

		replacement := s.issueReceiptLocked(p, rec.Artifact, rec.ArtifactDigest, rec.ProvenanceDigest, rec.Environment, rec.SignID)
		s.resignIndex[id] = replacement.SignID
		s.appendLedgerLocked("resigned", replacement.SignID, replacement.Environment, replacement.Generation)
		report.Resigned++
		report.NewSignIDs = append(report.NewSignIDs, replacement.SignID)
	}

	return report, nil
}

// QueryReceipt returns one receipt by sign id.
func (s *SigningService) QueryReceipt(signID string) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	rec, ok := s.receipts[signID]
	if !ok {
		return Receipt{}, ErrReceiptNotFound
	}
	return rec, nil
}

// SetQuota changes the signing quota of one partition.
func (s *SigningService) SetQuota(partition Environment, quota int) error {
	if !validEnvironment(partition) {
		return ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return ErrUnknownPartition
	}
	if quota < 0 {
		return ErrInvalidKey
	}
	if quota < p.used {
		return ErrQuotaExhausted
	}
	p.quota = quota
	return nil
}

// QuotaRemaining reports the unused signing quota of one partition.
func (s *SigningService) QuotaRemaining(partition Environment) (int, error) {
	if !validEnvironment(partition) {
		return 0, ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return 0, ErrUnknownPartition
	}
	remaining := p.quota - p.used
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// PartitionStatus reports one key partition.
func (s *SigningService) PartitionStatus(partition Environment) (PartitionStatus, error) {
	if !validEnvironment(partition) {
		return PartitionStatus{}, ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return PartitionStatus{}, ErrUnknownPartition
	}
	remaining := p.quota - p.used
	if remaining < 0 {
		remaining = 0
	}
	return PartitionStatus{
		Partition:  partition,
		Generation: p.generation,
		Algorithm:  p.algorithm,
		Quota:      p.quota,
		Used:       p.used,
		Remaining:  remaining,
		Revoking:   p.revoking,
		Receipts:   p.receipts,
	}, nil
}

// Generation reports the current key generation of one partition.
func (s *SigningService) Generation(partition Environment) (int, error) {
	if !validEnvironment(partition) {
		return 0, ErrInvalidEnvironment
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	p := s.partitions[partition]
	if p == nil {
		return 0, ErrUnknownPartition
	}
	return p.generation, nil
}

// Ledger returns the migration ledger in occurrence order.
func (s *SigningService) Ledger() []LedgerEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	if len(s.ledger) == 0 {
		return nil
	}
	out := make([]LedgerEntry, len(s.ledger))
	copy(out, s.ledger)
	return out
}

// Snapshot captures partitions, receipts, ledger and counters.
func (s *SigningService) Snapshot() (SigningSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	snap := SigningSnapshot{
		Partitions: make([]PartitionSnapshot, 0, len(s.partitionOrder)),
		Receipts:   make([]Receipt, 0, len(s.receiptOrder)),
		Ledger:     make([]LedgerEntry, len(s.ledger)),
	}
	for _, env := range s.partitionOrder {
		p := s.partitions[env]
		if p == nil {
			continue
		}
		snap.Partitions = append(snap.Partitions, PartitionSnapshot{
			Partition:        env,
			Generation:       p.generation,
			KeyID:            p.keyID,
			Algorithm:        p.algorithm,
			Quota:            p.quota,
			Used:             p.used,
			Revoking:         p.revoking,
			RevokeGeneration: p.revokeGeneration,
		})
	}
	for _, id := range s.receiptOrder {
		if rec, ok := s.receipts[id]; ok {
			snap.Receipts = append(snap.Receipts, rec)
		}
	}
	copy(snap.Ledger, s.ledger)
	snap.Counters = s.statsLocked()
	return snap, nil
}

// Restore replaces the service contents with a snapshot.
func (s *SigningService) Restore(snap SigningSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	if len(snap.Partitions) == 0 {
		return fmt.Errorf("%w: no key partitions", ErrBadSnapshot)
	}
	if snap.Counters.QuotaRejected < 0 {
		return fmt.Errorf("%w: negative quota rejection count", ErrBadSnapshot)
	}

	partitions := make(map[Environment]*partitionState, len(snap.Partitions))
	partitionOrder := make([]Environment, 0, len(snap.Partitions))
	for _, ps := range snap.Partitions {
		if !validEnvironment(ps.Partition) {
			return fmt.Errorf("%w: invalid partition %q", ErrBadSnapshot, ps.Partition)
		}
		if _, exists := partitions[ps.Partition]; exists {
			return fmt.Errorf("%w: duplicate partition %q", ErrBadSnapshot, ps.Partition)
		}
		if ps.Generation < 1 || ps.KeyID == "" || ps.Algorithm == "" {
			return fmt.Errorf("%w: invalid partition generation or key", ErrBadSnapshot)
		}
		if ps.Quota < 0 || ps.Used < 0 || ps.Used > ps.Quota {
			return fmt.Errorf("%w: invalid quota for %q", ErrBadSnapshot, ps.Partition)
		}
		if ps.Revoking && (ps.RevokeGeneration < 1 || ps.RevokeGeneration > ps.Generation) {
			return fmt.Errorf("%w: invalid revocation generation for %q", ErrBadSnapshot, ps.Partition)
		}
		partitions[ps.Partition] = &partitionState{
			generation:       ps.Generation,
			keyID:            ps.KeyID,
			algorithm:        ps.Algorithm,
			quota:            ps.Quota,
			used:             ps.Used,
			revoking:         ps.Revoking,
			revokeGeneration: ps.RevokeGeneration,
		}
		partitionOrder = append(partitionOrder, ps.Partition)
	}

	receipts := make(map[string]Receipt, len(snap.Receipts))
	receiptOrder := make([]string, 0, len(snap.Receipts))
	for _, rec := range snap.Receipts {
		if rec.SignID == "" {
			return fmt.Errorf("%w: empty receipt id", ErrBadSnapshot)
		}
		if _, exists := receipts[rec.SignID]; exists {
			return fmt.Errorf("%w: duplicate receipt %q", ErrBadSnapshot, rec.SignID)
		}
		if !validEnvironment(rec.Environment) {
			return fmt.Errorf("%w: invalid receipt environment", ErrBadSnapshot)
		}
		p := partitions[rec.Environment]
		if p == nil {
			return fmt.Errorf("%w: receipt partition %q is missing", ErrBadSnapshot, rec.Environment)
		}
		if rec.Generation < 1 || rec.Generation > p.generation {
			return fmt.Errorf("%w: invalid receipt generation", ErrBadSnapshot)
		}
		if rec.Algorithm == "" || !validDigest(rec.ArtifactDigest) || !validDigest(rec.ProvenanceDigest) {
			return fmt.Errorf("%w: invalid receipt payload", ErrBadSnapshot)
		}
		receipts[rec.SignID] = rec
		receiptOrder = append(receiptOrder, rec.SignID)
	}
	for _, rec := range snap.Receipts {
		if rec.ResignOf != "" {
			if _, ok := receipts[rec.ResignOf]; !ok {
				return fmt.Errorf("%w: missing original receipt %q", ErrBadSnapshot, rec.ResignOf)
			}
		}
	}

	ledger := make([]LedgerEntry, len(snap.Ledger))
	for i, entry := range snap.Ledger {
		if entry.Seq != i+1 {
			return fmt.Errorf("%w: non-contiguous ledger sequence", ErrBadSnapshot)
		}
		if !validLedgerKind(entry.Kind) || !validEnvironment(entry.Environment) || entry.Generation < 1 {
			return fmt.Errorf("%w: invalid ledger entry", ErrBadSnapshot)
		}
		ledger[i] = entry
	}

	for _, rec := range receipts {
		partitions[rec.Environment].receipts++
	}

	nextID := len(receipts)
	for id := range receipts {
		var n int
		if _, err := fmt.Sscanf(id, "sig-%d", &n); err == nil && n > nextID {
			nextID = n
		}
	}
	resignIndex := make(map[string]string)
	for _, id := range receiptOrder {
		rec := receipts[id]
		if rec.ResignOf == "" {
			continue
		}
		if _, exists := resignIndex[rec.ResignOf]; !exists {
			resignIndex[rec.ResignOf] = rec.SignID
		}
	}

	s.partitions = partitions
	s.partitionOrder = partitionOrder
	s.receipts = receipts
	s.receiptOrder = receiptOrder
	s.resignIndex = resignIndex
	s.ledger = ledger
	s.nextID = nextID
	s.quotaRejected = snap.Counters.QuotaRejected
	s.cond.Broadcast()
	return nil
}

func (s *SigningService) statsLocked() SigningStats {
	stats := SigningStats{
		Partitions:    len(s.partitions),
		Receipts:      len(s.receipts),
		QuotaRejected: s.quotaRejected,
		LedgerEntries: len(s.ledger),
	}
	for _, rec := range s.receipts {
		if rec.Revoked {
			stats.Revoked++
		}
		if rec.ResignOf == "" {
			stats.Signed++
		} else {
			stats.Resigned++
		}
	}
	return stats
}

// Stats aggregates service counters.
func (s *SigningService) Stats() SigningStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	return s.statsLocked()
}
