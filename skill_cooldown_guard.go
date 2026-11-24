package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
)

// Sentinels reported by the skill cooldown guard.
var (
	errNotImplemented = errors.New("not implemented")

	ErrInvalidSkillDef = errors.New("invalid skill definition")
	ErrDuplicateSkill  = errors.New("skill already defined")
	ErrUnknownSkill    = errors.New("skill not defined")
	ErrNotReady        = errors.New("skill is not ready")
	ErrGroupCooling    = errors.New("shared cooldown group is cooling")
	ErrInvalidReduce   = errors.New("invalid cooldown reduction")
	ErrBadSnapshot     = errors.New("snapshot payload rejected")
)

// Options carries the hard limits of the cooldown system.
type Options struct {
	MaxReducePct    int
	GroupCooldownMs int64
}

// SkillDef is one registered skill.
type SkillDef struct {
	Name       string
	CooldownMs int64
	Charges    int
	RechargeMs int64
	Group      string
}

// CastResult describes one accepted cast.
type CastResult struct {
	Skill        string
	At           int64
	Charges      int
	ChargesMax   int
	CooldownEnds int64
	Group        string
}

// skillState is the complete persisted state of one skill. charges are the
// number of charges at chargeAt; stepMs is the recharge interval of the cycle
// currently in progress. Freezing the step here means changing the reduction
// percentage never rewrites a cooldown that has already been committed.
type skillState struct {
	spec        SkillDef
	charges     int
	chargeAt    int64
	stepMs      int64
	reducePct   int
	cooldownEnd int64
}

type groupState struct {
	readyAt int64
}

// SkillCooldownGuard keeps the cooldown timers of every skill of one character.
type SkillCooldownGuard struct {
	mu     sync.Mutex
	opts   Options
	skills map[string]*skillState
	groups map[string]*groupState
	casts  int
}

// NewSkillCooldownGuard builds a guard with the given limits.
func NewSkillCooldownGuard(opts Options) *SkillCooldownGuard {
	return &SkillCooldownGuard{
		opts:   normalizeOptions(opts),
		skills: make(map[string]*skillState),
		groups: make(map[string]*groupState),
	}
}

func normalizeOptions(opts Options) Options {
	if opts.MaxReducePct < 0 {
		opts.MaxReducePct = 0
	}
	if opts.GroupCooldownMs < 0 {
		opts.GroupCooldownMs = 0
	}
	return opts
}

func validateSkillDef(spec SkillDef) error {
	if strings.TrimSpace(spec.Name) == "" || spec.CooldownMs <= 0 || spec.Charges <= 0 {
		return ErrInvalidSkillDef
	}
	if spec.RechargeMs < 0 {
		return ErrInvalidSkillDef
	}
	if spec.Charges > 1 && spec.RechargeMs <= 0 {
		return ErrInvalidSkillDef
	}
	return nil
}

// Define registers a skill definition.
func (g *SkillCooldownGuard) Define(spec SkillDef) error {
	if err := validateSkillDef(spec); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.skills == nil {
		g.skills = make(map[string]*skillState)
	}
	if g.groups == nil {
		g.groups = make(map[string]*groupState)
	}
	if _, exists := g.skills[spec.Name]; exists {
		return ErrDuplicateSkill
	}
	g.skills[spec.Name] = &skillState{
		spec:    spec,
		charges: spec.Charges,
	}
	return nil
}

