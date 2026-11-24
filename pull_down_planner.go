package task

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/bits"
	"sort"
	"sync"
)

// Errors reported by the pull down planner.
var (
	ErrRateRejected    = errors.New("frame rate is not usable")
	ErrSourceUnderrun  = errors.New("source frame has not arrived yet")
	ErrPolicyRejected  = errors.New("playout policy is not usable")
	ErrFrameOrder      = errors.New("source frame index is not usable")
	ErrPlannerSnapshot = errors.New("pull down planner snapshot is not readable")
)

// Rational is an exact frame rate expressed as a fraction.
type Rational struct {
	Num int64
	Den int64
}

// PlayoutPolicy bounds how many repeats or drops are tolerated in a row.
type PlayoutPolicy struct {
	MaxRepeat int
	MaxDrop   int
}

// Decision is one output slot of the conversion.
type Decision struct {
	Output  int64
	Source  int64
	Repeat  bool
	Dropped int
}

// PlannerStats reports the counters kept by the planner.
type PlannerStats struct {
	Outputs    uint64
	Repeats    uint64
	Drops      uint64
	RepeatOver uint64
	DropOver   uint64
	Resets     uint64
}

const snapshotMagic = "pull-down-planner/v1"

// PullDownPlanner maps a source frame rate onto a target frame rate.
type PullDownPlanner struct {
	mu         sync.Mutex
	src        Rational
	dst        Rational
	policy     PlayoutPolicy
	step       int64 // src.Num * dst.Den, validated to fit int64
	modulus    int64 // src.Den * dst.Num, validated to fit int64
	qStep      int64 // step / modulus
	rStep      int64 // step % modulus
	phase      int64 // index of the next output slot
	quot       int64 // floor(phase * step / modulus)
	rem        int64 // (phase * step) % modulus
	base       int64 // source index that output slot 0 maps to
	frames     map[int64]struct{}
	lastPushed int64
	hasPushed  bool
	lastSource int64
	hasOutput  bool
	repeatRun  int
	outputs    uint64
	stats      PlannerStats
}

// mulFitsInt64 reports whether a*b fits into a signed 64 bit integer. Both
// operands must be positive.
func mulFitsInt64(a, b int64) bool {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	return hi == 0 && lo <= math.MaxInt64
}

// validRates reports whether both rates are positive and the cross products
// needed by the scheduler fit into signed 64 bit integers.
func validRates(src, dst Rational) bool {
	if src.Num <= 0 || src.Den <= 0 || dst.Num <= 0 || dst.Den <= 0 {
		return false
	}
	return mulFitsInt64(src.Num, dst.Den) && mulFitsInt64(src.Den, dst.Num)
}

// NewPullDownPlanner validates both rates and builds a planner.
func NewPullDownPlanner(src, dst Rational) (*PullDownPlanner, error) {
	if !validRates(src, dst) {
		return nil, ErrRateRejected
	}
	p := &PullDownPlanner{
		src:    src,
		dst:    dst,
		policy: PlayoutPolicy{MaxRepeat: 2, MaxDrop: 2},
		frames: make(map[int64]struct{}),
	}
	p.deriveSteps()
	return p, nil
}

// deriveSteps recomputes the integer step parameters from the rates. The
// caller must hold the lock and the rates must have been validated.
func (p *PullDownPlanner) deriveSteps() {
	p.step = p.src.Num * p.dst.Den
	p.modulus = p.src.Den * p.dst.Num
	p.qStep = p.step / p.modulus
	p.rStep = p.step % p.modulus
}

// restartLocked rewinds the output phase to slot zero. The caller must hold
// the lock.
func (p *PullDownPlanner) restartLocked() {
	p.phase = 0
	p.quot = 0
	p.rem = 0
	p.base = 0
	p.hasOutput = false
	p.repeatRun = 0
	p.lastSource = 0
}

// Configure replaces both the source and the target rate and resets the
// output phase.
func (p *PullDownPlanner) Configure(src, dst Rational) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validRates(src, dst) {
		return ErrRateRejected
	}
	p.src = src
	p.dst = dst
	p.deriveSteps()
	p.restartLocked()
	p.stats.Resets++
	return nil
}

// SetTargetRate replaces the target rate and resets the output phase.
func (p *PullDownPlanner) SetTargetRate(dst Rational) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validRates(p.src, dst) {
		return ErrRateRejected
	}
	p.dst = dst
	p.deriveSteps()
	p.restartLocked()
	p.stats.Resets++
	return nil
}

// PushFrame reports that one more source frame has arrived.
func (p *PullDownPlanner) PushFrame(index int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 {
		return ErrFrameOrder
	}
	if p.hasPushed && index < p.lastPushed {
		// The source rewound its frame numbering: treat it as a source
		// reset and restart the schedule from this frame.
		p.frames = map[int64]struct{}{index: {}}
		p.lastPushed = index
		p.restartLocked()
		p.base = index
		p.stats.Resets++
		return nil
	}
	if _, dup := p.frames[index]; dup {
		return nil
	}
	p.frames[index] = struct{}{}
	p.lastPushed = index
	p.hasPushed = true
	return nil
}

