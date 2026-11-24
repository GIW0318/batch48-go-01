// Package task fits cascaded shadow maps for a directional light: it splits
// the view range, builds one orthographic matrix per cascade, snaps the
// matrices to the shadow map texel grid and reports the blend bands.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

// Vec3 is a position or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Vec2 is a two component vector, used for texel sizes.
type Vec2 struct {
	X float64
	Y float64
}

// Mat4 is a column major four by four matrix.
type Mat4 [16]float64

// Camera describes the view that the cascades must cover.
type Camera struct {
	Position Vec3
	Yaw      float64
	Pitch    float64
	FovYDeg  float64
	Aspect   float64
	Near     float64
	Far      float64
}

// CascadeConfig carries the cascade count and the split weighting.
type CascadeConfig struct {
	Cascades      int
	Lambda        float64
	BlendFraction float64
	TexelSize     Vec2
}

// FitStats reports how often matrices were built or reused.
type FitStats struct {
	Fits          int
	CacheHits     int
	Invalidations uint64
	Version       uint64
}

// Errors reported by the solver.
var (
	ErrBadConfig     = errors.New("cascade configuration is invalid")
	ErrBadRange      = errors.New("near and far planes are out of range")
	ErrBadSplitCount = errors.New("manual split count does not match the cascade count")
	ErrNonMonotonic  = errors.New("manual split distances are not increasing")
	ErrBadCascade    = errors.New("cascade index is out of range")
	ErrBadCamera     = errors.New("camera parameters are invalid")
	ErrBadTexel      = errors.New("texel size must be positive")
	ErrBadLambda     = errors.New("split lambda must be in the closed interval zero to one")
	ErrBadBlend      = errors.New("blend fraction must be in the closed interval zero to one")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

const (
	snapshotMagic  = "gsb-shadow-cascade-fit-solver"
	snapshotSchema = 1
)

type lensSignature struct {
	near   float64
	far    float64
	fov    float64
	aspect float64
}

type fitKey struct {
	level  int
	camera Camera
}

type snapshotData struct {
	Magic         string        `json:"magic"`
	Schema        int           `json:"schema"`
	Version       uint64        `json:"version"`
	Config        CascadeConfig `json:"config"`
	Manual        []float64     `json:"manual_distances,omitempty"`
	Bounds        []float64     `json:"bounds,omitempty"`
	SplitNear     float64       `json:"split_near"`
	SplitFar      float64       `json:"split_far"`
	HasBounds     bool          `json:"has_bounds"`
	Fits          int           `json:"fits"`
	CacheHits     int           `json:"cache_hits"`
	Invalidations uint64        `json:"invalidations"`
	LastReason    string        `json:"last_reason,omitempty"`
}

// CascadeFitSolver owns the cascade configuration and the fitted matrix cache.
type CascadeFitSolver struct {
	mu sync.RWMutex

	cfg     CascadeConfig
	baseCfg CascadeConfig

	manual    []float64
	bounds    []float64
	splitNear float64
	splitFar  float64
	hasBounds bool

	cache   map[fitKey]Mat4
	lens    lensSignature
	hasLens bool

	fits          int
	cacheHits     int
	invalidations uint64
	version       uint64
	lastReason    string
}

// NewCascadeFitSolver builds a solver for the given cascade configuration.
func NewCascadeFitSolver(cfg CascadeConfig) *CascadeFitSolver {
	return &CascadeFitSolver{
		cfg:     cfg,
		baseCfg: cfg,
		cache:   make(map[fitKey]Mat4),
	}
}

// Split computes the cascade boundaries between near and far.
func (s *CascadeFitSolver) Split(distances []float64, near, far float64) ([]float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateConfig(s.cfg); err != nil {
		return nil, err
	}
	if !validRange(near, far) {
		return nil, ErrBadRange
	}

	count := s.cfg.Cascades
	if len(distances) > 0 && len(distances) != count-1 {
		return nil, ErrBadSplitCount
	}

	manual := cloneFloats(distances)
	if len(manual) > 0 {
		for _, distance := range manual {
			if !validManualDistance(distance, near, far) {
				return nil, ErrBadRange
			}
		}
		for i := 1; i < len(manual); i++ {
			if manual[i] <= manual[i-1] {
				return nil, ErrNonMonotonic
			}
		}
	}

	var bounds []float64
	if len(manual) > 0 {
		bounds = manualBounds(manual, near, far, count)
	} else {
		manual = nil
		bounds = automaticBounds(s.cfg, near, far)
	}

	changed := !sameFloats(s.manual, manual) ||
		!s.hasBounds || s.splitNear != near || s.splitFar != far ||
		!sameFloats(s.bounds, bounds)

	s.manual = manual
	s.bounds = cloneFloats(bounds)
	s.splitNear = near
	s.splitFar = far
	s.hasBounds = true
	if changed {
		s.invalidateLocked("split boundaries changed")
	}

	return bounds, nil
}

// Fit builds the orthographic matrix of one cascade for the camera.
func (s *CascadeFitSolver) Fit(camera Camera, level int) (Mat4, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateConfig(s.cfg); err != nil {
		return Mat4{}, err
	}
	if level < 0 || level >= s.cfg.Cascades {
		return Mat4{}, ErrBadCascade
	}
	if err := validateCamera(camera); err != nil {
		return Mat4{}, err
	}

	lens := lensSignature{
		near:   camera.Near,
		far:    camera.Far,
		fov:    camera.FovYDeg,
		aspect: camera.Aspect,
	}
	if s.hasLens && s.lens != lens {
		s.invalidateLocked("camera near, far, field of view, or aspect changed")
	}
	s.lens = lens
	s.hasLens = true

	bounds, err := s.boundsForCameraLocked(camera)
	if err != nil {
		return Mat4{}, err
	}

	key := fitKey{level: level, camera: camera}
	if matrix, ok := s.cache[key]; ok {
		s.cacheHits++
		return matrix, nil
	}

	matrix := buildCascadeMatrix(camera, bounds[level], bounds[level+1])
	if texelX := s.cfg.TexelSize.X; positiveFinite(texelX) {
		matrix[12] = snapScalar(matrix[12], texelX)
	}
	if texelY := s.cfg.TexelSize.Y; positiveFinite(texelY) {
		matrix[13] = snapScalar(matrix[13], texelY)
	}

	if s.cache == nil {
		s.cache = make(map[fitKey]Mat4)
	}
	s.cache[key] = matrix
	s.fits++
	return matrix, nil
}

// SnapTexel aligns a matrix to a square texel grid.
func (s *CascadeFitSolver) SnapTexel(matrix Mat4, texel float64) Mat4 {
	if !positiveFinite(texel) {
		return matrix
	}

	matrix[12] = snapScalar(matrix[12], texel)
	matrix[13] = snapScalar(matrix[13], texel)
	matrix[14] = snapScalar(matrix[14], texel)
	return matrix
}

// SnapTexelXY aligns a matrix to independent horizontal and vertical texels.
func (s *CascadeFitSolver) SnapTexelXY(matrix Mat4, texelX, texelY float64) (Mat4, error) {
	if !positiveFinite(texelX) || !positiveFinite(texelY) {
		return matrix, ErrBadTexel
	}

	matrix[12] = snapScalar(matrix[12], texelX)
	matrix[13] = snapScalar(matrix[13], texelY)
	return matrix, nil
}

// BlendBand reports the normalized start and end of one cascade blend band.
func (s *CascadeFitSolver) BlendBand(level int) (float64, float64) {
	s.mu.RLock()
	count := s.cfg.Cascades
	fraction := s.cfg.BlendFraction
	s.mu.RUnlock()

	if level < 0 || level >= count {
		return 0, 0
	}
	if level == count-1 {
		return 1, 1
	}

	if !validUnitInterval(fraction) {
		fraction = 0
	}
	return 1 - fraction, 1
}

// SetLambda changes the logarithmic versus linear split weight.
func (s *CascadeFitSolver) SetLambda(lambda float64) error {
	if !validUnitInterval(lambda) {
		return ErrBadLambda
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg.Lambda == lambda {
		return nil
	}
	s.cfg.Lambda = lambda
	s.refreshAutomaticBoundsLocked()
	s.invalidateLocked("lambda changed")
	return nil
}

// SetBlendFraction changes the width of the cascade transition bands.
func (s *CascadeFitSolver) SetBlendFraction(fraction float64) error {
	if !validUnitInterval(fraction) {
		return ErrBadBlend
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg.BlendFraction == fraction {
		return nil
	}
	s.cfg.BlendFraction = fraction
	s.invalidateLocked("blend fraction changed")
	return nil
}

// Invalidate drops the cached matrices after an external change.
func (s *CascadeFitSolver) Invalidate(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked(reason)
}

// CascadeCount reports how many cascades the solver is configured for.
func (s *CascadeFitSolver) CascadeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Cascades
}

// ViewBounds reports the near and far depth of one cascade.
func (s *CascadeFitSolver) ViewBounds(level int) (float64, float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if level < 0 || level >= s.cfg.Cascades {
		return 0, 0, ErrBadCascade
	}
	next := level + 1
	if !s.hasBounds || level >= len(s.bounds) || next >= len(s.bounds) {
		return 0, 0, nil
	}
	return s.bounds[level], s.bounds[next], nil
}

// Stats reports the fit and cache counters.
func (s *CascadeFitSolver) Stats() FitStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return FitStats{
		Fits:          s.fits,
		CacheHits:     s.cacheHits,
		Invalidations: s.invalidations,
		Version:       s.version,
	}
}

// Version reports the configuration and cache version.
func (s *CascadeFitSolver) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Snapshot serialises the configuration and the manual split distances.
func (s *CascadeFitSolver) Snapshot() ([]byte, error) {
	s.mu.RLock()
	data := snapshotData{
		Magic:         snapshotMagic,
		Schema:        snapshotSchema,
		Version:       s.version,
		Config:        s.cfg,
		Manual:        cloneFloats(s.manual),
		Bounds:        cloneFloats(s.bounds),
		SplitNear:     s.splitNear,
		SplitFar:      s.splitFar,
		HasBounds:     s.hasBounds,
		Fits:          s.fits,
		CacheHits:     s.cacheHits,
		Invalidations: s.invalidations,
		LastReason:    s.lastReason,
	}
	s.mu.RUnlock()

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return payload, nil
}

// Restore rebuilds the solver from a snapshot, either completely or not at all.
func (s *CascadeFitSolver) Restore(payload []byte) error {
	var data snapshotData
	if err := json.Unmarshal(payload, &data); err != nil {
		return ErrBadSnapshot
	}
	if data.Magic != snapshotMagic || data.Schema != snapshotSchema {
		return ErrBadSnapshot
	}
	if data.Fits < 0 || data.CacheHits < 0 {
		return ErrBadSnapshot
	}

	manual := cloneFloats(data.Manual)
	bounds := cloneFloats(data.Bounds)
	count := data.Config.Cascades

	if len(manual) > 0 {
		if count < 1 || count > 8 || len(manual) != count-1 {
			return ErrBadSnapshot
		}
		for i := 1; i < len(manual); i++ {
			if manual[i] <= manual[i-1] {
				return ErrBadSnapshot
			}
		}
	}

	if data.HasBounds {
		if count < 1 || count > 8 {
			return ErrBadSnapshot
		}
		if !validRange(data.SplitNear, data.SplitFar) {
			return ErrBadSnapshot
		}
		if len(bounds) != count+1 {
			return ErrBadSnapshot
		}
		if bounds[0] != data.SplitNear || bounds[count] != data.SplitFar {
			return ErrBadSnapshot
		}
		for i := 1; i < len(bounds); i++ {
			if bounds[i] <= bounds[i-1] {
				return ErrBadSnapshot
			}
		}
		if len(manual) == count-1 && len(manual) > 0 {
			for i := range manual {
				if manual[i] != bounds[i+1] {
					return ErrBadSnapshot
				}
			}
		}
	} else if len(bounds) != 0 {
		return ErrBadSnapshot
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = data.Config
	s.manual = manual
	s.bounds = bounds
	s.splitNear = data.SplitNear
	s.splitFar = data.SplitFar
	s.hasBounds = data.HasBounds
	s.version = data.Version
	s.fits = data.Fits
	s.cacheHits = data.CacheHits
	s.invalidations = data.Invalidations
	s.lastReason = data.LastReason

	s.cache = make(map[fitKey]Mat4)
	s.lens = lensSignature{}
	s.hasLens = false
	return nil
}

// Reset restores the configuration captured at construction time.
func (s *CascadeFitSolver) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = s.baseCfg
	s.manual = nil
	s.bounds = nil
	s.splitNear = 0
	s.splitFar = 0
	s.hasBounds = false
	s.cache = make(map[fitKey]Mat4)
	s.lens = lensSignature{}
	s.hasLens = false
	s.fits = 0
	s.cacheHits = 0
	s.invalidations = 0
	s.version = 0
	s.lastReason = ""
	return nil
}

func (s *CascadeFitSolver) invalidateLocked(reason string) {
	s.lastReason = reason
	s.cache = make(map[fitKey]Mat4)
	s.invalidations++
	s.version++
}

func (s *CascadeFitSolver) refreshAutomaticBoundsLocked() {
	if !s.hasBounds {
		return
	}
	if count := s.cfg.Cascades; count >= 1 && count <= 8 && len(s.manual) != count-1 {
		s.bounds = automaticBounds(s.cfg, s.splitNear, s.splitFar)
	}
}

func (s *CascadeFitSolver) boundsForCameraLocked(camera Camera) ([]float64, error) {
	count := s.cfg.Cascades
	if s.hasBounds && len(s.bounds) == count+1 &&
		s.splitNear == camera.Near && s.splitFar == camera.Far {
		return cloneFloats(s.bounds), nil
	}

	if validManualForRange(s.manual, count, camera.Near, camera.Far) {
		bounds := manualBounds(s.manual, camera.Near, camera.Far, count)
		s.bounds = cloneFloats(bounds)
		s.splitNear = camera.Near
		s.splitFar = camera.Far
		s.hasBounds = true
		return bounds, nil
	}

	bounds := automaticBounds(s.cfg, camera.Near, camera.Far)
	s.bounds = cloneFloats(bounds)
	s.splitNear = camera.Near
	s.splitFar = camera.Far
	s.hasBounds = true
	return bounds, nil
}

func validateConfig(cfg CascadeConfig) error {
	if cfg.Cascades < 1 || cfg.Cascades > 8 {
		return ErrBadConfig
	}
	if !validUnitInterval(cfg.Lambda) || !validUnitInterval(cfg.BlendFraction) {
		return ErrBadConfig
	}
	return nil
}

func validateCamera(camera Camera) error {
	if !positiveFinite(camera.Near) {
		return ErrBadCamera
	}
	if !finiteFloat(camera.Far) || camera.Far <= camera.Near {
		return ErrBadCamera
	}
	if !validOpenDegreeInterval(camera.FovYDeg) {
		return ErrBadCamera
	}
	if !positiveFinite(camera.Aspect) {
		return ErrBadCamera
	}
	if !finiteFloat(camera.Position.X) || !finiteFloat(camera.Position.Y) || !finiteFloat(camera.Position.Z) {
		return ErrBadCamera
	}
	if !finiteFloat(camera.Yaw) || !finiteFloat(camera.Pitch) {
		return ErrBadCamera
	}
	return nil
}

func validRange(near, far float64) bool {
	return positiveFinite(near) && finiteFloat(far) && far > near
}

func validUnitInterval(value float64) bool {
	return value >= 0 && value <= 1
}

func validOpenDegreeInterval(value float64) bool {
	return finiteFloat(value) && value > 0 && value < 180
}

func positiveFinite(value float64) bool {
	return value > 0 && finiteFloat(value)
}

func finiteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validManualDistance(distance, near, far float64) bool {
	return finiteFloat(distance) && distance > near && distance < far
}

func validManualForRange(manual []float64, count int, near, far float64) bool {
	if count < 1 || len(manual) != count-1 || !validRange(near, far) {
		return false
	}
	previous := near
	for _, distance := range manual {
		if !finiteFloat(distance) || distance <= previous || distance >= far {
			return false
		}
		previous = distance
	}
	return true
}

func automaticBounds(cfg CascadeConfig, near, far float64) []float64 {
	count := cfg.Cascades
	bounds := make([]float64, count+1)
	bounds[0] = near
	bounds[count] = far

	rangeRatio := far / near
	for i := 1; i < count; i++ {
		t := float64(i) / float64(count)
		switch cfg.Lambda {
		case 0:
			bounds[i] = near + (far-near)*t
		case 1:
			bounds[i] = near * math.Pow(rangeRatio, t)
		default:
			logarithmic := near * math.Pow(rangeRatio, t)
			linear := near + (far-near)*t
			bounds[i] = cfg.Lambda*logarithmic + (1-cfg.Lambda)*linear
		}
	}
	return bounds
}

func manualBounds(manual []float64, near, far float64, count int) []float64 {
	bounds := make([]float64, count+1)
	bounds[0] = near
	copy(bounds[1:count], manual)
	bounds[count] = far
	return bounds
}

func snapScalar(value, texel float64) float64 {
	return math.Round(value/texel) * texel
}

func cloneFloats(values []float64) []float64 {
	if values == nil {
		return nil
	}
	clone := make([]float64, len(values))
	copy(clone, values)
	return clone
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildCascadeMatrix(camera Camera, cascadeNear, cascadeFar float64) Mat4 {
	forward := cameraForward(camera.Yaw, camera.Pitch)
	referenceUp := Vec3{X: 0, Y: 1, Z: 0}
	if math.Abs(forward.Y) > 0.999999 {
		referenceUp = Vec3{X: 0, Y: 0, Z: 1}
	}
	right := normalizeVec3(crossVec3(referenceUp, forward))
	up := normalizeVec3(crossVec3(forward, right))

	tanHalfFov := math.Tan(camera.FovYDeg * math.Pi / 360)
	nearHalfHeight := cascadeNear * tanHalfFov
	farHalfHeight := cascadeFar * tanHalfFov
	nearHalfWidth := nearHalfHeight * camera.Aspect
	farHalfWidth := farHalfHeight * camera.Aspect

	minimum := Vec3{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	maximum := Vec3{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}

	addCorner := func(distance, halfWidth, halfHeight, signRight, signUp float64) {
		center := addVec3(camera.Position, scaleVec3(forward, distance))
		corner := addVec3(center, addVec3(
			scaleVec3(right, signRight*halfWidth),
			scaleVec3(up, signUp*halfHeight),
		))
		minimum.X = math.Min(minimum.X, corner.X)
		minimum.Y = math.Min(minimum.Y, corner.Y)
		minimum.Z = math.Min(minimum.Z, corner.Z)
		maximum.X = math.Max(maximum.X, corner.X)
		maximum.Y = math.Max(maximum.Y, corner.Y)
		maximum.Z = math.Max(maximum.Z, corner.Z)
	}

	for _, signX := range []float64{-1, 1} {
		for _, signY := range []float64{-1, 1} {
			addCorner(cascadeNear, nearHalfWidth, nearHalfHeight, signX, signY)
			addCorner(cascadeFar, farHalfWidth, farHalfHeight, signX, signY)
		}
	}

	repairDegenerateAxis := func(minValue, maxValue float64) (float64, float64) {
		scale := math.Max(math.Abs(cascadeFar), math.Abs(cascadeNear))
		scale = math.Max(scale, math.Abs(camera.Position.X))
		scale = math.Max(scale, math.Abs(camera.Position.Y))
		scale = math.Max(scale, math.Abs(camera.Position.Z))
		epsilon := scale * 1e-12
		if !positiveFinite(epsilon) {
			epsilon = 1e-9
		}
		if maxValue-minValue < epsilon {
			center := (minValue + maxValue) * 0.5
			minValue = center - epsilon*0.5
			maxValue = center + epsilon*0.5
		}
		return minValue, maxValue
	}

	minimum.X, maximum.X = repairDegenerateAxis(minimum.X, maximum.X)
	minimum.Y, maximum.Y = repairDegenerateAxis(minimum.Y, maximum.Y)
	minimum.Z, maximum.Z = repairDegenerateAxis(minimum.Z, maximum.Z)

	width := maximum.X - minimum.X
	height := maximum.Y - minimum.Y
	depth := maximum.Z - minimum.Z

	var matrix Mat4
	matrix[0] = 2 / width
	matrix[5] = 2 / height
	matrix[10] = 2 / depth
	matrix[12] = -(maximum.X + minimum.X) / width
	matrix[13] = -(maximum.Y + minimum.Y) / height
	matrix[14] = -(maximum.Z + minimum.Z) / depth
	matrix[15] = 1
	return matrix
}

func cameraForward(yawDegrees, pitchDegrees float64) Vec3 {
	yaw := yawDegrees * math.Pi / 180
	pitch := pitchDegrees * math.Pi / 180
	cosPitch := math.Cos(pitch)
	forward := Vec3{
		X: math.Sin(yaw) * cosPitch,
		Y: math.Sin(pitch),
		Z: math.Cos(yaw) * cosPitch,
	}
	return normalizeVec3(forward)
}

func crossVec3(a, b Vec3) Vec3 {
	return Vec3{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

func addVec3(a, b Vec3) Vec3 {
	return Vec3{X: a.X + b.X, Y: a.Y + b.Y, Z: a.Z + b.Z}
}

func scaleVec3(vector Vec3, scale float64) Vec3 {
	return Vec3{X: vector.X * scale, Y: vector.Y * scale, Z: vector.Z * scale}
}

func normalizeVec3(vector Vec3) Vec3 {
	length := math.Sqrt(vector.X*vector.X + vector.Y*vector.Y + vector.Z*vector.Z)
	if !positiveFinite(length) {
		return Vec3{}
	}
	return scaleVec3(vector, 1/length)
}
