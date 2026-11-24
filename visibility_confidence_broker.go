// Package task brokers occlusion query samples between several render
// consumers. Each frame hands every consumer a weighted slice of the sampling
// budget, turns passed/total ratios into visible, hidden or unknown verdicts
// and keeps a verdict conservative when consumers disagree.
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"math"
	"sync"
)

// ObjectID identifies one renderable object.
type ObjectID = uint64

// Consumer identifies one render consumer that shares the query budget.
type Consumer string

// Consumers known to the broker.
const (
	ConsumerMain       Consumer = "main"
	ConsumerShadow     Consumer = "shadow"
	ConsumerReflection Consumer = "reflection"
)

// Verdict is the visibility answer for one object and one consumer.
type Verdict string

// Verdicts reported by the broker.
const (
	VerdictVisible Verdict = "visible"
	VerdictHidden  Verdict = "hidden"
	VerdictUnknown Verdict = "unknown"
)

// Config carries the sampling budget and the confidence thresholds.
type Config struct {
	SampleQuota uint32
	TTLFrames   uint64
	Low         float64
	High        float64
	Weights     map[Consumer]uint32
}

// Errors reported by the broker.
var (
	ErrUnknownConsumer = errors.New("consumer is unknown")
	ErrNoFrame         = errors.New("no frame has begun")
	ErrFrameOrder      = errors.New("frame number must increase")
	ErrBadSamples      = errors.New("sample counts are invalid")
	ErrBadThresholds   = errors.New("thresholds are invalid")
	ErrBadWeights      = errors.New("weights are invalid")
	ErrQuotaExhausted  = errors.New("sample quota is exhausted")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// knownConsumers is the closed, deterministic consumer order.
var knownConsumers = []Consumer{ConsumerMain, ConsumerShadow, ConsumerReflection}

const (
	snapshotMagic   = "VCBROKER01"
	snapshotVersion = uint16(1)
	snapshotHashLen = sha256.Size
)

// VisibilityConfidenceBroker shares occlusion samples between consumers.
type VisibilityConfidenceBroker struct {
	mu        sync.Mutex
	quota     uint32
	ttl       uint64
	low       float64
	high      float64
	weights   map[Consumer]uint32
	frame     uint64
	started   bool
	used      map[Consumer]uint32
	samples   map[ObjectID]map[Consumer]sample
	conflicts int
}

type sample struct {
	passed uint32
	total  uint32
	frame  uint64
}

// sampleDTO is the serialisable form of sample: gob cannot touch unexported
// fields, so every field here is exported.
type sampleDTO struct {
	Passed uint32
	Total  uint32
	Frame  uint64
}

type snapshotDTO struct {
	Version uint16
	Frame   uint64
	Started bool
	Quota   uint32
	TTL     uint64
	Low     float64
	High    float64
	Weights map[Consumer]uint32
	Used    map[Consumer]uint32
	Samples map[ObjectID]map[Consumer]sampleDTO
}

// NewVisibilityConfidenceBroker builds a broker from one config.
func NewVisibilityConfidenceBroker(cfg Config) *VisibilityConfidenceBroker {
	return &VisibilityConfidenceBroker{
		quota:   cfg.SampleQuota,
		ttl:     cfg.TTLFrames,
		low:     cfg.Low,
		high:    cfg.High,
		weights: normaliseWeights(cfg.Weights),
		used:    make(map[Consumer]uint32),
		samples: make(map[ObjectID]map[Consumer]sample),
	}
}

func isKnownConsumer(consumer Consumer) bool {
	for _, known := range knownConsumers {
		if consumer == known {
			return true
		}
	}
	return false
}

func weightsValid(weights map[Consumer]uint32) bool {
	for _, consumer := range knownConsumers {
		weight, ok := weights[consumer]
		if !ok || weight == 0 {
			return false
		}
	}
	return true
}

func equalWeights() map[Consumer]uint32 {
	return map[Consumer]uint32{
		ConsumerMain:       1,
		ConsumerShadow:     1,
		ConsumerReflection: 1,
	}
}

// normaliseWeights keeps valid config weights isolated from the caller's map
// and falls back to equal weights for an unusable constructor config. The
// mutating entry point SetWeights reports the same condition as an error.
func normaliseWeights(weights map[Consumer]uint32) map[Consumer]uint32 {
	if !weightsValid(weights) {
		return equalWeights()
	}
	return cloneWeights(weights)
}

func cloneWeights(weights map[Consumer]uint32) map[Consumer]uint32 {
	cloned := make(map[Consumer]uint32, len(knownConsumers))
	for _, consumer := range knownConsumers {
		cloned[consumer] = weights[consumer]
	}
	return cloned
}

func thresholdsValid(low, high float64) bool {
	if math.IsNaN(low) || math.IsNaN(high) || math.IsInf(low, 0) || math.IsInf(high, 0) {
		return false
	}
	if low < 0 || low > 1 || high < 0 || high > 1 {
		return false
	}
	return low <= high
}

// BeginFrame starts a new frame and refills every consumer quota.
func (b *VisibilityConfidenceBroker) BeginFrame(frame uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Frame numbers start at 1 and may only move strictly forwards. The zero
	// value of b.frame means no frame has ever opened, so frame == 0 and any
	// repeated/backwards number are both rejected before state is touched.
	if frame == 0 || frame <= b.frame {
		return ErrFrameOrder
	}

	b.frame = frame
	b.started = true
	b.used = map[Consumer]uint32{
		ConsumerMain:       0,
		ConsumerShadow:     0,
		ConsumerReflection: 0,
	}
	return nil
}

// Frame reports the frame that is currently open.
func (b *VisibilityConfidenceBroker) Frame() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.frame
}