// Cast consumes one charge of the skill at the caller supplied millisecond.
func (g *SkillCooldownGuard) Cast(skill string, at int64) (CastResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return CastResult{}, ErrUnknownSkill
	}

	charges, anchor, step := materialize(st, at)
	if charges <= 0 {
		return CastResult{}, ErrNotReady
	}

	var group *groupState
	if st.spec.Group != "" {
		group = g.groups[st.spec.Group]
		if group != nil && group.readyAt > at {
			return CastResult{}, ErrGroupCooling
		}
	}

	// Everything below is the single commit for this cast. The preceding
	// checks do not mutate any state, so rejected casts leave the guard intact.
	wasFull := charges >= st.spec.Charges
	st.charges = charges - 1
	st.chargeAt = anchor
	st.stepMs = step
	if wasFull {
		st.chargeAt = at
		st.stepMs = g.rechargeStep(st)
	}

	st.cooldownEnd = addMillis(at, g.effectiveCooldown(st))

	end := st.cooldownEnd
	if st.spec.Group != "" {
		if group == nil {
			group = &groupState{}
			if g.groups == nil {
				g.groups = make(map[string]*groupState)
			}
			g.groups[st.spec.Group] = group
		}
		groupEnd := addMillis(at, g.opts.GroupCooldownMs)
		if groupEnd > group.readyAt {
			group.readyAt = groupEnd
		}
		if group.readyAt > end {
			end = group.readyAt
		}
	}

	g.casts++
	return CastResult{
		Skill:        skill,
		At:           at,
		Charges:      st.charges,
		ChargesMax:   st.spec.Charges,
		CooldownEnds: end,
		Group:        st.spec.Group,
	}, nil
}

// Ready reports whether the skill can be cast at the given millisecond.
func (g *SkillCooldownGuard) Ready(skill string, at int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return false
	}
	charges, _, _ := materialize(st, at)
	if charges <= 0 {
		return false
	}
	if st.spec.Group != "" {
		if group := g.groups[st.spec.Group]; group != nil && group.readyAt > at {
			return false
		}
	}
	return true
}

// Charges reports the current charge count at the given millisecond.
func (g *SkillCooldownGuard) Charges(skill string, at int64) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return 0
	}
	charges, _, _ := materialize(st, at)
	return charges
}

// Reduce accumulates a cooldown reduction percentage on one skill.
func (g *SkillCooldownGuard) Reduce(skill string, pct int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return ErrUnknownSkill
	}
	st.reducePct = clampReduction(st.reducePct, pct, g.opts.MaxReducePct)
	return nil
}

// Reset restores the skill to full charges and clears its personal cooldown.
func (g *SkillCooldownGuard) Reset(skill string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return ErrUnknownSkill
	}
	st.charges = st.spec.Charges
	st.chargeAt = 0
	st.stepMs = 0
	st.cooldownEnd = 0
	if st.spec.Group != "" {
		delete(g.groups, st.spec.Group)
	}
	return nil
}

// CooldownRemaining reports the milliseconds until the skill is castable again.
func (g *SkillCooldownGuard) CooldownRemaining(skill string, at int64) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.skills[skill]
	if !ok {
		return 0
	}
	return skillRemaining(st, at)
}

// GroupRemaining reports the milliseconds left on a shared cooldown group.
func (g *SkillCooldownGuard) GroupRemaining(group string, at int64) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.groups[group]
	if !ok {
		return 0
	}
	return millisUntil(st.readyAt, at)
}

// Skills returns every registered skill name in ascending order.
func (g *SkillCooldownGuard) Skills() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	names := make([]string, 0, len(g.skills))
	for name := range g.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// materialize computes the charges and recharge anchor at at without changing
// the stored state. Because the anchor is only advanced to the latest whole
// boundary (never all the way to at), the sub-step remainder survives any
// number of queries.
func materialize(st *skillState, at int64) (int, int64, int64) {
	charges := st.charges
	anchor := st.chargeAt
	step := st.stepMs

	if charges >= st.spec.Charges {
		return charges, anchor, step
	}
	if step <= 0 {
		if at < anchor {
			return charges, anchor, step
		}
		return st.spec.Charges, at, step
	}
	if at <= anchor {
		return charges, anchor, step
	}

	elapsed := positiveDiff(at, anchor)
	gained := elapsed / step
	if gained <= 0 {
		return charges, anchor, step
	}
	missing := int64(st.spec.Charges - charges)
	if gained >= missing {
		return st.spec.Charges, at, step
	}
	charges += int(gained)
	anchor = addMillis(anchor, gained*step)
	return charges, anchor, step
}

