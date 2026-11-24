// Package task compiles a set of switchable post processing effects into one
// ordered pass chain, allocates ping pong temporary targets and keeps history
// buffers for effects that need the previous frame.
package task

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
)

// Effect identifies one post processing effect.
type Effect string

// Effects known to the compiler.
const (
	EffectMotionBlur Effect = "motion_blur"
	EffectSSAO       Effect = "ssao"
	EffectBloom      Effect = "bloom"
	EffectTonemap    Effect = "tonemap"
	EffectFXAA       Effect = "fxaa"
)

// Render target formats understood by the compiler.
const (
	FormatRGBA8     = "rgba8"
	FormatRGBA16F   = "rgba16f"
	FormatR11G11B10 = "r11g11b10"
)

// Pass kinds produced by Compile.
const (
	PassEffect  = "effect"
	PassConvert = "convert"
)

// Target kinds produced by Compile.
const (
	TargetTemp    = "temp"
	TargetHistory = "history"
)

const (
	externalTarget = "external"
	tempTarget0    = "temp0"
	tempTarget1    = "temp1"
)

// effectPriority is the fixed, lowest-to-highest ordering of all effects.
var effectPriority = [...]Effect{
	EffectMotionBlur,
	EffectSSAO,
	EffectBloom,
	EffectTonemap,
	EffectFXAA,
}

// Params carries the per effect settings.
type Params struct {
	Scale   float64
	In      string
	Out     string
	History bool
}

// Pass is one step of a compiled chain.
type Pass struct {
	Effect Effect
	Kind   string
	Input  string
	Output string
	Width  int
	Height int
}

// Target is one transient or history render target.
type Target struct {
	Name   string
	Kind   string
	Width  int
	Height int
}

// Chain is the result of one Compile.
type Chain struct {
	Width   int
	Height  int
	Passes  []Pass
	Targets []Target
	Version uint64
}

// Errors reported by the compiler.
var (
	ErrUnknownEffect = errors.New("effect is unknown")
	ErrNotEnabled    = errors.New("effect is not enabled")
	ErrBadScale      = errors.New("scale must be in the open interval zero to one")
	ErrBadFormat     = errors.New("format is unknown")
	ErrBadSize       = errors.New("render size must be positive")
	ErrEmptyChain    = errors.New("no effect is enabled")
	ErrPassLimit     = errors.New("effect count exceeds the pass limit")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// PostFXChainCompiler owns the enabled effect set and the compiled chain.
type PostFXChainCompiler struct {
	mu        sync.Mutex
	effects   map[Effect]Params
	history   map[Effect]bool
	maxPasses int
	width     int
	height    int
	version   uint64
	chain     Chain
	compiled  bool
}

// NewPostFXChainCompiler builds an empty compiler.
func NewPostFXChainCompiler() *PostFXChainCompiler {
	return &PostFXChainCompiler{
		effects:   make(map[Effect]Params),
		history:   make(map[Effect]bool),
		maxPasses: 8,
	}
}

// Enable switches one effect on and records its parameters.
func (c *PostFXChainCompiler) Enable(effect Effect, params Params) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !knownEffect(effect) {
		return ErrUnknownEffect
	}
	if err := validateParams(params); err != nil {
		return err
	}
	if c.effects == nil {
		c.effects = make(map[Effect]Params)
	}
	if c.history == nil {
		c.history = make(map[Effect]bool)
	}

	old, existed := c.effects[effect]
	c.effects[effect] = params

	if !existed {
		c.version++
	}
	if !existed || old != params {
		if params.History {
			c.history[effect] = false
		} else {
			delete(c.history, effect)
		}
		if existed {
			c.version++
		}
	} else if params.History {
		if _, ok := c.history[effect]; !ok {
			c.history[effect] = false
		}
	}
	return nil
}

// Disable switches one effect off.
func (c *PostFXChainCompiler) Disable(effect Effect) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.effects[effect]; !ok {
		return ErrNotEnabled
	}
	delete(c.effects, effect)
	delete(c.history, effect)
	c.version++
	return nil
}

