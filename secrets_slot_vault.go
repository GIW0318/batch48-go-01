package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors reported by the slot vault.
var (
	ErrInvalidTenant    = errors.New("secrets_slot_vault: invalid tenant")
	ErrInvalidSlot      = errors.New("secrets_slot_vault: invalid slot")
	ErrSlotExists       = errors.New("secrets_slot_vault: slot already exists")
	ErrSlotNotFound     = errors.New("secrets_slot_vault: slot not found")
	ErrSlotLimit        = errors.New("secrets_slot_vault: slot limit reached")
	ErrSlotLocked       = errors.New("secrets_slot_vault: slot locked")
	ErrSlotRetired      = errors.New("secrets_slot_vault: slot retired")
	ErrNotSealed        = errors.New("secrets_slot_vault: slot carries no sealed record")
	ErrSecretMismatch   = errors.New("secrets_slot_vault: secret mismatch")
	ErrUnknownRecovery  = errors.New("secrets_slot_vault: unknown recovery code")
	ErrRecoveryUsed     = errors.New("secrets_slot_vault: recovery code already consumed")
	ErrRotationRequired = errors.New("secrets_slot_vault: rotation required before open")
	ErrStaleGeneration  = errors.New("secrets_slot_vault: generation must advance by exactly one")
	ErrInvalidKEK       = errors.New("secrets_slot_vault: invalid kek id")
	ErrBadSnapshot      = errors.New("secrets_slot_vault: bad snapshot")
)

const (
	defaultAlgorithm  = "aes-256-gcm"
	initialGeneration = 1
	initialKEKID      = "kek-1"
	defaultGraceOpens = 2
)

// SlotKind is the category of material kept in one slot.
type SlotKind string

// Supported slot kinds.
const (
	SlotKindPassword SlotKind = "password"
	SlotKindTOTP     SlotKind = "totp"
	SlotKindRecovery SlotKind = "recovery"
)

// SlotState is the lifecycle state of one slot.
type SlotState string

// Supported slot states.
const (
	SlotActive  SlotState = "active"
	SlotLocked  SlotState = "locked"
	SlotRetired SlotState = "retired"
)

// SlotSpec is one slot registration request.
type SlotSpec struct {
	Tenant string
	User   string
	Name   string
	Kind   SlotKind
	Secret []byte
	Codes  []string
}

// SealedRecord is the sealed form of one slot, bound to a KEK generation.
type SealedRecord struct {
	Tenant     string
	User       string
	Name       string
	Generation int
	KEKID      string
	Algorithm  string
	Tag        uint64
}

// SlotStatus is the observable state of one slot.
type SlotStatus struct {
	Tenant     string
	User       string
	Name       string
	Kind       SlotKind
	State      SlotState
	Generation int
	Failures   int
	Consumed   int
	Remaining  int
	Grace      int
}

// RotateReport describes one KEK rotation.
type RotateReport struct {
	FromGeneration int
	ToGeneration   int
	Resealed       int
	Skipped        int
}

// SlotSnapshot is the portable form of one slot.
type SlotSnapshot struct {
	Tenant     string
	User       string
	Name       string
	Kind       SlotKind
	State      SlotState
	Generation int
	Failures   int
	Consumed   int
	Sealed     bool
	Secret     []byte
	Codes      []string
	UsedCodes  []string
}

// VaultSnapshot is the portable form of a whole vault.
type VaultSnapshot struct {
	Generation  int
	KEKID       string
	Grace       int
	MaxFailures int
	MaxSlots    int
	Slots       []SlotSnapshot
}

// VaultStats aggregates slot counters.
type VaultStats struct {
	Slots    int
	Sealed   int
	Active   int
	Locked   int
	Retired  int
	Failures int
	Consumed int
}

type slotKey struct {
	tenant string
	user   string
	name   string
}

type secretSlot struct {
	spec         SlotSpec
	state        SlotState
	sealed       bool
	generation   int
	kekID        string
	algorithm    string
	tag          uint64
	failures     int
	consumed     int
	usedCodes    map[string]struct{}
	graceUsed    map[int]int
	lastGraceGen int
}

// SecretSlotVault keeps sealed secrets per tenant and user.
type SecretSlotVault struct {
	mu          sync.Mutex
	maxSlots    int
	maxFailures int
	generation  int
	kekID       string
	graceOpens  int
	slots       map[slotKey]*secretSlot
}