// skillRemaining is the larger of the next charge boundary and the personal
// cooldown produced by the most recent cast. A full skill with no personal
// cooldown reports zero.
func skillRemaining(st *skillState, at int64) int64 {
	charges, anchor, step := materialize(st, at)
	var next int64
	if charges < st.spec.Charges && step > 0 {
		next = addMillis(anchor, step)
	}
	return maxInt64(millisUntil(next, at), millisUntil(st.cooldownEnd, at))
}

func (g *SkillCooldownGuard) rechargeStep(st *skillState) int64 {
	if st.spec.Charges > 1 {
		return st.spec.RechargeMs
	}
	return g.effectiveCooldown(st)
}

func (g *SkillCooldownGuard) effectiveCooldown(st *skillState) int64 {
	cd := st.spec.CooldownMs
	pct := st.reducePct
	if pct <= 0 {
		return cd
	}
	if pct >= 100 {
		return 0
	}
	keep := int64(100 - pct)
	// Split the division to avoid overflowing on very large cooldowns.
	return (cd/100)*keep + (cd%100)*keep/100
}

func clampReduction(current, delta, limit int) int {
	if limit < 0 {
		limit = 0
	}
	if current < 0 {
		current = 0
	}
	if current > limit {
		current = limit
	}
	if delta > 0 {
		if delta > limit-current {
			return limit
		}
		return current + delta
	}
	if delta < 0 {
		if delta < -current {
			return 0
		}
		return current + delta
	}
	return current
}

func addMillis(base, delta int64) int64 {
	if delta > 0 && base > math.MaxInt64-delta {
		return math.MaxInt64
	}
	if delta < 0 && base < math.MinInt64-delta {
		return math.MinInt64
	}
	return base + delta
}

func positiveDiff(later, earlier int64) int64 {
	if later <= earlier {
		return 0
	}
	if earlier < 0 && later > math.MaxInt64+earlier {
		return math.MaxInt64
	}
	return later - earlier
}