// SetScale changes the resolution scale of one enabled effect.
func (c *PostFXChainCompiler) SetScale(effect Effect, scale float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	params, ok := c.effects[effect]
	if !ok {
		return ErrNotEnabled
	}
	if !validScale(scale) {
		return ErrBadScale
	}
	if params.Scale == scale {
		return nil
	}

	params.Scale = scale
	c.effects[effect] = params
	c.version++
	if params.History {
		c.history[effect] = false
	}
	return nil
}

// Enabled lists the enabled effects in fixed priority order.
func (c *PostFXChainCompiler) Enabled() []Effect {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Effect, 0, len(c.effects))
	for _, effect := range effectPriority {
		if _, ok := c.effects[effect]; ok {
			out = append(out, effect)
		}
	}
	return out
}

// ParamsOf reports the parameters recorded for one enabled effect.
func (c *PostFXChainCompiler) ParamsOf(effect Effect) (Params, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	params, ok := c.effects[effect]
	return params, ok
}

// SetMaxPasses changes how many effect passes one chain may hold.
func (c *PostFXChainCompiler) SetMaxPasses(n int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if n <= 0 {
		return ErrPassLimit
	}
	if n != c.maxPasses {
		c.maxPasses = n
		c.version++
	}
	return nil
}

// Compile builds the ordered pass chain for one render size.
func (c *PostFXChainCompiler) Compile(w, h int) (Chain, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if w <= 0 || h <= 0 {
		return Chain{}, ErrBadSize
	}

	chain, err := buildChain(c.effects, w, h, c.maxPasses)
	if err != nil {
		return Chain{}, err
	}

	if !c.compiled || c.width != w || c.height != h {
		c.version++
	}
	chain.Version = c.version

	c.chain = cloneChain(chain)
	c.compiled = true
	c.width = w
	c.height = h
	c.history = make(map[Effect]bool, len(c.effects))
	for effect, params := range c.effects {
		if params.History {
			c.history[effect] = true
		}
	}
	return cloneChain(chain), nil
}

// Resize records the render size used by the next Compile.
func (c *PostFXChainCompiler) Resize(w, h int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if w <= 0 || h <= 0 {
		return ErrBadSize
	}
	if c.width == w && c.height == h {
		return nil
	}

	c.width = w
	c.height = h
	c.version++
	c.invalidateHistoryLocked()
	return nil
}

// Size reports the render size recorded by Resize or the last Compile.
func (c *PostFXChainCompiler) Size() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.width, c.height
}

// InvalidateHistory drops every history buffer and marks history stale.
func (c *PostFXChainCompiler) InvalidateHistory(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	changed := c.invalidateHistoryLocked()
	if changed {
		c.version++
	}
}

// HistoryValid reports whether the history buffers of one effect are usable.
func (c *PostFXChainCompiler) HistoryValid(effect Effect) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	params, ok := c.effects[effect]
	if !ok || !params.History {
		return false
	}
	return c.history[effect]
}

// LastChain reports the chain produced by the last successful Compile.
func (c *PostFXChainCompiler) LastChain() (Chain, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.compiled {
		return Chain{}, false
	}
	return cloneChain(c.chain), true
}

// Snapshot serialises the effect set, the limits and the compiled chain.
func (c *PostFXChainCompiler) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	effects := make([]snapshotEffect, 0, len(c.effects))
	for _, effect := range effectPriority {
		params, ok := c.effects[effect]
		if !ok {
			continue
		}
		effects = append(effects, snapshotEffect{
			Effect:  effect,
			Scale:   params.Scale,
			In:      params.In,
			Out:     params.Out,
			History: params.History,
		})
	}

	history := make(map[string]bool, len(c.history))
	for effect, valid := range c.history {
		history[string(effect)] = valid
	}

	chain := snapshotFromChain(c.chain)
	maxPasses := c.maxPasses
	width, height := c.width, c.height
	version := c.version
	compiled := c.compiled
	payload := snapshotPayload{
		Magic:     snapshotMagic,
		Schema:    snapshotSchema,
		MaxPasses: &maxPasses,
		Width:     &width,
		Height:    &height,
		Effects:   &effects,
		History:   &history,
		Version:   &version,
		Compiled:  &compiled,
		Chain:     &chain,
	}
	return json.Marshal(payload)
}

