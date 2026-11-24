package task

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Sentinel errors reported by the key wrap guard.
var (
	ErrInvalidPurpose     = errors.New("envelope_key_wrap_guard: invalid purpose")
	ErrInvalidAlgorithm   = errors.New("envelope_key_wrap_guard: invalid algorithm")
	ErrInvalidDEK         = errors.New("envelope_key_wrap_guard: invalid dek")
	ErrDEKExists          = errors.New("envelope_key_wrap_guard: dek already registered")
	ErrDEKNotFound        = errors.New("envelope_key_wrap_guard: unknown dek")
	ErrWrapQuotaExhausted = errors.New("envelope_key_wrap_guard: dek wrap limit reached")
	ErrKEKQuotaExhausted  = errors.New("envelope_key_wrap_guard: kek quota exhausted")
	ErrEnvelopeUnknown    = errors.New("envelope_key_wrap_guard: unknown envelope")
	ErrPurposeMismatch    = errors.New("envelope_key_wrap_guard: envelope purpose mismatch")
	ErrSubjectMismatch    = errors.New("envelope_key_wrap_guard: envelope subject mismatch")
	ErrAlgorithmMismatch  = errors.New("envelope_key_wrap_guard: envelope algorithm mismatch")
	ErrGenerationRetired  = errors.New("envelope_key_wrap_guard: kek generation retired")
	ErrBreakerOpen        = errors.New("envelope_key_wrap_guard: unwrap breaker open")
	ErrInvalidThreshold   = errors.New("envelope_key_wrap_guard: invalid failure threshold")
	ErrBadSnapshot        = errors.New("envelope_key_wrap_guard: bad snapshot")

	ErrInvalidSubject     = errors.New("envelope_key_wrap_guard: invalid subject")
	ErrInvalidQuota       = errors.New("envelope_key_wrap_guard: invalid kek quota")
	ErrEnvelopeBinding    = errors.New("envelope_key_wrap_guard: envelope binding mismatch")
	ErrEnvelopeIntegrity  = errors.New("envelope_key_wrap_guard: envelope integrity check failed")
	ErrGenerationMismatch = errors.New("envelope_key_wrap_guard: generation mismatch")
	ErrGenerationNotFound = errors.New("envelope_key_wrap_guard: unknown kek generation")
)

const (
	defaultGeneration = 1
	defaultAlgorithm  = AlgorithmAES256GCM
	defaultKEKQuota   = 64
	envelopeIDPrefix  = "env-"
)

// Purpose scopes what a wrapped data key may be used for.
type Purpose string

// Supported envelope purposes.
const (
	PurposeData   Purpose = "data"
	PurposeBackup Purpose = "backup"
	PurposeExport Purpose = "export"
)

// Algorithm identifies the wrapping algorithm of one KEK generation.
type Algorithm string

// Supported wrapping algorithms.
const (
	AlgorithmAES256GCM Algorithm = "aes-256-gcm"
	AlgorithmChaCha20  Algorithm = "chacha20-poly1305"
)

// Envelope is the wrapped form of one data key.
type Envelope struct {
	ID         string
	DEKID      string
	Subject    string
	Purpose    Purpose
	Generation int
	Algorithm  Algorithm
	Wrapped    []byte
}

// DEKStatus is the observable state of one registered data key.
type DEKStatus struct {
	ID         string
	Subject    string
	Purpose    Purpose
	Wraps      int
	MaxWraps   int
	Generation int
	Algorithm  Algorithm
	EnvelopeID string
}

// ResealReport describes one batch re-wrap of a purpose.
type ResealReport struct {
	From     int
	To       int
	Resealed int
	Skipped  int
}

// DEKSnapshot is the portable form of one registered data key.
type DEKSnapshot struct {
	ID         string
	Subject    string
	Purpose    Purpose
	Key        []byte
	Wraps      int
	MaxWraps   int
	Generation int
	Algorithm  Algorithm
	EnvelopeID string
}