// NewSecretSlotVault builds a vault with a slot ceiling and a failure ceiling.
func NewSecretSlotVault(maxSlots, maxFailures int) *SecretSlotVault {
	if maxSlots < 0 {
		maxSlots = 0
	}
	if maxFailures < 0 {
		maxFailures = 0
	}
	return &SecretSlotVault{
		maxSlots:    maxSlots,
		maxFailures: maxFailures,
		generation:  initialGeneration,
		kekID:       initialKEKID,
		graceOpens:  defaultGraceOpens,
		slots:       make(map[slotKey]*secretSlot),
	}
}

// Generation reports the KEK generation currently in force.
func (v *SecretSlotVault) Generation() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.generation
}

// GraceOpens reports how many opens an unrenewed record may still serve.
func (v *SecretSlotVault) GraceOpens() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.graceOpens
}

// SetGraceOpens changes the grace allowance for stale records.
func (v *SecretSlotVault) SetGraceOpens(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: grace must not be negative", ErrInvalidSlot)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.graceOpens = n
	return nil
}

// PutSlot registers one slot for a tenant and user.
func (v *SecretSlotVault) PutSlot(spec SlotSpec) error {
	if err := validateCoordinates(spec.Tenant, spec.User, spec.Name); err != nil {
		return err
	}
	if !validKind(spec.Kind) {
		return ErrInvalidSlot
	}

	key := makeKey(spec.Tenant, spec.User, spec.Name)

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, exists := v.slots[key]; exists {
		return ErrSlotExists
	}
	if len(v.slots) >= v.maxSlots {
		return ErrSlotLimit
	}

	slot := &secretSlot{
		spec: SlotSpec{
			Tenant: spec.Tenant,
			User:   spec.User,
			Name:   spec.Name,
			Kind:   spec.Kind,
			Secret: append([]byte(nil), spec.Secret...),
			Codes:  append([]string(nil), spec.Codes...),
		},
		state:      SlotActive,
		generation: v.generation,
		kekID:      v.kekID,
		algorithm:  defaultAlgorithm,
		usedCodes:  make(map[string]struct{}),
		graceUsed:  make(map[int]int),
	}
	v.slots[key] = slot
	return nil
}

// Seal wraps the slot payload into a record bound to the current generation.
func (v *SecretSlotVault) Seal(tenant, user, name string) (SealedRecord, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, err := v.slotFor(tenant, user, name)
	if err != nil {
		return SealedRecord{}, err
	}
	if slot.state == SlotRetired {
		return SealedRecord{}, ErrSlotRetired
	}
	if !hasMaterial(slot) {
		return SealedRecord{}, ErrInvalidSlot
	}

	slot.generation = v.generation
	slot.kekID = v.kekID
	slot.algorithm = defaultAlgorithm
	slot.tag = computeTag(slot, slot.generation, slot.kekID)
	slot.sealed = true
	return slot.record(), nil
}

// Record returns the current sealed record of one slot.
func (v *SecretSlotVault) Record(tenant, user, name string) (SealedRecord, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, err := v.slotFor(tenant, user, name)
	if err != nil {
		return SealedRecord{}, err
	}
	if !slot.sealed {
		return SealedRecord{}, ErrNotSealed
	}
	return slot.record(), nil
}

// Open verifies a secret against the slot behind the supplied sealed record.
func (v *SecretSlotVault) Open(rec SealedRecord, secret []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, ok := v.slots[makeKey(rec.Tenant, rec.User, rec.Name)]
	if !ok {
		return ErrSlotNotFound
	}
	if slot.state == SlotRetired {
		return ErrSlotRetired
	}
	if slot.state == SlotLocked {
		return ErrSlotLocked
	}
	if v.maxFailures > 0 && slot.failures >= v.maxFailures {
		slot.state = SlotLocked
		return ErrSlotLocked
	}
	if !slot.sealed {
		return ErrNotSealed
	}
	if !recordIsIntegral(slot, rec) {
		return registerOpenFailure(v, slot)
	}

	if rec.Generation != slot.generation || rec.KEKID != slot.kekID {
		if rec.Generation > slot.generation {
			return ErrSecretMismatch
		}
		if slot.graceUsed == nil {
			slot.graceUsed = make(map[int]int)
		}
		usedGrace := slot.graceUsed[rec.Generation]
		if usedGrace >= v.graceOpens {
			return ErrRotationRequired
		}
		slot.graceUsed[rec.Generation] = usedGrace + 1
		slot.lastGraceGen = rec.Generation
	}

	if subtle.ConstantTimeCompare(expectedSecret(slot), secret) != 1 {
		return registerOpenFailure(v, slot)
	}

	slot.failures = 0
	return nil
}