// Restore rebuilds the compiler from a snapshot, either completely or not at all.
func (c *PostFXChainCompiler) Restore(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(payload) == 0 {
		return ErrBadSnapshot
	}

	var data snapshotPayload
	if err := json.Unmarshal(payload, &data); err != nil {
		return ErrBadSnapshot
	}
	if data.Magic != snapshotMagic || data.Schema != snapshotSchema ||
		data.MaxPasses == nil || data.Width == nil || data.Height == nil ||
		data.Effects == nil || data.History == nil || data.Version == nil ||
		data.Compiled == nil || data.Chain == nil {
		return ErrBadSnapshot
	}
	if *data.MaxPasses <= 0 {
		return ErrBadSnapshot
	}
	if (*data.Width == 0) != (*data.Height == 0) || *data.Width < 0 || *data.Height < 0 {
		return ErrBadSnapshot
	}
	if *data.Compiled && (*data.Width <= 0 || *data.Height <= 0) {
		return ErrBadSnapshot
	}

	effects := make(map[Effect]Params, len(*data.Effects))
	for _, item := range *data.Effects {
		if !knownEffect(item.Effect) {
			return ErrBadSnapshot
		}
		if _, exists := effects[item.Effect]; exists {
			return ErrBadSnapshot
		}
		params := Params{
			Scale:   item.Scale,
			In:      item.In,
			Out:     item.Out,
			History: item.History,
		}
		if err := validateParams(params); err != nil {
			return ErrBadSnapshot
		}
		effects[item.Effect] = params
	}

	history := make(map[Effect]bool, len(*data.History))
	for name, valid := range *data.History {
		effect := Effect(name)
		params, ok := effects[effect]
		if !ok || !params.History {
			return ErrBadSnapshot
		}
		if valid && !*data.Compiled {
			return ErrBadSnapshot
		}
		history[effect] = valid
	}
	for effect, params := range effects {
		if !params.History {
			continue
		}
		if _, ok := history[effect]; !ok {
			return ErrBadSnapshot
		}
	}

	chain := chainFromSnapshot(*data.Chain)
	if err := validateSnapshotChain(chain, *data.Compiled); err != nil {
		return ErrBadSnapshot
	}

	c.effects = effects
	c.history = history
	c.maxPasses = *data.MaxPasses
	c.width = *data.Width
	c.height = *data.Height
	c.version = *data.Version
	c.chain = chain
	c.compiled = *data.Compiled
	return nil
}

func knownEffect(effect Effect) bool {
	for _, candidate := range effectPriority {
		if effect == candidate {
			return true
		}
	}
	return false
}

func validFormat(format string) bool {
	switch format {
	case FormatRGBA8, FormatRGBA16F, FormatR11G11B10:
		return true
	default:
		return false
	}
}

func validScale(scale float64) bool {
	return !math.IsNaN(scale) && !math.IsInf(scale, 0) && scale > 0 && scale <= 1
}

func validateParams(params Params) error {
	if !validScale(params.Scale) {
		return ErrBadScale
	}
	if !validFormat(params.In) || !validFormat(params.Out) {
		return ErrBadFormat
	}
	return nil
}

func scaledDimension(value int, scale float64) int {
	scaled := int(math.Ceil(float64(value) * scale))
	if scaled < 1 {
		return 1
	}
	return scaled
}

func orderedEffects(effects map[Effect]Params) []Effect {
	ordered := make([]Effect, 0, len(effects))
	for _, effect := range effectPriority {
		if _, ok := effects[effect]; ok {
			ordered = append(ordered, effect)
		}
	}
	return ordered
}

