package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the transform chain.
var (
	ErrTransformSize     = errors.New("source size is not usable")
	ErrTransformAlign    = errors.New("alignment must be 1, 2, 4 or 8")
	ErrTransformRotation = errors.New("rotation must be a multiple of 90 degrees")
	ErrTransformCrop     = errors.New("crop rectangle is outside the source frame")
	ErrTransformSnapshot = errors.New("transform chain snapshot is not readable")
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
	mu sync.Mutex
}

// NewTransformChain validates the source frame and the chroma alignment.
func NewTransformChain(src Size, align int) (*TransformChain, error) {
	return nil, errNotImplemented
}

// SetSourceSize replaces the source frame size.
func (c *TransformChain) SetSourceSize(src Size) error {
	return errNotImplemented
}

// SetAlign replaces the chroma alignment.
func (c *TransformChain) SetAlign(align int) error {
	return errNotImplemented
}

// SetTransform stores a transform after normalising it.
func (c *TransformChain) SetTransform(t Transform) error {
	return errNotImplemented
}

// Normalize returns the canonical form of a transform.
func (c *TransformChain) Normalize(t Transform) (Transform, error) {
	return Transform{}, errNotImplemented
}

// Apply computes the output geometry of a transform.
func (c *TransformChain) Apply(t Transform) (Result, error) {
	return Result{}, errNotImplemented
}

// OutputSize reports the output size of the stored transform.
func (c *TransformChain) OutputSize() (Size, error) {
	return Size{}, errNotImplemented
}

// Current reports the stored transform in canonical form.
func (c *TransformChain) Current() Transform {
	return Transform{}
}

// Reset returns the chain to the identity transform.
func (c *TransformChain) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the chain state.
func (c *TransformChain) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the chain state from a snapshot.
func (c *TransformChain) Restore(data []byte) error {
	return errNotImplemented
}

// Stats reports the counters kept by the chain.
func (c *TransformChain) Stats() ChainStats {
	return ChainStats{}
}