// KeyWrapSnapshot is the portable form of a whole guard.
type KeyWrapSnapshot struct {
	Purpose          Purpose
	Generation       int
	Algorithm        Algorithm
	Quota            int
	QuotaUsed        int
	FailureThreshold int
	Failures         int
	DEKs             []DEKSnapshot
	Envelopes        []Envelope
	Retired          []int
}

// KeyWrapStats aggregates guard counters.
type KeyWrapStats struct {
	DEKs      int
	Envelopes int
	Wraps     int
	Unwraps   int
	Failures  int
	Resealed  int
}

type kekGeneration struct {
	generation int
	algorithm  Algorithm
	quota      int
	quotaUsed  int
	retired    bool
}

type dekRecord struct {
	id          string
	key         []byte
	subject     string
	purpose     Purpose
	wraps       int
	maxWraps    int
	generation  int
	algorithm   Algorithm
	envelopeID  string
	envelopeIDs []string
}

// KeyWrapGuard wraps data keys under a versioned KEK ring for one purpose.
type KeyWrapGuard struct {
	mu sync.Mutex

	purpose       Purpose
	currentGen    int
	nextEnvSeq    int
	generations   map[int]*kekGeneration
	deks          map[string]*dekRecord
	envelopes     map[string]Envelope
	failThreshold int
	failures      int
	breakerOpen   bool
	stats         KeyWrapStats
}

// NewKeyWrapGuard builds a guard scoped to a single purpose.
func NewKeyWrapGuard(purpose Purpose) *KeyWrapGuard {
	g := &KeyWrapGuard{
		purpose:     purpose,
		currentGen:  defaultGeneration,
		nextEnvSeq:  1,
		generations: make(map[int]*kekGeneration),
		deks:        make(map[string]*dekRecord),
		envelopes:   make(map[string]Envelope),
	}
	g.generations[defaultGeneration] = &kekGeneration{
		generation: defaultGeneration,
		algorithm:  defaultAlgorithm,
		quota:      defaultKEKQuota,
	}
	return g
}

// RegisterDEK registers one data key with a wrap ceiling.
func (g *KeyWrapGuard) RegisterDEK(id string, key []byte, maxWraps int) error {
	if g == nil {
		return ErrDEKNotFound
	}
	if !validPurpose(g.purpose) {
		return fmt.Errorf("%w: %q", ErrInvalidPurpose, g.purpose)
	}
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidDEK)
	}
	if len(key) < 8 || maxWraps <= 0 {
		return ErrInvalidDEK
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.deks[id]; exists {
		return ErrDEKExists
	}
	g.deks[id] = &dekRecord{
		id:       id,
		key:      cloneBytes(key),
		purpose:  g.purpose,
		maxWraps: maxWraps,
	}
	return nil
}

