// Package task brokers occlusion query samples between several render
// consumers. Each frame hands every consumer a weighted slice of the sampling
// budget, turns passed/total ratios into visible, hidden or unknown verdicts
// and keeps a verdict conservative when consumers disagree.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// NewVisibilityConfidenceBroker builds a broker from one config.
func NewVisibilityConfidenceBroker(cfg Config) *VisibilityConfidenceBroker {
	return &VisibilityConfidenceBroker{
		quota:   cfg.SampleQuota,
		ttl:     cfg.TTLFrames,
		low:     cfg.Low,
		high:    cfg.High,
		weights: cfg.Weights,
		used:    make(map[Consumer]uint32),
		samples: make(map[ObjectID]map[Consumer]sample),
	}
}

// BeginFrame starts a new frame and refills every consumer quota.
func (b *VisibilityConfidenceBroker) BeginFrame(frame uint64) error {
	return errNotImplemented
}

// Frame reports the frame that is currently open.
func (b *VisibilityConfidenceBroker) Frame() uint64 {
	return 0
}

// Submit records one occlusion query result for a consumer.
func (b *VisibilityConfidenceBroker) Submit(objID ObjectID, consumer Consumer, passed, total uint32) error {
	return errNotImplemented
}

// Visible turns the recorded samples of one object into a verdict.
func (b *VisibilityConfidenceBroker) Visible(objID ObjectID, consumer Consumer) (Verdict, error) {
	return "", errNotImplemented
}

// SetThresholds replaces the low and high confidence thresholds.
func (b *VisibilityConfidenceBroker) SetThresholds(low, high float64) error {
	return errNotImplemented
}

// Thresholds reports the low and high confidence thresholds.
func (b *VisibilityConfidenceBroker) Thresholds() (float64, float64) {
	return 0, 0
}

// SetWeights replaces the per consumer budget weights.
func (b *VisibilityConfidenceBroker) SetWeights(weights map[Consumer]uint32) error {
	return errNotImplemented
}

// QuotaFor reports how many samples one consumer may submit this frame.
func (b *VisibilityConfidenceBroker) QuotaFor(consumer Consumer) uint32 {
	return 0
}

// SamplesLeft reports how many samples one consumer may still submit.
func (b *VisibilityConfidenceBroker) SamplesLeft(consumer Consumer) uint32 {
	return 0
}

// ConflictCount reports how many objects got disagreeing verdicts.
func (b *VisibilityConfidenceBroker) ConflictCount() int {
	return 0
}

// Invalidate drops every recorded sample of one object.
func (b *VisibilityConfidenceBroker) Invalidate(objID ObjectID) {
}

// Snapshot serialises the verdict table, the thresholds and the frame counter.
func (b *VisibilityConfidenceBroker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the broker from a snapshot, either completely or not at all.
func (b *VisibilityConfidenceBroker) Restore(payload []byte) error {
	return errNotImplemented
}