func buildChain(effects map[Effect]Params, w, h, maxPasses int) (Chain, error) {
	ordered := orderedEffects(effects)
	if len(ordered) == 0 {
		return Chain{}, ErrEmptyChain
	}

	passes := make([]Pass, 0, len(ordered)*2)
	upstreamFormat := ""
	upstreamWidth, upstreamHeight := w, h

	for index, effect := range ordered {
		params := effects[effect]
		width := scaledDimension(w, params.Scale)
		height := scaledDimension(h, params.Scale)

		if index > 0 && upstreamFormat != params.In {
			passes = append(passes, Pass{
				Kind:   PassConvert,
				Input:  upstreamFormat,
				Output: params.In,
				Width:  upstreamWidth,
				Height: upstreamHeight,
			})
		}

		input := externalTarget
		if index > 0 {
			input = params.In
		}
		passes = append(passes, Pass{
			Effect: effect,
			Kind:   PassEffect,
			Input:  input,
			Width:  width,
			Height: height,
		})

		upstreamFormat = params.Out
		upstreamWidth, upstreamHeight = width, height
	}

	if len(passes) > maxPasses {
		return Chain{}, ErrPassLimit
	}

	for index := range passes {
		if passes[index].Kind != PassEffect {
			continue
		}
		if index == len(passes)-1 {
			passes[index].Output = externalTarget
			continue
		}
		if index%2 == 0 {
			passes[index].Output = tempTarget0
		} else {
			passes[index].Output = tempTarget1
		}
	}

	var tempWidth [2]int
	var tempHeight [2]int
	var tempUsed [2]bool
	for index := 0; index < len(passes)-1; index++ {
		slot := index % 2
		tempUsed[slot] = true
		if passes[index].Width > tempWidth[slot] {
			tempWidth[slot] = passes[index].Width
		}
		if passes[index].Height > tempHeight[slot] {
			tempHeight[slot] = passes[index].Height
		}
	}

	targets := make([]Target, 0, 2+2*len(ordered))
	for slot := 0; slot < 2; slot++ {
		if !tempUsed[slot] {
			continue
		}
		name := tempTarget0
		if slot == 1 {
			name = tempTarget1
		}
		targets = append(targets, Target{
			Name:   name,
			Kind:   TargetTemp,
			Width:  tempWidth[slot],
			Height: tempHeight[slot],
		})
	}
	for _, effect := range ordered {
		params := effects[effect]
		if !params.History {
			continue
		}
		width := scaledDimension(w, params.Scale)
		height := scaledDimension(h, params.Scale)
		for buffer := 0; buffer < 2; buffer++ {
			targets = append(targets, Target{
				Name:   historyTargetName(effect, buffer),
				Kind:   TargetHistory,
				Width:  width,
				Height: height,
			})
		}
	}

	return Chain{
		Width:   w,
		Height:  h,
		Passes:  passes,
		Targets: targets,
	}, nil
}

func historyTargetName(effect Effect, buffer int) string {
	if buffer == 0 {
		return string(effect) + "_history0"
	}
	return string(effect) + "_history1"
}

func cloneChain(chain Chain) Chain {
	out := chain
	out.Passes = append([]Pass(nil), chain.Passes...)
	out.Targets = append([]Target(nil), chain.Targets...)
	return out
}

func (c *PostFXChainCompiler) invalidateHistoryLocked() bool {
	changed := false
	for effect, params := range c.effects {
		if !params.History {
			delete(c.history, effect)
			continue
		}
		if c.history[effect] {
			changed = true
		}
		c.history[effect] = false
	}
	for effect := range c.history {
		if params, ok := c.effects[effect]; !ok || !params.History {
			delete(c.history, effect)
		}
	}
	return changed
}

const (
	snapshotMagic  = "postfx-chain-compiler"
	snapshotSchema = 1
)

type snapshotEffect struct {
	Effect  Effect  `json:"effect"`
	Scale   float64 `json:"scale"`
	In      string  `json:"in"`
	Out     string  `json:"out"`
	History bool    `json:"history"`
}

type snapshotChain struct {
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Passes  []Pass   `json:"passes"`
	Targets []Target `json:"targets"`
	Version uint64   `json:"version"`
}

