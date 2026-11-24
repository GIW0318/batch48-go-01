package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Errors reported by the transform chain.
var (
	ErrTransformSize     = errors.New("source size is not usable")
	ErrTransformAlign    = errors.New("alignment must be 1, 2, 4 or 8")
	ErrTransformRotation = errors.New("rotation must be a multiple of 90 degrees")
	ErrTransformCrop     = errors.New("crop rectangle is outside the source frame")
	ErrTransformSnapshot = errors.New("transform chain snapshot is not readable")
)

const (
	snapshotMagic   = "gsb-display-transform-guard"
	snapshotVersion = 1
)

// Size is a pixel width and height.
type Size struct {
	W int
	H int
}

// Rect is a pixel rectangle.
type Rect struct {
	X int
	Y int
	W int
	H int
}

// Transform describes rotation, mirroring and cropping of one video frame.
// The crop rectangle is expressed in source pixel coordinates.
type Transform struct {
	Rotation int
	FlipH    bool
	FlipV    bool
	Crop     Rect
}

// Result is the outcome of applying a transform.
type Result struct {
	Output    Size
	Crop      Rect
	Requested Rect
	Aligned   bool
}

// ChainStats reports the counters kept by the chain.
type ChainStats struct {
	Applied    uint64
	Normalized uint64
	Merged     uint64
	Rejected   uint64
	Restored   uint64
}

// TransformChain keeps one display transform chain coherent.
type TransformChain struct {
	mu      sync.Mutex
	src     Size
	align   int
	current Transform
	stats   ChainStats
}

type snapshotEnvelope struct {
	Magic    string `json:"magic"`
	Version  int    `json:"version"`
	Payload  []byte `json:"payload"`
	Checksum string `json:"checksum"`
}

type snapshotState struct {
	Src     Size       `json:"src"`
	Align   int        `json:"align"`
	Current Transform  `json:"current"`
	Stats   ChainStats `json:"stats"`
}

// NewTransformChain validates the source frame and the chroma alignment.
func NewTransformChain(src Size, align int) (*TransformChain, error) {
	if err := validateSize(src); err != nil {
		return nil, err
	}
	if err := validateAlign(align); err != nil {
		return nil, err
	}

	return &TransformChain{src: src, align: align}, nil
}

// SetSourceSize replaces the source frame size.
func (c *TransformChain) SetSourceSize(src Size) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateSize(src); err != nil {
		c.stats.Rejected++
		return err
	}
	if err := validateCrop(c.current.Crop, src); err != nil {
		c.stats.Rejected++
		return err
	}

	c.src = src
	return nil
}

// SetAlign replaces the chroma alignment.
func (c *TransformChain) SetAlign(align int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateAlign(align); err != nil {
		c.stats.Rejected++
		return err
	}

	c.align = align
	return nil
}

// SetTransform stores a transform after normalising it.
func (c *TransformChain) SetTransform(t Transform) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	normalized, _, err := c.normalizeCountLocked(t)
	if err != nil {
		return err
	}

	c.current = normalized
	return nil
}

// Normalize returns the canonical form of a transform.
func (c *TransformChain) Normalize(t Transform) (Transform, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	normalized, _, err := c.normalizeCountLocked(t)
	return normalized, err
}

// Apply computes the output geometry of a transform.
func (c *TransformChain) Apply(t Transform) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	result, merged, err := geometry(t, c.src, c.align)
	if err != nil {
		c.stats.Rejected++
		return Result{}, err
	}

	if merged {
		c.stats.Merged++
	}
	c.stats.Applied++
	return result, nil
}

// OutputSize reports the output size of the stored transform.
func (c *TransformChain) OutputSize() (Size, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	result, _, err := geometry(c.current, c.src, c.align)
	if err != nil {
		return Size{}, err
	}
	return result.Output, nil
}

// Current reports the stored transform in canonical form.
func (c *TransformChain) Current() Transform {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.current
}

// Reset returns the chain to the identity transform.
func (c *TransformChain) Reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.current = Transform{}
	return nil
}

// Snapshot serialises the chain state.
func (c *TransformChain) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := snapshotState{
		Src:     c.src,
		Align:   c.align,
		Current: c.current,
		Stats:   c.stats,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("%w: encode state: %v", ErrTransformSnapshot, err)
	}

	sum := sha256.Sum256(payload)
	envelope := snapshotEnvelope{
		Magic:    snapshotMagic,
		Version:  snapshotVersion,
		Payload:  payload,
		Checksum: hex.EncodeToString(sum[:]),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: encode envelope: %v", ErrTransformSnapshot, err)
	}
	return data, nil
}

