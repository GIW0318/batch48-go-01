// Package task resolves the depth bias and the normal offset used when
// sampling cascaded shadow maps, keeping both values finite and bounded.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
)

// LightKind identifies the shadow projection of a light.
type LightKind string

// Light kinds understood by the resolver.
const (
	KindDirectional LightKind = "directional"
	KindSpot        LightKind = "spot"
	KindPoint       LightKind = "point"
)

// BiasCoefficients carries the per kind scaling factors.
type BiasCoefficients struct {
	Constant    float64
	Slope       float64
	Min         float64
	Max         float64
	NormalScale float64
}

// CombinedBias is the depth bias, the normal offset and their sum.
type CombinedBias struct {
	Bias         float64
	NormalOffset float64
	Total        float64
}

// ResolverConfig carries the global tuning and the per cascade texel scale.
type ResolverConfig struct {
	Constant        float64
	SlopeScale      float64
	MinBias         float64
	MaxBias         float64
	NormalScale     float64
	MaxNormalOffset float64
	CascadeScales   []float64
}

// ResolverStats reports the resolver counters.
type ResolverStats struct {
	Resolves   int
	Recomputes uint64
	Version    uint64
}

// Errors reported by the resolver.
var (
	ErrUnknownKind = errors.New("light kind is unknown")
	ErrBadLevel    = errors.New("cascade level is out of range")
	ErrBadSlope    = errors.New("slope is out of range")
	ErrZeroTexel   = errors.New("texel world size must be positive")
	ErrBadScale    = errors.New("cascade scale table is invalid")
	ErrBadSnapshot = errors.New("snapshot payload is invalid")
)

const (
	maxCascadeLevels = 8
	maxSlope         = 16.0

	snapshotMagic  = "gsb-shadow-bias-resolver"
	snapshotFormat = 1
)

var kindConstantScale = map[LightKind]float64{
	KindDirectional: 1.00,
	KindSpot:        0.75,
	KindPoint:       0.50,
}

type resolverCore struct {
	cfg    ResolverConfig
	scales []float64

	constant        float64
	slopeScale      float64
	normalScale     float64
	minBias         float64
	maxBias         float64
	maxNormalOffset float64
	version         uint64
	recomputes      uint64
}

// ShadowBiasResolver owns the bias configuration and the cascade scales.
type ShadowBiasResolver struct {
	initial  ResolverConfig
	current  atomic.Pointer[resolverCore]
	resolves atomic.Uint64
	updateMu sync.Mutex
}

// NewShadowBiasResolver builds a resolver with the given configuration.
func NewShadowBiasResolver(cfg ResolverConfig) *ShadowBiasResolver {
	r := &ShadowBiasResolver{initial: cloneConfig(cfg)}
	r.current.Store(newResolverCore(cfg, cfg.CascadeScales, 0, 0))
	return r
}

// Bias computes the depth bias of one cascade for one light kind and slope.
func (r *ShadowBiasResolver) Bias(level int, kind LightKind, slope float64) (float64, error) {
	core := r.current.Load()
	if level < 0 || level >= len(core.scales) {
		return 0, ErrBadLevel
	}

	kindScale, ok := kindConstantScale[kind]
	if !ok {
		return 0, ErrUnknownKind
	}
	if err := validateSlope(slope); err != nil {
		return 0, err
	}

	texelScale := core.scales[level]
	if !isFinite(texelScale) || texelScale <= 0 {
		return 0, ErrZeroTexel
	}

	constantPart := safeMul(core.constant, kindScale)
	slopePart := safeMul(core.slopeScale, slope)
	rawBias := safeMul(safeAdd(constantPart, slopePart), texelScale)
	bias := clampRange(rawBias, core.minBias, core.maxBias)

	r.resolves.Add(1)
	return bias, nil
}

// NormalOffset computes the world space normal offset from a texel size.
func (r *ShadowBiasResolver) NormalOffset(texelWorld, slope float64) float64 {
	return normalOffsetFor(r.current.Load(), texelWorld, slope)
}

// NormalOffsetChecked is NormalOffset with explicit input validation.
func (r *ShadowBiasResolver) NormalOffsetChecked(texelWorld, slope float64) (float64, error) {
	core := r.current.Load()
	if !isFinite(texelWorld) || texelWorld <= 0 {
		return 0, ErrZeroTexel
	}
	if err := validateSlope(slope); err != nil {
		return 0, err
	}
	return normalOffsetFor(core, texelWorld, slope), nil
}