type snapshotPayload struct {
	Magic     string            `json:"magic"`
	Schema    int               `json:"schema"`
	MaxPasses *int              `json:"max_passes"`
	Width     *int              `json:"width"`
	Height    *int              `json:"height"`
	Effects   *[]snapshotEffect `json:"effects"`
	History   *map[string]bool  `json:"history"`
	Version   *uint64           `json:"version"`
	Compiled  *bool             `json:"compiled"`
	Chain     *snapshotChain    `json:"chain"`
}

func snapshotFromChain(chain Chain) snapshotChain {
	passes := append([]Pass(nil), chain.Passes...)
	if passes == nil {
		passes = make([]Pass, 0)
	}
	targets := append([]Target(nil), chain.Targets...)
	if targets == nil {
		targets = make([]Target, 0)
	}
	return snapshotChain{
		Width:   chain.Width,
		Height:  chain.Height,
		Passes:  passes,
		Targets: targets,
		Version: chain.Version,
	}
}

func chainFromSnapshot(chain snapshotChain) Chain {
	return Chain{
		Width:   chain.Width,
		Height:  chain.Height,
		Passes:  append([]Pass(nil), chain.Passes...),
		Targets: append([]Target(nil), chain.Targets...),
		Version: chain.Version,
	}
}

func validateSnapshotChain(chain Chain, compiled bool) error {
	if !compiled {
		if len(chain.Passes) != 0 || len(chain.Targets) != 0 {
			return ErrBadSnapshot
		}
		return nil
	}
	if chain.Width <= 0 || chain.Height <= 0 || len(chain.Passes) == 0 {
		return ErrBadSnapshot
	}

	effectSeen := make(map[Effect]bool)
	targetNames := make(map[string]bool)
	tempNames := make(map[string]bool)
	tempCount := 0
	historyCount := 0

	for index, pass := range chain.Passes {
		if pass.Width <= 0 || pass.Height <= 0 || pass.Input == "" || pass.Output == "" {
			return ErrBadSnapshot
		}
		switch pass.Kind {
		case PassEffect:
			if !knownEffect(pass.Effect) || effectSeen[pass.Effect] {
				return ErrBadSnapshot
			}
			effectSeen[pass.Effect] = true
			if pass.Input != externalTarget && !validFormat(pass.Input) {
				return ErrBadSnapshot
			}
		case PassConvert:
			if !validFormat(pass.Input) || !validFormat(pass.Output) {
				return ErrBadSnapshot
			}
		default:
			return ErrBadSnapshot
		}
		if index == 0 && pass.Input != externalTarget {
			return ErrBadSnapshot
		}
		if index == len(chain.Passes)-1 {
			if pass.Output != externalTarget {
				return ErrBadSnapshot
			}
		} else if pass.Kind == PassEffect && pass.Output == externalTarget {
			return ErrBadSnapshot
		}
	}

	for _, target := range chain.Targets {
		if target.Name == "" || target.Width <= 0 || target.Height <= 0 || targetNames[target.Name] {
			return ErrBadSnapshot
		}
		targetNames[target.Name] = true
		switch target.Kind {
		case TargetTemp:
			tempCount++
			if tempCount > 2 {
				return ErrBadSnapshot
			}
			tempNames[target.Name] = true
		case TargetHistory:
			historyCount++
		default:
			return ErrBadSnapshot
		}
	}
	if historyCount%2 != 0 {
		return ErrBadSnapshot
	}

	for index, pass := range chain.Passes {
		if pass.Kind != PassEffect || index == len(chain.Passes)-1 {
			continue
		}
		name := tempTarget0
		if index%2 == 1 {
			name = tempTarget1
		}
		if pass.Output != name || !tempNames[name] {
			return ErrBadSnapshot
		}
	}
	expectedTemps := len(chain.Passes) - 1
	if expectedTemps > 2 {
		expectedTemps = 2
	}
	if tempCount != expectedTemps {
		return ErrBadSnapshot
	}
	return nil
}