// Restore replaces the chain state from a snapshot.
func (c *TransformChain) Restore(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: decode envelope: %v", ErrTransformSnapshot, err)
	}
	if envelope.Magic != snapshotMagic || envelope.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported snapshot format", ErrTransformSnapshot)
	}
	if len(envelope.Payload) == 0 || envelope.Checksum == "" {
		return fmt.Errorf("%w: missing snapshot payload", ErrTransformSnapshot)
	}

	sum := sha256.Sum256(envelope.Payload)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: checksum mismatch", ErrTransformSnapshot)
	}

	var state snapshotState
	if err := json.Unmarshal(envelope.Payload, &state); err != nil {
		return fmt.Errorf("%w: decode state: %v", ErrTransformSnapshot, err)
	}
	if err := validateSize(state.Src); err != nil {
		return fmt.Errorf("%w: %v", ErrTransformSnapshot, err)
	}
	if err := validateAlign(state.Align); err != nil {
		return fmt.Errorf("%w: %v", ErrTransformSnapshot, err)
	}

	normalized, merged, err := normalizeTransform(state.Current, state.Src)
	if err != nil || merged || normalized != state.Current {
		return fmt.Errorf("%w: invalid stored transform", ErrTransformSnapshot)
	}

	c.src = state.Src
	c.align = state.Align
	c.current = state.Current
	c.stats = state.Stats
	c.stats.Restored++
	return nil
}

// Stats reports the counters kept by the chain.
func (c *TransformChain) Stats() ChainStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.stats
}

func (c *TransformChain) normalizeCountLocked(t Transform) (Transform, bool, error) {
	normalized, merged, err := normalizeTransform(t, c.src)
	if err != nil {
		c.stats.Rejected++
		return Transform{}, false, err
	}

	c.stats.Normalized++
	if merged {
		c.stats.Merged++
	}
	return normalized, merged, nil
}

func validateSize(size Size) error {
	if size.W <= 0 || size.H <= 0 {
		return ErrTransformSize
	}
	return nil
}

func validateAlign(align int) error {
	switch align {
	case 1, 2, 4, 8:
		return nil
	default:
		return ErrTransformAlign
	}
}

func canonicalRotation(rotation int) (int, error) {
	if rotation%90 != 0 {
		return 0, ErrTransformRotation
	}

	rotation %= 360
	if rotation < 0 {
		rotation += 360
	}
	return rotation, nil
}

func normalizeTransform(t Transform, src Size) (Transform, bool, error) {
	rotation, err := canonicalRotation(t.Rotation)
	if err != nil {
		return Transform{}, false, err
	}

	merged := t.FlipH && t.FlipV
	normalized := Transform{
		Rotation: rotation,
		Crop:     t.Crop,
	}
	if merged {
		normalized.Rotation = (rotation + 180) % 360
	} else {
		normalized.FlipH = t.FlipH
		normalized.FlipV = t.FlipV
	}

	if err := validateCrop(normalized.Crop, src); err != nil {
		return Transform{}, false, err
	}
	return normalized, merged, nil
}

func validateCrop(crop Rect, src Size) error {
	if crop == (Rect{}) {
		return nil
	}
	if crop.X < 0 || crop.Y < 0 || crop.W <= 0 || crop.H <= 0 {
		return ErrTransformCrop
	}
	if crop.X > src.W || crop.Y > src.H {
		return ErrTransformCrop
	}
	if crop.W > src.W-crop.X || crop.H > src.H-crop.Y {
		return ErrTransformCrop
	}
	return nil
}

func geometry(t Transform, src Size, align int) (Result, bool, error) {
	normalized, merged, err := normalizeTransform(t, src)
	if err != nil {
		return Result{}, false, err
	}

	frame := orientedSize(src, normalized.Rotation)
	rawCrop := mapCropToRotatedFrame(normalized.Crop, src, normalized.Rotation)
	crop := alignCropToFrame(rawCrop, frame, align)

	return Result{
		Output:    Size{W: crop.W, H: crop.H},
		Crop:      crop,
		Requested: t.Crop,
		Aligned:   crop != rawCrop,
	}, merged, nil
}

func orientedSize(src Size, rotation int) Size {
	if rotation == 90 || rotation == 270 {
		return Size{W: src.H, H: src.W}
	}
	return src
}

func mapCropToRotatedFrame(crop Rect, src Size, rotation int) Rect {
	if crop == (Rect{}) {
		crop = Rect{X: 0, Y: 0, W: src.W, H: src.H}
	}

	switch rotation {
	case 90:
		return Rect{
			X: src.H - crop.Y - crop.H,
			Y: crop.X,
			W: crop.H,
			H: crop.W,
		}
	case 180:
		return Rect{
			X: src.W - crop.X - crop.W,
			Y: src.H - crop.Y - crop.H,
			W: crop.W,
			H: crop.H,
		}
	case 270:
		return Rect{
			X: crop.Y,
			Y: src.W - crop.X - crop.W,
			W: crop.H,
			H: crop.W,
		}
	default:
		return crop
	}
}

func alignCropToFrame(raw Rect, frame Size, align int) Rect {
	aligned := Rect{
		X: alignDown(raw.X, align),
		Y: alignDown(raw.Y, align),
		W: alignUp(raw.W, align),
		H: alignUp(raw.H, align),
	}

	if aligned.W <= frame.W {
		if limit := frame.W - aligned.W; aligned.X > limit {
			aligned.X = limit
		}
	} else if aligned.X > 0 {
		aligned.X = 0
	}

	if aligned.H <= frame.H {
		if limit := frame.H - aligned.H; aligned.Y > limit {
			aligned.Y = limit
		}
	} else if aligned.Y > 0 {
		aligned.Y = 0
	}

	return aligned
}

func alignDown(value, align int) int {
	return value - value%align
}

func alignUp(value, align int) int {
	if remainder := value % align; remainder != 0 {
		value += align - remainder
	}
	return value
}