func (b *VisibilityConfidenceBroker) weightSumLocked() uint64 {
	var sum uint64
	for _, consumer := range knownConsumers {
		sum += uint64(b.weights[consumer])
	}
	return sum
}

// quotaForLocked returns floor(SampleQuota * weight / sumWeights). Unknown
// consumers have a zero weight and therefore a zero quota.
func (b *VisibilityConfidenceBroker) quotaForLocked(consumer Consumer) uint32 {
	weight := uint64(b.weights[consumer])
	if weight == 0 {
		return 0
	}
	sum := b.weightSumLocked()
	if sum == 0 {
		return 0
	}
	quota := (uint64(b.quota) * weight) / sum
	return uint32(quota)
}

// Submit records one occlusion query result for a consumer.
func (b *VisibilityConfidenceBroker) Submit(objID ObjectID, consumer Consumer, passed, total uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.started {
		return ErrNoFrame
	}
	if !isKnownConsumer(consumer) {
		return ErrUnknownConsumer
	}
	if passed > total {
		return ErrBadSamples
	}

	quota := b.quotaForLocked(consumer)
	used := b.used[consumer]
	if used >= quota {
		return ErrQuotaExhausted
	}

	// Every accepted submission consumes one query slot. The table keeps only
	// the last submission for one object/consumer in the current frame, while
	// records of different consumers stay side by side.
	b.used[consumer] = used + 1

	byConsumer, ok := b.samples[objID]
	if !ok {
		byConsumer = make(map[Consumer]sample)
		b.samples[objID] = byConsumer
	}
	byConsumer[consumer] = sample{
		passed: passed,
		total:  total,
		frame:  b.frame,
	}
	return nil
}

// verdictLocked turns one retained sample into its effective verdict. Expired
// samples and zero-total ratios resolve to unknown instead of panicking or
// becoming hidden.
func (b *VisibilityConfidenceBroker) verdictLocked(record sample) Verdict {
	if b.frame < record.frame {
		return VerdictUnknown
	}
	age := b.frame - record.frame
	// "Exceeds TTL" means a strict greater-than: at age == TTL the boundary
	// sample is still counted once, on the next frame it falls back to unknown.
	if age > b.ttl {
		return VerdictUnknown
	}
	if record.total == 0 {
		return VerdictUnknown
	}

	ratio := float64(record.passed) / float64(record.total)
	switch {
	case ratio <= b.low:
		return VerdictHidden
	case ratio >= b.high:
		return VerdictVisible
	default:
		return VerdictUnknown
	}
}

// Visible turns the recorded samples of one object and consumer into a
// verdict. Every consumer is judged from its own samples only.
func (b *VisibilityConfidenceBroker) Visible(objID ObjectID, consumer Consumer) (Verdict, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !isKnownConsumer(consumer) {
		return "", ErrUnknownConsumer
	}
	byConsumer, ok := b.samples[objID]
	if !ok {
		return VerdictUnknown, nil
	}
	record, ok := byConsumer[consumer]
	if !ok {
		return VerdictUnknown, nil
	}
	return b.verdictLocked(record), nil
}

// SetThresholds replaces the low and high confidence thresholds.
func (b *VisibilityConfidenceBroker) SetThresholds(low, high float64) error {
	if !thresholdsValid(low, high) {
		return ErrBadThresholds
	}
	b.mu.Lock()
	b.low = low
	b.high = high
	b.mu.Unlock()
	return nil
}

// Thresholds reports the low and high confidence thresholds.
func (b *VisibilityConfidenceBroker) Thresholds() (float64, float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.low, b.high
}

// SetWeights replaces the per consumer budget weights. All three known
// consumers must be present with strictly positive weights.
func (b *VisibilityConfidenceBroker) SetWeights(weights map[Consumer]uint32) error {
	if !weightsValid(weights) {
		return ErrBadWeights
	}
	cloned := cloneWeights(weights)
	b.mu.Lock()
	b.weights = cloned
	b.mu.Unlock()
	return nil
}

// QuotaFor reports how many samples one consumer may submit this frame.
func (b *VisibilityConfidenceBroker) QuotaFor(consumer Consumer) uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.quotaForLocked(consumer)
}

// SamplesLeft reports how many samples one consumer may still submit.
func (b *VisibilityConfidenceBroker) SamplesLeft(consumer Consumer) uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	quota := b.quotaForLocked(consumer)
	used := b.used[consumer]
	if used >= quota {
		return 0
	}
	return quota - used
}

