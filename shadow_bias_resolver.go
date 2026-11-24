// Package task resolves the depth bias and the normal offset used when
// sampling cascaded shadow maps, keeping both values finite and bounded.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// ShadowBiasResolver owns the bias configuration and the cascade scales.
type ShadowBiasResolver struct {
	mu  sync.RWMutex
	cfg ResolverConfig
}

// NewShadowBiasResolver builds a resolver with the given configuration.
func NewShadowBiasResolver(cfg ResolverConfig) *ShadowBiasResolver {
	return &ShadowBiasResolver{cfg: cfg}
}

// Bias computes the depth bias of one cascade for one light kind and slope.
func (r *ShadowBiasResolver) Bias(level int, kind LightKind, slope float64) (float64, error) {
	return 0, errNotImplemented
}

// NormalOffset computes the world space normal offset from a texel size.
func (r *ShadowBiasResolver) NormalOffset(texelWorld, slope float64) float64 {
	return 0
}

// NormalOffsetChecked is NormalOffset with explicit input validation.
func (r *ShadowBiasResolver) NormalOffsetChecked(texelWorld, slope float64) (float64, error) {
	return 0, errNotImplemented
}

// Clamp bounds the depth bias and the normal offset independently.
func (r *ShadowBiasResolver) Clamp(bias, normalOffset float64) (float64, float64) {
	return 0, 0
}

// Combine reports both terms and their single addition total.
func (r *ShadowBiasResolver) Combine(bias, normalOffset float64) CombinedBias {
	return CombinedBias{}
}

// SetCascadeScale replaces the per cascade texel world sizes.
func (r *ShadowBiasResolver) SetCascadeScale(scales []float64) error {
	return errNotImplemented
}

// CascadeScale reports the texel world size of one cascade.
func (r *ShadowBiasResolver) CascadeScale(level int) (float64, error) {
	return 0, errNotImplemented
}

// Coefficients reports the bias coefficients of one light kind.
func (r *ShadowBiasResolver) Coefficients(kind LightKind) (BiasCoefficients, error) {
	return BiasCoefficients{}, errNotImplemented
}

// Version reports the configuration and scale table version.
func (r *ShadowBiasResolver) Version() uint64 {
	return 0
}

// Stats reports the resolver counters.
func (r *ShadowBiasResolver) Stats() ResolverStats {
	return ResolverStats{}
}

// Reset restores the configuration captured at construction time.
func (r *ShadowBiasResolver) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the configuration and the cascade scales.
func (r *ShadowBiasResolver) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the resolver from a snapshot, either completely or not at all.
func (r *ShadowBiasResolver) Restore(payload []byte) error {
	return errNotImplemented
}