// Wrap produces an envelope bound to purpose, subject and generation.
func (g *KeyWrapGuard) Wrap(dekID string, subject string) (Envelope, error) {
	if g == nil {
		return Envelope{}, ErrDEKNotFound
	}
	if !validPurpose(g.purpose) {
		return Envelope{}, fmt.Errorf("%w: %q", ErrInvalidPurpose, g.purpose)
	}
	if strings.TrimSpace(subject) == "" {
		return Envelope{}, errors.Join(ErrInvalidSubject, ErrSubjectMismatch)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	dek, ok := g.deks[dekID]
	if !ok {
		return Envelope{}, ErrDEKNotFound
	}
	if dek.purpose != g.purpose {
		return Envelope{}, ErrPurposeMismatch
	}
	if dek.wraps >= dek.maxWraps {
		return Envelope{}, ErrWrapQuotaExhausted
	}

	kek, ok := g.generations[g.currentGen]
	if !ok || kek.generation != g.currentGen {
		return Envelope{}, fmt.Errorf("%w: active generation", ErrBadSnapshot)
	}
	if kek.retired {
		return Envelope{}, ErrGenerationRetired
	}
	if kek.quotaUsed >= kek.quota {
		return Envelope{}, ErrKEKQuotaExhausted
	}

	envelopeID := fmt.Sprintf("%s%d", envelopeIDPrefix, g.nextEnvSeq)
	binding := Envelope{
		ID:         envelopeID,
		DEKID:      dekID,
		Subject:    subject,
		Purpose:    g.purpose,
		Generation: kek.generation,
		Algorithm:  kek.algorithm,
	}
	wrapped, err := sealEnvelope(binding, dek.key)
	if err != nil {
		return Envelope{}, err
	}
	binding.Wrapped = wrapped

	stored := cloneEnvelope(binding)
	g.envelopes[envelopeID] = stored
	g.nextEnvSeq++
	kek.quotaUsed++
	dek.wraps++
	dek.subject = subject
	dek.generation = kek.generation
	dek.algorithm = kek.algorithm
	dek.envelopeID = envelopeID
	dek.envelopeIDs = append(dek.envelopeIDs, envelopeID)
	g.stats.Wraps++

	return cloneEnvelope(binding), nil
}

// Unwrap verifies every binding of an envelope before returning the data key.
func (g *KeyWrapGuard) Unwrap(env Envelope) ([]byte, error) {
	if g == nil {
		return nil, ErrEnvelopeUnknown
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.breakerOpen {
		return nil, ErrBreakerOpen
	}

	if !validPurpose(env.Purpose) || env.Purpose != g.purpose {
		return g.unwrapFailureLocked(fmt.Errorf("%w: guard %q envelope %q",
			ErrPurposeMismatch, g.purpose, env.Purpose))
	}

	record, ok := g.envelopes[env.ID]
	if !ok {
		return g.unwrapFailureLocked(ErrEnvelopeUnknown)
	}
	if env.DEKID != record.DEKID {
		return g.unwrapFailureLocked(fmt.Errorf("%w: dek id", ErrEnvelopeBinding))
	}
	if env.Subject != record.Subject {
		return g.unwrapFailureLocked(ErrSubjectMismatch)
	}
	if env.Purpose != record.Purpose {
		return g.unwrapFailureLocked(ErrPurposeMismatch)
	}
	if env.Algorithm != record.Algorithm {
		return g.unwrapFailureLocked(ErrAlgorithmMismatch)
	}
	if env.Generation != record.Generation {
		return g.unwrapFailureLocked(errors.Join(ErrGenerationMismatch, ErrAlgorithmMismatch))
	}

	dek, ok := g.deks[record.DEKID]
	if !ok {
		return g.unwrapFailureLocked(ErrDEKNotFound)
	}
	kek, ok := g.generations[record.Generation]
	if !ok {
		return g.unwrapFailureLocked(ErrGenerationRetired)
	}
	if kek.algorithm != record.Algorithm || kek.generation != record.Generation {
		return g.unwrapFailureLocked(ErrAlgorithmMismatch)
	}
	if kek.retired {
		return g.unwrapFailureLocked(ErrGenerationRetired)
	}

	plain, err := openEnvelope(env)
	if err != nil {
		return g.unwrapFailureLocked(err)
	}
	if !bytes.Equal(plain, dek.key) {
		return g.unwrapFailureLocked(ErrEnvelopeIntegrity)
	}

	g.failures = 0
	g.breakerOpen = false
	g.stats.Unwraps++
	return plain, nil
}

// RotateKEK advances the key ring to the next generation.
func (g *KeyWrapGuard) RotateKEK(alg Algorithm, quota int) (int, error) {
	if !validAlgorithm(alg) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAlgorithm, alg)
	}
	if quota < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidQuota, quota)
	}
	if g == nil {
		return 0, ErrBadSnapshot
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	next := g.currentGen + 1
	g.generations[next] = &kekGeneration{
		generation: next,
		algorithm:  alg,
		quota:      quota,
	}
	g.currentGen = next
	return next, nil
}