func millisUntil(end, at int64) int64 {
	return positiveDiff(end, at)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

const snapshotVersion = 1

type snapshotOptions struct {
	MaxReducePct    int   `json:"max_reduce_pct"`
	GroupCooldownMs int64 `json:"group_cooldown_ms"`
}

type snapshotSkill struct {
	Name        string `json:"name"`
	CooldownMs  int64  `json:"cooldown_ms"`
	ChargesMax  int    `json:"charges_max"`
	RechargeMs  int64  `json:"recharge_ms"`
	Group       string `json:"group"`
	Charges     int    `json:"charges"`
	ChargeAt    int64  `json:"charge_at"`
	StepMs      int64  `json:"step_ms"`
	ReducePct   int    `json:"reduce_pct"`
	CooldownEnd int64  `json:"cooldown_end"`
}

type snapshotGroup struct {
	Name    string `json:"name"`
	ReadyAt int64  `json:"ready_at"`
}

type snapshotPayload struct {
	Options snapshotOptions `json:"options"`
	Skills  []snapshotSkill `json:"skills"`
	Groups  []snapshotGroup `json:"groups"`
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises the guard state.
func (g *SkillCooldownGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload := snapshotPayload{
		Options: snapshotOptions{
			MaxReducePct:    g.opts.MaxReducePct,
			GroupCooldownMs: g.opts.GroupCooldownMs,
		},
		Skills: make([]snapshotSkill, 0, len(g.skills)),
		Groups: make([]snapshotGroup, 0, len(g.groups)),
	}
	for name, st := range g.skills {
		payload.Skills = append(payload.Skills, snapshotSkill{
			Name:        name,
			CooldownMs:  st.spec.CooldownMs,
			ChargesMax:  st.spec.Charges,
			RechargeMs:  st.spec.RechargeMs,
			Group:       st.spec.Group,
			Charges:     st.charges,
			ChargeAt:    st.chargeAt,
			StepMs:      st.stepMs,
			ReducePct:   st.reducePct,
			CooldownEnd: st.cooldownEnd,
		})
	}
	for name, group := range g.groups {
		payload.Groups = append(payload.Groups, snapshotGroup{
			Name:    name,
			ReadyAt: group.readyAt,
		})
	}
	sort.Slice(payload.Skills, func(i, j int) bool {
		return payload.Skills[i].Name < payload.Skills[j].Name
	})
	sort.Slice(payload.Groups, func(i, j int) bool {
		return payload.Groups[i].Name < payload.Groups[j].Name
	})

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(sum[:]),
		Payload:  raw,
	}
	return json.Marshal(envelope)
}

// Restore replaces the guard state from a snapshot payload.
func (g *SkillCooldownGuard) Restore(payload []byte) error {
	nextOpts, nextSkills, nextGroups, err := decodeSnapshot(payload)
	if err != nil {
		return ErrBadSnapshot
	}

	g.mu.Lock()
	g.opts = nextOpts
	g.skills = nextSkills
	g.groups = nextGroups
	g.mu.Unlock()
	return nil
}

func decodeSnapshot(payload []byte) (Options, map[string]*skillState, map[string]*groupState, error) {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return Options{}, nil, nil, ErrBadSnapshot
	}

	var envelope snapshotEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Options{}, nil, nil, ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion || len(envelope.Checksum) != 64 || len(envelope.Payload) == 0 {
		return Options{}, nil, nil, ErrBadSnapshot
	}
	sum := sha256.Sum256(envelope.Payload)
	want := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(want), []byte(envelope.Checksum)) != 1 {
		return Options{}, nil, nil, ErrBadSnapshot
	}

	var decoded snapshotPayload
	if err := json.Unmarshal(envelope.Payload, &decoded); err != nil {
		return Options{}, nil, nil, ErrBadSnapshot
	}
	if decoded.Options.MaxReducePct < 0 || decoded.Options.GroupCooldownMs < 0 {
		return Options{}, nil, nil, ErrBadSnapshot
	}
	opts := normalizeOptions(Options{
		MaxReducePct:    decoded.Options.MaxReducePct,
		GroupCooldownMs: decoded.Options.GroupCooldownMs,
	})

	skills := make(map[string]*skillState, len(decoded.Skills))
	for _, saved := range decoded.Skills {
		spec := SkillDef{
			Name:       saved.Name,
			CooldownMs: saved.CooldownMs,
			Charges:    saved.ChargesMax,
			RechargeMs: saved.RechargeMs,
			Group:      saved.Group,
		}
		if validateSkillDef(spec) != nil {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if _, exists := skills[spec.Name]; exists {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if saved.Charges < 0 || saved.Charges > spec.Charges {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if saved.StepMs < 0 {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if saved.Charges < spec.Charges && spec.Charges > 1 && saved.StepMs <= 0 {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if saved.ReducePct < 0 || saved.ReducePct > opts.MaxReducePct {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		skills[spec.Name] = &skillState{
			spec:        spec,
			charges:     saved.Charges,
			chargeAt:    saved.ChargeAt,
			stepMs:      saved.StepMs,
			reducePct:   saved.ReducePct,
			cooldownEnd: saved.CooldownEnd,
		}
	}

	groups := make(map[string]*groupState, len(decoded.Groups))
	for _, saved := range decoded.Groups {
		if strings.TrimSpace(saved.Name) == "" {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		if _, exists := groups[saved.Name]; exists {
			return Options{}, nil, nil, ErrBadSnapshot
		}
		groups[saved.Name] = &groupState{readyAt: saved.ReadyAt}
	}

	return opts, skills, groups, nil
}