// ConsumeRecovery spends one recovery code of a recovery slot.
func (v *SecretSlotVault) ConsumeRecovery(tenant, user, code string) (bool, error) {
	if strings.TrimSpace(tenant) == "" {
		return false, ErrInvalidTenant
	}
	if strings.TrimSpace(user) == "" {
		return false, ErrInvalidSlot
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	slots := v.recoverySlotsLocked(tenant, user)
	if len(slots) == 0 {
		return false, ErrUnknownRecovery
	}

	sawUsed := false
	sawRetired := false
	for _, slot := range slots {
		if _, used := slot.usedCodes[code]; used {
			sawUsed = true
			continue
		}
		if !codePresent(slot.spec.Codes, code) {
			continue
		}
		if slot.state == SlotRetired {
			sawRetired = true
			continue
		}

		slot.usedCodes[code] = struct{}{}
		slot.consumed++
		if remainingCodes(slot) == 0 {
			slot.state = SlotLocked
		}
		return true, nil
	}

	if sawUsed {
		return false, ErrRecoveryUsed
	}
	if sawRetired {
		return false, ErrSlotRetired
	}
	return false, ErrUnknownRecovery
}

// RotateKEK advances to the next KEK generation and re-seals every slot.
func (v *SecretSlotVault) RotateKEK(nextGeneration int, kekID string) (RotateReport, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if nextGeneration != v.generation+1 {
		return RotateReport{}, ErrStaleGeneration
	}
	if strings.TrimSpace(kekID) == "" {
		return RotateReport{}, ErrInvalidKEK
	}

	keys := sortedSlotKeys(v.slots)
	for _, key := range keys {
		slot := v.slots[key]
		if slot.state != SlotRetired && !slot.sealed {
			return RotateReport{}, ErrNotSealed
		}
	}

	report := RotateReport{FromGeneration: v.generation, ToGeneration: nextGeneration}
	v.generation = nextGeneration
	v.kekID = kekID

	for _, key := range keys {
		slot := v.slots[key]
		if slot.state == SlotRetired {
			report.Skipped++
			continue
		}

		slot.generation = nextGeneration
		slot.kekID = kekID
		slot.algorithm = defaultAlgorithm
		slot.tag = computeTag(slot, nextGeneration, kekID)
		if slot.graceUsed == nil {
			slot.graceUsed = make(map[int]int)
		}
		slot.lastGraceGen = report.FromGeneration
		report.Resealed++
	}

	return report, nil
}

func registerOpenFailure(v *SecretSlotVault, slot *secretSlot) error {
	slot.failures++
	if v.maxFailures > 0 && slot.failures >= v.maxFailures {
		slot.state = SlotLocked
		return ErrSlotLocked
	}
	return ErrSecretMismatch
}

// Unlock returns a locked slot to active.
func (v *SecretSlotVault) Unlock(tenant, user, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, err := v.slotFor(tenant, user, name)
	if err != nil {
		return err
	}
	if slot.state == SlotRetired {
		return ErrSlotRetired
	}

	slot.state = SlotActive
	slot.failures = 0
	return nil
}

// RegenRecovery installs a fresh batch of recovery codes on an unlocked slot.
func (v *SecretSlotVault) RegenRecovery(tenant, user string, codes []string) error {
	if strings.TrimSpace(tenant) == "" {
		return ErrInvalidTenant
	}
	if strings.TrimSpace(user) == "" {
		return ErrInvalidSlot
	}
	if len(codes) == 0 {
		return ErrInvalidSlot
	}
	for _, code := range codes {
		if code == "" {
			return ErrInvalidSlot
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	slots := v.recoverySlotsLocked(tenant, user)
	if len(slots) == 0 {
		return ErrSlotNotFound
	}

	var target *secretSlot
	for _, slot := range slots {
		if slot.state != SlotRetired {
			target = slot
			break
		}
	}
	if target == nil {
		return ErrSlotRetired
	}
	if target.state == SlotLocked {
		return ErrSlotLocked
	}

	target.spec.Codes = append([]string(nil), codes...)
	if target.usedCodes == nil {
		target.usedCodes = make(map[string]struct{})
	}
	if target.sealed {
		target.generation = v.generation
		target.kekID = v.kekID
		target.algorithm = defaultAlgorithm
		target.tag = computeTag(target, target.generation, target.kekID)
	}
	return nil
}

// Retire takes a slot out of service.
func (v *SecretSlotVault) Retire(tenant, user, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, err := v.slotFor(tenant, user, name)
	if err != nil {
		return err
	}
	slot.state = SlotRetired
	return nil
}

// SlotStatus reports one slot.
func (v *SecretSlotVault) SlotStatus(tenant, user, name string) (SlotStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	slot, err := v.slotFor(tenant, user, name)
	if err != nil {
		return SlotStatus{}, err
	}

	grace := v.graceOpens
	if slot.lastGraceGen != 0 {
		grace = v.graceOpens - slot.graceUsed[slot.lastGraceGen]
		if grace < 0 {
			grace = 0
		}
	}

	return SlotStatus{
		Tenant:     slot.spec.Tenant,
		User:       slot.spec.User,
		Name:       slot.spec.Name,
		Kind:       slot.spec.Kind,
		State:      slot.state,
		Generation: slot.generation,
		Failures:   slot.failures,
		Consumed:   slot.consumed,
		Remaining:  remainingCodes(slot),
		Grace:      grace,
	}, nil
}

// Snapshot captures generations, counters and sealed payloads.
func (v *SecretSlotVault) Snapshot() (VaultSnapshot, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	keys := sortedSlotKeys(v.slots)
	snapshot := VaultSnapshot{
		Generation:  v.generation,
		KEKID:       v.kekID,
		Grace:       v.graceOpens,
		MaxFailures: v.maxFailures,
		MaxSlots:    v.maxSlots,
		Slots:       make([]SlotSnapshot, 0, len(keys)),
	}

	for _, key := range keys {
		slot := v.slots[key]
		usedCodes := make([]string, 0, len(slot.usedCodes))
		for code := range slot.usedCodes {
			usedCodes = append(usedCodes, code)
		}
		sort.Strings(usedCodes)

		snapshot.Slots = append(snapshot.Slots, SlotSnapshot{
			Tenant:     slot.spec.Tenant,
			User:       slot.spec.User,
			Name:       slot.spec.Name,
			Kind:       slot.spec.Kind,
			State:      slot.state,
			Generation: slot.generation,
			Failures:   slot.failures,
			Consumed:   slot.consumed,
			Sealed:     slot.sealed,
			Secret:     append([]byte(nil), slot.spec.Secret...),
			Codes:      append([]string(nil), slot.spec.Codes...),
			UsedCodes:  usedCodes,
		})
	}

	return snapshot, nil
}

// Restore replaces the vault contents with a snapshot.
func (v *SecretSlotVault) Restore(snap VaultSnapshot) error {
	restored, err := vaultFromSnapshot(snap)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.maxSlots = restored.maxSlots
	v.maxFailures = restored.maxFailures
	v.generation = restored.generation
	v.kekID = restored.kekID
	v.graceOpens = restored.graceOpens
	v.slots = restored.slots
	return nil
}

// Stats aggregates slot counters.
func (v *SecretSlotVault) Stats() VaultStats {
	v.mu.Lock()
	defer v.mu.Unlock()

	stats := VaultStats{Slots: len(v.slots)}
	for _, slot := range v.slots {
		if slot.sealed {
			stats.Sealed++
		}
		switch slot.state {
		case SlotActive:
			stats.Active++
		case SlotLocked:
			stats.Locked++
		case SlotRetired:
			stats.Retired++
		}
		stats.Failures += slot.failures
		stats.Consumed += slot.consumed
	}
	return stats
}

func vaultFromSnapshot(snap VaultSnapshot) (*SecretSlotVault, error) {
	if snap.Generation < initialGeneration {
		return nil, fmt.Errorf("%w: generation must be at least %d", ErrBadSnapshot, initialGeneration)
	}
	if strings.TrimSpace(snap.KEKID) == "" {
		return nil, fmt.Errorf("%w: missing kek id", ErrBadSnapshot)
	}
	if snap.MaxSlots < 0 || snap.MaxFailures < 0 || snap.Grace < 0 {
		return nil, fmt.Errorf("%w: negative vault setting", ErrBadSnapshot)
	}

	restored := NewSecretSlotVault(snap.MaxSlots, snap.MaxFailures)
	restored.generation = snap.Generation
	restored.kekID = snap.KEKID
	restored.graceOpens = snap.Grace

	seen := make(map[slotKey]struct{}, len(snap.Slots))
	for _, item := range snap.Slots {
		if err := validateCoordinates(item.Tenant, item.User, item.Name); err != nil {
			return nil, fmt.Errorf("%w: invalid coordinates: %v", ErrBadSnapshot, err)
		}
		if !validKind(item.Kind) {
			return nil, fmt.Errorf("%w: invalid kind %q", ErrBadSnapshot, item.Kind)
		}
		if !validState(item.State) {
			return nil, fmt.Errorf("%w: invalid state %q", ErrBadSnapshot, item.State)
		}
		if item.Generation < initialGeneration {
			return nil, fmt.Errorf("%w: invalid slot generation", ErrBadSnapshot)
		}
		if item.Failures < 0 || item.Consumed < 0 {
			return nil, fmt.Errorf("%w: negative counter", ErrBadSnapshot)
		}

		key := makeKey(item.Tenant, item.User, item.Name)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate slot", ErrBadSnapshot)
		}
		seen[key] = struct{}{}

		usedCodes := make(map[string]struct{}, len(item.UsedCodes))
		for _, code := range item.UsedCodes {
			if code == "" {
				return nil, fmt.Errorf("%w: empty used code", ErrBadSnapshot)
			}
			if _, duplicate := usedCodes[code]; duplicate {
				return nil, fmt.Errorf("%w: duplicate used code", ErrBadSnapshot)
			}
			usedCodes[code] = struct{}{}
		}
		if item.Consumed < len(usedCodes) {
			return nil, fmt.Errorf("%w: consumed counter precedes used codes", ErrBadSnapshot)
		}

		slot := &secretSlot{
			spec: SlotSpec{
				Tenant: item.Tenant,
				User:   item.User,
				Name:   item.Name,
				Kind:   item.Kind,
				Secret: append([]byte(nil), item.Secret...),
				Codes:  append([]string(nil), item.Codes...),
			},
			state:      item.State,
			sealed:     item.Sealed,
			generation: item.Generation,
			failures:   item.Failures,
			consumed:   item.Consumed,
			usedCodes:  usedCodes,
			graceUsed:  make(map[int]int),
			algorithm:  defaultAlgorithm,
		}
		if item.Generation == snap.Generation {
			slot.kekID = snap.KEKID
		} else {
			slot.kekID = fmt.Sprintf("kek-%d", item.Generation)
		}
		if item.Sealed {
			if !hasMaterial(slot) {
				return nil, fmt.Errorf("%w: sealed slot has no payload", ErrBadSnapshot)
			}
			if strings.TrimSpace(slot.kekID) == "" {
				return nil, fmt.Errorf("%w: sealed slot has no kek id", ErrBadSnapshot)
			}
			slot.tag = computeTag(slot, slot.generation, slot.kekID)
		}
		if restored.maxFailures > 0 && slot.state == SlotActive && slot.failures >= restored.maxFailures {
			slot.state = SlotLocked
		}
		restored.slots[key] = slot
	}

	if len(restored.slots) > restored.maxSlots {
		return nil, fmt.Errorf("%w: slot count exceeds limit", ErrBadSnapshot)
	}
	return restored, nil
}

func validateCoordinates(tenant, user, name string) error {
	if strings.TrimSpace(tenant) == "" {
		return ErrInvalidTenant
	}
	if strings.TrimSpace(user) == "" || strings.TrimSpace(name) == "" {
		return ErrInvalidSlot
	}
	return nil
}

func validKind(kind SlotKind) bool {
	switch kind {
	case SlotKindPassword, SlotKindTOTP, SlotKindRecovery:
		return true
	default:
		return false
	}
}

func validState(state SlotState) bool {
	switch state {
	case SlotActive, SlotLocked, SlotRetired:
		return true
	default:
		return false
	}
}

func makeKey(tenant, user, name string) slotKey {
	return slotKey{tenant: tenant, user: user, name: name}
}

func (v *SecretSlotVault) slotFor(tenant, user, name string) (*secretSlot, error) {
	slot, ok := v.slots[makeKey(tenant, user, name)]
	if !ok {
		return nil, ErrSlotNotFound
	}
	return slot, nil
}

func (s *secretSlot) record() SealedRecord {
	return SealedRecord{
		Tenant:     s.spec.Tenant,
		User:       s.spec.User,
		Name:       s.spec.Name,
		Generation: s.generation,
		KEKID:      s.kekID,
		Algorithm:  s.algorithm,
		Tag:        s.tag,
	}
}

func hasMaterial(slot *secretSlot) bool {
	if slot.spec.Kind == SlotKindRecovery {
		return len(slot.spec.Codes) > 0
	}
	return len(slot.spec.Secret) > 0
}

func computeTag(slot *secretSlot, generation int, kekID string) uint64 {
	hash := sha256.New()
	putString := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write([]byte(value))
	}
	putBytes := func(value []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write(value)
	}
	putInt := func(value int) {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		hash.Write(encoded[:])
	}

	putString(defaultAlgorithm)
	putInt(generation)
	putString(kekID)
	putString(slot.spec.Tenant)
	putString(slot.spec.User)
	putString(slot.spec.Name)
	putString(string(slot.spec.Kind))
	if slot.spec.Kind == SlotKindRecovery {
		putInt(len(slot.spec.Codes))
		for _, code := range slot.spec.Codes {
			putString(code)
		}
	} else {
		putBytes(slot.spec.Secret)
	}

	sum := hash.Sum(nil)
	tag := binary.BigEndian.Uint64(sum[:8])
	if tag == 0 {
		return 1
	}
	return tag
}

func recordIsIntegral(slot *secretSlot, rec SealedRecord) bool {
	if rec.Tenant != slot.spec.Tenant || rec.User != slot.spec.User || rec.Name != slot.spec.Name {
		return false
	}
	if rec.Algorithm != defaultAlgorithm || rec.Generation < initialGeneration {
		return false
	}
	if strings.TrimSpace(rec.KEKID) == "" || rec.Tag == 0 {
		return false
	}
	return rec.Tag == computeTag(slot, rec.Generation, rec.KEKID)
}

func expectedSecret(slot *secretSlot) []byte {
	if slot.spec.Kind != SlotKindRecovery {
		return slot.spec.Secret
	}

	hash := sha256.New()
	for _, code := range slot.spec.Codes {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(code)))
		hash.Write(length[:])
		hash.Write([]byte(code))
	}
	return hash.Sum(nil)
}

func codePresent(codes []string, code string) bool {
	for _, candidate := range codes {
		if candidate == code {
			return true
		}
	}
	return false
}

func remainingCodes(slot *secretSlot) int {
	unique := make(map[string]struct{}, len(slot.spec.Codes))
	for _, code := range slot.spec.Codes {
		unique[code] = struct{}{}
	}
	remaining := len(unique)
	for code := range unique {
		if _, used := slot.usedCodes[code]; used {
			remaining--
		}
	}
	return max(remaining, 0)
}

func (v *SecretSlotVault) recoverySlotsLocked(tenant, user string) []*secretSlot {
	var slots []*secretSlot
	for key, slot := range v.slots {
		if key.tenant == tenant && key.user == user && slot.spec.Kind == SlotKindRecovery {
			slots = append(slots, slot)
		}
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].spec.Name < slots[j].spec.Name
	})
	return slots
}

func sortedSlotKeys(slots map[slotKey]*secretSlot) []slotKey {
	keys := make([]slotKey, 0, len(slots))
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenant != keys[j].tenant {
			return keys[i].tenant < keys[j].tenant
		}
		if keys[i].user != keys[j].user {
			return keys[i].user < keys[j].user
		}
		return keys[i].name < keys[j].name
	})
	return keys
}