// ResealBatch re-wraps every envelope of one purpose onto the current generation.
func (g *KeyWrapGuard) ResealBatch(purpose Purpose) (ResealReport, error) {
	if g == nil {
		return ResealReport{}, ErrBadSnapshot
	}
	if !validPurpose(purpose) {
		return ResealReport{}, fmt.Errorf("%w: %q", ErrInvalidPurpose, purpose)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if purpose != g.purpose {
		return ResealReport{}, ErrPurposeMismatch
	}
	target, ok := g.generations[g.currentGen]
	if !ok || target.generation != g.currentGen {
		return ResealReport{}, ErrGenerationRetired
	}
	if target.retired {
		return ResealReport{}, ErrGenerationRetired
	}

	report := ResealReport{From: target.generation, To: target.generation}
	candidates := make([]Envelope, 0)
	for _, env := range g.orderedEnvelopesLocked() {
		if env.Purpose != purpose {
			continue
		}
		if env.Generation == target.generation && env.Algorithm == target.algorithm {
			report.Skipped++
			continue
		}
		candidates = append(candidates, env)
		if len(candidates) == 1 || env.Generation < report.From {
			report.From = env.Generation
		}
	}
	report.Resealed = len(candidates)

	if len(candidates) == 0 {
		return report, nil
	}
	if target.quota-target.quotaUsed < len(candidates) {
		return ResealReport{}, ErrKEKQuotaExhausted
	}

	type preparedEnvelope struct {
		env     Envelope
		wrapped []byte
	}
	prepared := make([]preparedEnvelope, 0, len(candidates))
	for _, old := range candidates {
		plain, err := openEnvelope(old)
		if err != nil {
			return ResealReport{}, err
		}
		dek, ok := g.deks[old.DEKID]
		if !ok || !bytes.Equal(plain, dek.key) {
			return ResealReport{}, ErrEnvelopeIntegrity
		}

		next := old
		next.Generation = target.generation
		next.Algorithm = target.algorithm
		next.Wrapped = nil
		wrapped, err := sealEnvelope(next, plain)
		if err != nil {
			return ResealReport{}, err
		}
		prepared = append(prepared, preparedEnvelope{env: next, wrapped: wrapped})
	}

	// All quota and cryptographic preconditions have succeeded. Only now
	// mutate visible state, making the batch commit atomic.
	for _, item := range prepared {
		item.env.Wrapped = item.wrapped
		g.envelopes[item.env.ID] = cloneEnvelope(item.env)
	}
	for _, dek := range g.deks {
		if len(dek.envelopeIDs) == 0 {
			continue
		}
		latestID := dek.envelopeIDs[len(dek.envelopeIDs)-1]
		if latest, ok := g.envelopes[latestID]; ok {
			dek.envelopeID = latest.ID
			dek.generation = latest.Generation
			dek.algorithm = latest.Algorithm
		}
	}
	target.quotaUsed += len(prepared)
	g.stats.Resealed += len(prepared)

	return report, nil
}

// RetireGeneration retires one KEK generation.
func (g *KeyWrapGuard) RetireGeneration(gen int) error {
	if g == nil {
		return ErrGenerationNotFound
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	kek, ok := g.generations[gen]
	if !ok {
		return fmt.Errorf("%w: %d", ErrGenerationNotFound, gen)
	}
	kek.retired = true
	return nil
}

// AuditQueue lists envelopes that reference retired generations.
func (g *KeyWrapGuard) AuditQueue() []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ids := make([]string, 0)
	for _, env := range g.orderedEnvelopesLocked() {
		if kek, ok := g.generations[env.Generation]; ok && kek.retired {
			ids = append(ids, env.ID)
		}
	}
	return ids
}

// SetFailureThreshold sets how many unwrap failures open the breaker.
func (g *KeyWrapGuard) SetFailureThreshold(n int) error {
	if n <= 0 {
		return ErrInvalidThreshold
	}
	if g == nil {
		return ErrBadSnapshot
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.failThreshold = n
	if !g.breakerOpen && g.failures >= n {
		g.breakerOpen = true
	}
	return nil
}

// BreakerOpen reports whether unwrap is currently cut off.
func (g *KeyWrapGuard) BreakerOpen() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.breakerOpen || (g.failThreshold > 0 && g.failures >= g.failThreshold)
}

// DEKStatus reports one registered data key.
func (g *KeyWrapGuard) DEKStatus(id string) (DEKStatus, error) {
	if g == nil {
		return DEKStatus{}, ErrDEKNotFound
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	dek, ok := g.deks[id]
	if !ok {
		return DEKStatus{}, ErrDEKNotFound
	}

	status := DEKStatus{
		ID:         dek.id,
		Subject:    dek.subject,
		Purpose:    dek.purpose,
		Wraps:      dek.wraps,
		MaxWraps:   dek.maxWraps,
		Generation: dek.generation,
		Algorithm:  dek.algorithm,
		EnvelopeID: dek.envelopeID,
	}

	active := g.latestActiveEnvelopeLocked(dek)
	if active == nil && dek.envelopeID != "" {
		if env, ok := g.envelopes[dek.envelopeID]; ok {
			active = &env
		}
	}
	if active == nil && dek.envelopeID != "" && dek.generation > 0 {
		if kek, ok := g.generations[dek.generation]; ok && kek.retired {
			return DEKStatus{}, ErrGenerationRetired
		}
		return status, nil
	}
	if active == nil {
		if dek.wraps > 0 {
			return DEKStatus{}, ErrGenerationRetired
		}
		return status, nil
	}

	if kek, ok := g.generations[active.Generation]; ok && kek.retired {
		return DEKStatus{}, ErrGenerationRetired
	}
	status.Subject = active.Subject
	status.Generation = active.Generation
	status.Algorithm = active.Algorithm
	status.EnvelopeID = active.ID
	return status, nil
}

// QuotaRemaining reports the unused wrap allowance of the current generation.
func (g *KeyWrapGuard) QuotaRemaining() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	kek, ok := g.generations[g.currentGen]
	if !ok {
		return 0
	}
	remaining := kek.quota - kek.quotaUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Generation reports the KEK generation currently in force.
func (g *KeyWrapGuard) Generation() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentGen
}

// Snapshot captures the key ring, envelopes and counters.
func (g *KeyWrapGuard) Snapshot() (KeyWrapSnapshot, error) {
	if g == nil {
		return KeyWrapSnapshot{}, ErrBadSnapshot
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	current, ok := g.generations[g.currentGen]
	if !ok {
		return KeyWrapSnapshot{}, fmt.Errorf("%w: active generation", ErrBadSnapshot)
	}
	snapshotFailures := g.failures
	if g.breakerOpen && g.failThreshold > 0 && snapshotFailures < g.failThreshold {
		snapshotFailures = g.failThreshold
	}

	dekIDs := make([]string, 0, len(g.deks))
	for id := range g.deks {
		dekIDs = append(dekIDs, id)
	}
	sort.Strings(dekIDs)

	snap := KeyWrapSnapshot{
		Purpose:          g.purpose,
		Generation:       g.currentGen,
		Algorithm:        current.algorithm,
		Quota:            current.quota,
		QuotaUsed:        current.quotaUsed,
		FailureThreshold: g.failThreshold,
		Failures:         snapshotFailures,
		DEKs:             make([]DEKSnapshot, 0, len(dekIDs)),
		Envelopes:        g.orderedEnvelopesLocked(),
		Retired:          make([]int, 0),
	}

	for _, id := range dekIDs {
		dek := g.deks[id]
		snap.DEKs = append(snap.DEKs, DEKSnapshot{
			ID:         dek.id,
			Subject:    dek.subject,
			Purpose:    dek.purpose,
			Key:        cloneBytes(dek.key),
			Wraps:      dek.wraps,
			MaxWraps:   dek.maxWraps,
			Generation: dek.generation,
			Algorithm:  dek.algorithm,
			EnvelopeID: dek.envelopeID,
		})
	}

	generations := make([]int, 0, len(g.generations))
	for gen := range g.generations {
		generations = append(generations, gen)
	}
	sort.Ints(generations)
	for _, gen := range generations {
		kek := g.generations[gen]
		if kek.retired {
			snap.Retired = append(snap.Retired, kek.generation)
		}
	}

	return snap, nil
}

// Restore replaces the guard contents with a snapshot.
func (g *KeyWrapGuard) Restore(snap KeyWrapSnapshot) error {
	if g == nil {
		return ErrBadSnapshot
	}

	generations, err := generationsFromSnapshot(snap)
	if err != nil {
		return err
	}
	deks, envelopes, idsByDEK, err := recordsFromSnapshot(snap, generations)
	if err != nil {
		return err
	}

	nextEnvSeq := 1
	for id := range envelopes {
		if seq, ok := envelopeSequence(id); ok && seq >= nextEnvSeq {
			nextEnvSeq = seq + 1
		}
	}

	stats := KeyWrapStats{Failures: snap.Failures}
	stats.DEKs = len(deks)
	stats.Envelopes = len(envelopes)
	totalWraps := 0
	for _, dek := range deks {
		totalWraps += dek.wraps
	}
	stats.Wraps = totalWraps
	breakerOpen := snap.FailureThreshold > 0 && snap.Failures >= snap.FailureThreshold

	g.mu.Lock()
	defer g.mu.Unlock()

	g.purpose = snap.Purpose
	g.currentGen = snap.Generation
	g.nextEnvSeq = nextEnvSeq
	g.generations = generations
	g.deks = deks
	g.envelopes = envelopes
	g.failThreshold = snap.FailureThreshold
	g.failures = snap.Failures
	g.breakerOpen = breakerOpen
	g.stats = stats

	for id, dek := range deks {
		ids := idsByDEK[id]
		sortEnvelopeIDs(ids)
		dek.envelopeIDs = ids
		if len(ids) > 0 {
			latest := envelopes[ids[len(ids)-1]]
			dek.envelopeID = latest.ID
			dek.generation = latest.Generation
			dek.algorithm = latest.Algorithm
			if dek.subject == "" {
				dek.subject = latest.Subject
			}
		}
	}
	return nil
}

// Stats aggregates guard counters.
func (g *KeyWrapGuard) Stats() KeyWrapStats {
	if g == nil {
		return KeyWrapStats{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statsLocked()
}

func (g *KeyWrapGuard) statsLocked() KeyWrapStats {
	stats := g.stats
	stats.DEKs = len(g.deks)
	stats.Envelopes = len(g.envelopes)
	wraps := 0
	for _, dek := range g.deks {
		wraps += dek.wraps
	}
	stats.Wraps = wraps
	return stats
}

func (g *KeyWrapGuard) unwrapFailureLocked(err error) ([]byte, error) {
	g.failures++
	g.stats.Failures++
	if g.failThreshold > 0 && g.failures >= g.failThreshold {
		g.breakerOpen = true
	}
	return nil, err
}

func (g *KeyWrapGuard) latestActiveEnvelopeLocked(dek *dekRecord) *Envelope {
	for i := len(dek.envelopeIDs) - 1; i >= 0; i-- {
		id := dek.envelopeIDs[i]
		env, ok := g.envelopes[id]
		if !ok {
			continue
		}
		kek, ok := g.generations[env.Generation]
		if ok && kek.retired {
			continue
		}
		return &env
	}
	return nil
}

func (g *KeyWrapGuard) orderedEnvelopesLocked() []Envelope {
	out := make([]Envelope, 0, len(g.envelopes))
	for _, env := range g.envelopes {
		out = append(out, cloneEnvelope(env))
	}
	sort.SliceStable(out, func(i, j int) bool {
		seqI, okI := envelopeSequence(out[i].ID)
		seqJ, okJ := envelopeSequence(out[j].ID)
		if okI && okJ && seqI != seqJ {
			return seqI < seqJ
		}
		if okI != okJ {
			return okI
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func generationsFromSnapshot(snap KeyWrapSnapshot) (map[int]*kekGeneration, error) {
	if !validPurpose(snap.Purpose) {
		return nil, badSnapshotf("invalid purpose %q", snap.Purpose)
	}
	if snap.Generation < 1 {
		return nil, badSnapshotf("generation must be positive")
	}
	if !validAlgorithm(snap.Algorithm) {
		return nil, badSnapshotf("invalid algorithm %q", snap.Algorithm)
	}
	if snap.Quota < 0 || snap.QuotaUsed < 0 || snap.QuotaUsed > snap.Quota {
		return nil, badSnapshotf("invalid quota state")
	}
	if snap.FailureThreshold < 0 {
		return nil, badSnapshotf("failure threshold is negative")
	}
	if snap.Failures < 0 {
		return nil, badSnapshotf("failure streak is negative")
	}

	gens := make(map[int]*kekGeneration)
	gens[snap.Generation] = &kekGeneration{
		generation: snap.Generation,
		algorithm:  snap.Algorithm,
		quota:      snap.Quota,
		quotaUsed:  snap.QuotaUsed,
	}
	for _, gen := range snap.Retired {
		if gen < 1 {
			return nil, badSnapshotf("invalid retired generation %d", gen)
		}
		kek, ok := gens[gen]
		if !ok {
			kek = &kekGeneration{generation: gen, algorithm: snap.Algorithm, quota: defaultKEKQuota}
			gens[gen] = kek
		}
		kek.retired = true
	}
	return gens, nil
}

func recordsFromSnapshot(
	snap KeyWrapSnapshot,
	gens map[int]*kekGeneration,
) (map[string]*dekRecord, map[string]Envelope, map[string][]string, error) {
	deks := make(map[string]*dekRecord, len(snap.DEKs))
	for _, item := range snap.DEKs {
		if item.ID == "" {
			return nil, nil, nil, badSnapshotf("dek id is empty")
		}
		if _, exists := deks[item.ID]; exists {
			return nil, nil, nil, badSnapshotf("duplicate dek %q", item.ID)
		}
		if len(item.Key) < 8 || item.MaxWraps <= 0 || item.Wraps < 0 || item.Wraps > item.MaxWraps {
			return nil, nil, nil, badSnapshotf("invalid dek %q", item.ID)
		}
		purpose := item.Purpose
		if purpose == "" {
			purpose = snap.Purpose
		}
		if !validPurpose(purpose) || purpose != snap.Purpose {
			return nil, nil, nil, badSnapshotf("dek %q purpose mismatch", item.ID)
		}
		if item.Generation < 0 || (item.Generation > 0 && !validAlgorithm(item.Algorithm)) {
			return nil, nil, nil, badSnapshotf("dek %q generation binding", item.ID)
		}
		deks[item.ID] = &dekRecord{
			id:         item.ID,
			key:        cloneBytes(item.Key),
			subject:    item.Subject,
			purpose:    purpose,
			wraps:      item.Wraps,
			maxWraps:   item.MaxWraps,
			generation: item.Generation,
			algorithm:  item.Algorithm,
			envelopeID: item.EnvelopeID,
		}
	}

	envelopes := make(map[string]Envelope, len(snap.Envelopes))
	idsByDEK := make(map[string][]string)
	for _, item := range snap.Envelopes {
		if item.ID == "" {
			return nil, nil, nil, badSnapshotf("envelope id is empty")
		}
		if _, exists := envelopes[item.ID]; exists {
			return nil, nil, nil, badSnapshotf("duplicate envelope %q", item.ID)
		}
		if item.DEKID == "" {
			return nil, nil, nil, badSnapshotf("envelope %q missing dek", item.ID)
		}
		if _, ok := deks[item.DEKID]; !ok {
			return nil, nil, nil, badSnapshotf("envelope %q references unknown dek", item.ID)
		}
		if strings.TrimSpace(item.Subject) == "" {
			return nil, nil, nil, badSnapshotf("envelope %q missing subject", item.ID)
		}
		if !validPurpose(item.Purpose) || item.Purpose != snap.Purpose {
			return nil, nil, nil, badSnapshotf("envelope %q purpose mismatch", item.ID)
		}
		if item.Generation < 1 || !validAlgorithm(item.Algorithm) || len(item.Wrapped) == 0 {
			return nil, nil, nil, badSnapshotf("envelope %q algorithm/generation binding", item.ID)
		}

		kek, ok := gens[item.Generation]
		if !ok {
			gens[item.Generation] = &kekGeneration{
				generation: item.Generation,
				algorithm:  item.Algorithm,
				quota:      defaultKEKQuota,
			}
		} else if kek.algorithm != item.Algorithm {
			return nil, nil, nil, badSnapshotf("envelope %q algorithm does not match generation %d",
				item.ID, item.Generation)
		}

		envelopes[item.ID] = cloneEnvelope(item)
		idsByDEK[item.DEKID] = append(idsByDEK[item.DEKID], item.ID)
	}

	return deks, envelopes, idsByDEK, nil
}

func validPurpose(p Purpose) bool {
	switch p {
	case PurposeData, PurposeBackup, PurposeExport:
		return true
	default:
		return false
	}
}

func validAlgorithm(alg Algorithm) bool {
	switch alg {
	case AlgorithmAES256GCM, AlgorithmChaCha20:
		return true
	default:
		return false
	}
}

func badSnapshotf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadSnapshot, fmt.Sprintf(format, args...))
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneEnvelope(env Envelope) Envelope {
	env.Wrapped = cloneBytes(env.Wrapped)
	return env
}

func envelopeSequence(id string) (int, bool) {
	if !strings.HasPrefix(id, envelopeIDPrefix) {
		return 0, false
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(id, envelopeIDPrefix))
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}

func sortEnvelopeIDs(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		seqI, okI := envelopeSequence(ids[i])
		seqJ, okJ := envelopeSequence(ids[j])
		if okI && okJ && seqI != seqJ {
			return seqI < seqJ
		}
		if okI != okJ {
			return okI
		}
		return ids[i] < ids[j]
	})
}

func deriveKEK(purpose Purpose, generation int, alg Algorithm) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte("gsb/batch48-go-safety-d-02/KeyWrapGuard/v1"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(purpose))
	var generationBytes [8]byte
	binary.BigEndian.PutUint64(generationBytes[:], uint64(generation))
	_, _ = h.Write(generationBytes[:])
	_, _ = h.Write([]byte(alg))
	return h.Sum(nil)
}

func envelopeAAD(env Envelope) []byte {
	aad := make([]byte, 0, 128+len(env.ID)+len(env.DEKID)+len(env.Subject))
	aad = appendLengthPrefixed(aad, "key-wrap-envelope-v1")
	aad = appendLengthPrefixed(aad, env.ID)
	aad = appendLengthPrefixed(aad, env.DEKID)
	aad = appendLengthPrefixed(aad, env.Subject)
	aad = appendLengthPrefixed(aad, string(env.Purpose))
	aad = appendLengthPrefixed(aad, string(env.Algorithm))
	var generationBytes [8]byte
	binary.BigEndian.PutUint64(generationBytes[:], uint64(env.Generation))
	aad = append(aad, generationBytes[:]...)
	return aad
}

func appendLengthPrefixed(dst []byte, value string) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func envelopeAEAD(env Envelope) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKEK(env.Purpose, env.Generation, env.Algorithm))
	if err != nil {
		return nil, fmt.Errorf("%w: kek cipher: %v", ErrEnvelopeIntegrity, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: kek aead: %v", ErrEnvelopeIntegrity, err)
	}
	return aead, nil
}

func sealEnvelope(env Envelope, plaintext []byte) ([]byte, error) {
	aead, err := envelopeAEAD(env)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("%w: nonce: %v", ErrEnvelopeIntegrity, err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, envelopeAAD(env))
	wrapped := make([]byte, 0, len(nonce)+len(ciphertext))
	wrapped = append(wrapped, nonce...)
	wrapped = append(wrapped, ciphertext...)
	return wrapped, nil
}

func openEnvelope(env Envelope) ([]byte, error) {
	aead, err := envelopeAEAD(env)
	if err != nil {
		return nil, err
	}
	if len(env.Wrapped) < aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("%w: short payload", ErrEnvelopeIntegrity)
	}
	nonce := env.Wrapped[:aead.NonceSize()]
	ciphertext := env.Wrapped[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, envelopeAAD(env))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvelopeIntegrity, err)
	}
	return plaintext, nil
}