// Clamp bounds the depth bias and the normal offset independently.
func (r *ShadowBiasResolver) Clamp(bias, normalOffset float64) (float64, float64) {
	core := r.current.Load()
	return clampRange(bias, core.minBias, core.maxBias),
		clampRange(normalOffset, 0, core.maxNormalOffset)
}

// Combine reports both terms and their single addition total.
func (r *ShadowBiasResolver) Combine(bias, normalOffset float64) CombinedBias {
	bias = saturateFinite(bias)
	normalOffset = saturateFinite(normalOffset)
	return CombinedBias{
		Bias:         bias,
		NormalOffset: normalOffset,
		Total:        safeAdd(bias, normalOffset),
	}
}

// SetCascadeScale replaces the per cascade texel world sizes.
func (r *ShadowBiasResolver) SetCascadeScale(scales []float64) error {
	if len(scales) == 0 || len(scales) > maxCascadeLevels {
		return ErrBadScale
	}

	next := make([]float64, len(scales))
	for i, scale := range scales {
		if !isFinite(scale) || scale <= 0 {
			return ErrBadScale
		}
		next[i] = scale
	}

	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	old := r.current.Load()
	core := newResolverCore(old.cfg, next, incrementUint64(old.version), incrementUint64(old.recomputes))
	r.current.Store(core)
	return nil
}

// CascadeScale reports the texel world size of one cascade.
func (r *ShadowBiasResolver) CascadeScale(level int) (float64, error) {
	core := r.current.Load()
	if level < 0 || level >= len(core.scales) {
		return 0, ErrBadLevel
	}
	return core.scales[level], nil
}

// Coefficients reports the bias coefficients of one light kind.
func (r *ShadowBiasResolver) Coefficients(kind LightKind) (BiasCoefficients, error) {
	if _, ok := kindConstantScale[kind]; !ok {
		return BiasCoefficients{}, ErrUnknownKind
	}
	return coefficientsFor(r.current.Load(), kind), nil
}

// Version reports the configuration and scale table version.
func (r *ShadowBiasResolver) Version() uint64 {
	return r.current.Load().version
}

// Stats reports the resolver counters.
func (r *ShadowBiasResolver) Stats() ResolverStats {
	core := r.current.Load()
	return ResolverStats{
		Resolves:   int(r.resolves.Load()),
		Recomputes: core.recomputes,
		Version:    core.version,
	}
}

// Reset restores the configuration captured at construction time.
func (r *ShadowBiasResolver) Reset() error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	old := r.current.Load()
	core := newResolverCore(
		r.initial,
		r.initial.CascadeScales,
		incrementUint64(old.version),
		incrementUint64(old.recomputes),
	)
	r.current.Store(core)
	return nil
}

// Snapshot serialises the configuration and the cascade scales.
func (r *ShadowBiasResolver) Snapshot() ([]byte, error) {
	core := r.current.Load()
	cfg := core.cfg
	cfg.CascadeScales = nil

	payload := snapshotData{
		Magic:      snapshotMagic,
		Format:     snapshotFormat,
		Version:    core.version,
		Recomputes: core.recomputes,
		Resolves:   r.resolves.Load(),
		Config:     cfg,
		Scales:     cloneFloats(core.scales),
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	if err := encoder.Encode(&payload); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Restore rebuilds the resolver from a snapshot, either completely or not at all.
func (r *ShadowBiasResolver) Restore(payload []byte) error {
	var data snapshotData
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return ErrBadSnapshot
	}

	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrBadSnapshot
	}
	if data.Magic != snapshotMagic || data.Format != snapshotFormat {
		return ErrBadSnapshot
	}
	if len(data.Scales) == 0 || len(data.Scales) > maxCascadeLevels {
		return ErrBadSnapshot
	}
	for _, scale := range data.Scales {
		if !isFinite(scale) {
			return ErrBadSnapshot
		}
	}
	if data.Resolves > uint64(math.MaxInt) {
		return ErrBadSnapshot
	}

	cfg := cloneConfig(data.Config)
	core := newResolverCore(cfg, data.Scales, data.Version, data.Recomputes)

	r.updateMu.Lock()
	r.current.Store(core)
	r.resolves.Store(data.Resolves)
	r.updateMu.Unlock()
	return nil
}