// ConflictCount reports how many objects currently hold opposing definitive
// verdicts (one consumer visible, another hidden). It is derived from the
// retained table so repeated calls, map iteration order, TTL expiry and
// invalidation all stay consistent and each object counts at most once.
func (b *VisibilityConfidenceBroker) ConflictCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := 0
	for _, byConsumer := range b.samples {
		hasVisible := false
		hasHidden := false
		for _, record := range byConsumer {
			switch b.verdictLocked(record) {
			case VerdictVisible:
				hasVisible = true
			case VerdictHidden:
				hasHidden = true
			}
		}
		if hasVisible && hasHidden {
			count++
		}
	}
	b.conflicts = count
	return count
}

// Invalidate drops every recorded sample of one object.
func (b *VisibilityConfidenceBroker) Invalidate(objID ObjectID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.samples, objID)
}

// Snapshot serialises the sample table, thresholds, weights, TTL, frame
// counter and per-consumer used quota.
func (b *VisibilityConfidenceBroker) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	dto := snapshotDTO{
		Version: snapshotVersion,
		Frame:   b.frame,
		Started: b.started,
		Quota:   b.quota,
		TTL:     b.ttl,
		Low:     b.low,
		High:    b.high,
		Weights: cloneWeights(b.weights),
		Used: map[Consumer]uint32{
			ConsumerMain:       b.used[ConsumerMain],
			ConsumerShadow:     b.used[ConsumerShadow],
			ConsumerReflection: b.used[ConsumerReflection],
		},
		Samples: make(map[ObjectID]map[Consumer]sampleDTO, len(b.samples)),
	}

	for objID, byConsumer := range b.samples {
		encoded := make(map[Consumer]sampleDTO, len(byConsumer))
		for consumer, record := range byConsumer {
			encoded[consumer] = sampleDTO{
				Passed: record.passed,
				Total:  record.total,
				Frame:  record.frame,
			}
		}
		dto.Samples[objID] = encoded
	}

	var gobBuffer bytes.Buffer
	if err := gob.NewEncoder(&gobBuffer).Encode(&dto); err != nil {
		return nil, err
	}
	raw := gobBuffer.Bytes()
	digest := sha256.Sum256(raw)

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(raw)))

	payload := make([]byte, 0, len(snapshotMagic)+4+len(raw)+snapshotHashLen)
	payload = append(payload, snapshotMagic...)
	payload = append(payload, length...)
	payload = append(payload, raw...)
	payload = append(payload, digest[:]...)
	return payload, nil
}

// Restore rebuilds the broker from a snapshot, either completely or not at
// all. The payload is fully decoded and validated before any receiver state is
// touched, so a rejected payload leaves the previous state intact.
func (b *VisibilityConfidenceBroker) Restore(payload []byte) error {
	dto, err := decodeSnapshot(payload)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.frame = dto.Frame
	b.started = dto.Started
	b.quota = dto.Quota
	b.ttl = dto.TTL
	b.low = dto.Low
	b.high = dto.High
	b.weights = cloneWeights(dto.Weights)

	b.used = make(map[Consumer]uint32, len(knownConsumers))
	for _, consumer := range knownConsumers {
		b.used[consumer] = dto.Used[consumer]
	}

	b.samples = make(map[ObjectID]map[Consumer]sample, len(dto.Samples))
	for objID, byConsumer := range dto.Samples {
		restored := make(map[Consumer]sample, len(byConsumer))
		for consumer, record := range byConsumer {
			restored[consumer] = sample{
				passed: record.Passed,
				total:  record.Total,
				frame:  record.Frame,
			}
		}
		b.samples[objID] = restored
	}
	b.conflicts = 0
	return nil
}

func decodeSnapshot(payload []byte) (*snapshotDTO, error) {
	magicLen := len(snapshotMagic)
	headerLen := magicLen + 4
	if len(payload) < headerLen+snapshotHashLen {
		return nil, ErrBadSnapshot
	}
	if string(payload[:magicLen]) != snapshotMagic {
		return nil, ErrBadSnapshot
	}

	rawLen := binary.BigEndian.Uint32(payload[magicLen:headerLen])
	totalLen := uint64(headerLen) + uint64(rawLen) + uint64(snapshotHashLen)
	if uint64(len(payload)) != totalLen {
		return nil, ErrBadSnapshot
	}

	raw := payload[headerLen : headerLen+int(rawLen)]
	digest := payload[headerLen+int(rawLen) : totalLen]
	expected := sha256.Sum256(raw)
	if !bytes.Equal(expected[:], digest) {
		return nil, ErrBadSnapshot
	}

	var dto snapshotDTO
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&dto); err != nil {
		return nil, ErrBadSnapshot
	}
	if dto.Version != snapshotVersion {
		return nil, ErrBadSnapshot
	}
	if !weightsValid(dto.Weights) {
		return nil, ErrBadSnapshot
	}

	for _, byConsumer := range dto.Samples {
		for consumer, record := range byConsumer {
			if !isKnownConsumer(consumer) {
				return nil, ErrBadSnapshot
			}
			if record.Passed > record.Total {
				return nil, ErrBadSnapshot
			}
		}
	}
	return &dto, nil
}