// Next produces the decision for the next output slot.
func (p *PullDownPlanner) Next() (Decision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	source := p.base + p.quot
	if _, ok := p.frames[source]; !ok {
		return Decision{}, ErrSourceUnderrun
	}
	dec := Decision{Output: p.phase, Source: source}
	if p.hasOutput {
		switch {
		case source == p.lastSource:
			dec.Repeat = true
			p.stats.Repeats++
			p.repeatRun++
			if p.repeatRun > p.policy.MaxRepeat {
				p.stats.RepeatOver++
			}
		default:
			p.repeatRun = 0
			if gap := source - p.lastSource; gap > 1 {
				dec.Dropped = int(gap - 1)
				p.stats.Drops += uint64(gap - 1)
				if dec.Dropped > p.policy.MaxDrop {
					p.stats.DropOver++
				}
			}
		}
	}
	p.lastSource = source
	p.hasOutput = true
	p.outputs = uint64(p.phase)
	p.stats.Outputs = p.outputs
	// Advance the phase with integer math only, keeping the remainder
	// bounded by the modulus so the error never grows with the output
	// count.
	p.phase++
	p.quot += p.qStep
	if p.rem >= p.modulus-p.rStep {
		p.rem -= p.modulus - p.rStep
		p.quot++
	} else {
		p.rem += p.rStep
	}
	return dec, nil
}

// SetPolicy replaces the repeat and drop tolerances.
func (p *PullDownPlanner) SetPolicy(policy PlayoutPolicy) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if policy.MaxRepeat < 0 || policy.MaxDrop < 0 {
		return ErrPolicyRejected
	}
	p.policy = policy
	return nil
}

// ExpectedOutputs reports how many output frames a source frame count turns
// into under the configured rates.
func (p *PullDownPlanner) ExpectedOutputs(sourceFrames int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sourceFrames <= 0 {
		return 0
	}
	num := new(big.Int).Mul(big.NewInt(sourceFrames), big.NewInt(p.dst.Num*p.src.Den))
	num.Quo(num, big.NewInt(p.dst.Den*p.src.Num))
	if !num.IsInt64() {
		return math.MaxInt64
	}
	return num.Int64()
}

// AccumulatedError reports the fractional part left in the source time base
// for the next output slot, in units of 1/src.Den of a source frame.
func (p *PullDownPlanner) AccumulatedError() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rem / p.dst.Num
}

// Phase reports the index of the next output slot.
func (p *PullDownPlanner) Phase() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase
}

// FramesAvailable reports how many source frames have been pushed.
func (p *PullDownPlanner) FramesAvailable() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return int64(len(p.frames))
}

// Reset drops every pushed frame and restarts the output phase.
func (p *PullDownPlanner) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frames = make(map[int64]struct{})
	p.hasPushed = false
	p.lastPushed = 0
	p.restartLocked()
	p.stats.Resets++
	return nil
}

// plannerSnapshot is the serialised form of the planner state.
type plannerSnapshot struct {
	Magic      string        `json:"magic"`
	Src        Rational      `json:"src"`
	Dst        Rational      `json:"dst"`
	Policy     PlayoutPolicy `json:"policy"`
	Phase      int64         `json:"phase"`
	Quot       int64         `json:"quot"`
	Rem        int64         `json:"rem"`
	Base       int64         `json:"base"`
	Frames     []int64       `json:"frames"`
	LastPushed int64         `json:"lastPushed"`
	HasPushed  bool          `json:"hasPushed"`
	LastSource int64         `json:"lastSource"`
	HasOutput  bool          `json:"hasOutput"`
	RepeatRun  int           `json:"repeatRun"`
	Outputs    uint64        `json:"outputs"`
	Stats      PlannerStats  `json:"stats"`
}

// Snapshot serialises the planner state.
func (p *PullDownPlanner) Snapshot() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	frames := make([]int64, 0, len(p.frames))
	for index := range p.frames {
		frames = append(frames, index)
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	snap := plannerSnapshot{
		Magic:      snapshotMagic,
		Src:        p.src,
		Dst:        p.dst,
		Policy:     p.policy,
		Phase:      p.phase,
		Quot:       p.quot,
		Rem:        p.rem,
		Base:       p.base,
		Frames:     frames,
		LastPushed: p.lastPushed,
		HasPushed:  p.hasPushed,
		LastSource: p.lastSource,
		HasOutput:  p.hasOutput,
		RepeatRun:  p.repeatRun,
		Outputs:    p.outputs,
		Stats:      p.stats,
	}
	return json.Marshal(snap)
}

// Restore replaces the planner state from a snapshot.
func (p *PullDownPlanner) Restore(data []byte) error {
	var snap plannerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrPlannerSnapshot
	}
	if snap.Magic != snapshotMagic || !validRates(snap.Src, snap.Dst) {
		return ErrPlannerSnapshot
	}
	if snap.Policy.MaxRepeat < 0 || snap.Policy.MaxDrop < 0 {
		return ErrPlannerSnapshot
	}
	modulus := snap.Src.Den * snap.Dst.Num
	if snap.Phase < 0 || snap.Quot < 0 || snap.Base < 0 ||
		snap.Rem < 0 || snap.Rem >= modulus || snap.RepeatRun < 0 {
		return ErrPlannerSnapshot
	}
	frames := make(map[int64]struct{}, len(snap.Frames))
	for _, index := range snap.Frames {
		if index < 0 {
			return ErrPlannerSnapshot
		}
		frames[index] = struct{}{}
	}
	if snap.HasPushed && snap.LastPushed < 0 {
		return ErrPlannerSnapshot
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.src = snap.Src
	p.dst = snap.Dst
	p.policy = snap.Policy
	p.deriveSteps()
	p.phase = snap.Phase
	p.quot = snap.Quot
	p.rem = snap.Rem
	p.base = snap.Base
	p.frames = frames
	p.lastPushed = snap.LastPushed
	p.hasPushed = snap.HasPushed
	p.lastSource = snap.LastSource
	p.hasOutput = snap.HasOutput
	p.repeatRun = snap.RepeatRun
	p.outputs = snap.Outputs
	p.stats = snap.Stats
	return nil
}

// Stats reports the counters kept by the planner.
func (p *PullDownPlanner) Stats() PlannerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}