type snapshotData struct {
	Magic      string         `json:"magic"`
	Format     int            `json:"format"`
	Version    uint64         `json:"version"`
	Recomputes uint64         `json:"recomputes"`
	Resolves   uint64         `json:"resolves"`
	Config     ResolverConfig `json:"config"`
	Scales     []float64      `json:"scales"`
}

func newResolverCore(cfg ResolverConfig, scales []float64, version, recomputes uint64) *resolverCore {
	cfg = sanitizeConfig(cfg)
	scales = cloneFloats(scales)
	if len(scales) > maxCascadeLevels {
		scales = scales[:maxCascadeLevels]
	}
	for i, scale := range scales {
		if !isFinite(scale) {
			scales[i] = 0
		}
	}
	cfg.CascadeScales = scales

	minBias := math.Max(0, cfg.MinBias)
	maxBias := cfg.MaxBias
	if maxBias < minBias {
		maxBias = minBias
	}

	return &resolverCore{
		cfg:             cfg,
		scales:          scales,
		constant:        math.Abs(cfg.Constant),
		slopeScale:      math.Abs(cfg.SlopeScale),
		normalScale:     math.Abs(cfg.NormalScale),
		minBias:         minBias,
		maxBias:         maxBias,
		maxNormalOffset: math.Max(0, cfg.MaxNormalOffset),
		version:         version,
		recomputes:      recomputes,
	}
}

func sanitizeConfig(cfg ResolverConfig) ResolverConfig {
	cfg.Constant = finiteOr(cfg.Constant, 0)
	cfg.SlopeScale = finiteOr(cfg.SlopeScale, 0)
	cfg.MinBias = finiteOr(cfg.MinBias, 0)
	cfg.MaxBias = finiteOr(cfg.MaxBias, math.MaxFloat64)
	cfg.NormalScale = finiteOr(cfg.NormalScale, 0)
	cfg.MaxNormalOffset = finiteOr(cfg.MaxNormalOffset, math.MaxFloat64)
	return cfg
}

func coefficientsFor(core *resolverCore, kind LightKind) BiasCoefficients {
	scale := kindConstantScale[kind]
	return BiasCoefficients{
		Constant:    safeMul(core.constant, scale),
		Slope:       core.slopeScale,
		Min:         core.minBias,
		Max:         core.maxBias,
		NormalScale: core.normalScale,
	}
}

func normalOffsetFor(core *resolverCore, texelWorld, slope float64) float64 {
	return safeMul(texelWorld, core.normalScale, safeAdd(1, slope))
}

func validateSlope(slope float64) error {
	if !isFinite(slope) || slope < 0 || slope > maxSlope {
		return ErrBadSlope
	}
	return nil
}

func clampRange(value, low, high float64) float64 {
	switch {
	case math.IsNaN(value):
		return low
	case value <= low:
		return low
	case value >= high:
		return high
	default:
		return value
	}
}

func cloneConfig(cfg ResolverConfig) ResolverConfig {
	cfg.CascadeScales = cloneFloats(cfg.CascadeScales)
	return cfg
}

func cloneFloats(values []float64) []float64 {
	if values == nil {
		return nil
	}
	cloned := make([]float64, len(values))
	copy(cloned, values)
	return cloned
}

func incrementUint64(value uint64) uint64 {
	if value == math.MaxUint64 {
		return value
	}
	return value + 1
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteOr(value, fallback float64) float64 {
	if !isFinite(value) {
		return fallback
	}
	return value
}

func saturateFinite(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	if math.IsInf(value, 1) {
		return math.MaxFloat64
	}
	if math.IsInf(value, -1) {
		return -math.MaxFloat64
	}
	return value
}

func safeAdd(left, right float64) float64 {
	result := left + right
	if math.IsNaN(result) {
		return 0
	}
	return saturateFinite(result)
}

func safeMul(values ...float64) float64 {
	sign := 1.0
	for _, value := range values {
		if math.IsNaN(value) {
			return 0
		}
		if value == 0 {
			return 0
		}
		if value < 0 || math.IsInf(value, -1) {
			sign = -sign
		}
	}

	product := 1.0
	for _, value := range values {
		absolute := math.Abs(value)
		if math.IsInf(absolute, 1) || product > math.MaxFloat64/absolute {
			return sign * math.MaxFloat64
		}
		product *= absolute
	}
	return sign * product
}
