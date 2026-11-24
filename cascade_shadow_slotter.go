// Package task splits a camera range into shadow cascades and maps them onto
// the shadow map slots of the frame.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Limits of the cascade table.
const (
	MaxCascades    = 8
	MaxSlots       = 4
	BaseResolution = 1024
	MinResolution  = 64
)

// Errors reported by the slotter.
var (
	ErrBadCamera       = errors.New("camera settings are invalid")
	ErrBadCascadeCount = errors.New("cascade count is out of range")
	ErrBadSlotCount    = errors.New("slot count is out of range")
	ErrNoSuchCascade   = errors.New("cascade is unknown")
	ErrBadHysteresis   = errors.New("hysteresis thresholds are invalid")
	ErrBadDistance     = errors.New("distance is negative")
	ErrNotConfigured   = errors.New("cascade table is not configured")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Camera is the view frustum the cascades are fitted to.
type Camera struct {
	Near   float64
	Far    float64
	FovY   float64
	Aspect float64
}

// Cascade is one shadow range with its slot and map size.
type Cascade struct {
	ID     int
	Near   float64
	Far    float64
	Slot   int
	Width  int
	Height int
}

// CascadeShadowSlotter owns the cascade table and the slot assignment.
type CascadeShadowSlotter struct {
	mu sync.Mutex
}

// NewCascadeShadowSlotter builds a slotter without a configured camera.
func NewCascadeShadowSlotter() *CascadeShadowSlotter {
	return &CascadeShadowSlotter{}
}

// Split divides the camera range into cascades with a constant ratio.
func (s *CascadeShadowSlotter) Split(cam Camera, cascades int) ([]Cascade, error) {
	return nil, errNotImplemented
}

// AssignSlots maps the cascades onto the available shadow map slots.
func (s *CascadeShadowSlotter) AssignSlots(cascades []Cascade, slots int) error {
	return errNotImplemented
}

// Resolution reports the shadow map size of one cascade.
func (s *CascadeShadowSlotter) Resolution(cascadeID int) (int, int, error) {
	return 0, 0, errNotImplemented
}

// SetCamera stages a camera that takes effect on the next frame boundary.
func (s *CascadeShadowSlotter) SetCamera(cam Camera) error {
	return errNotImplemented
}

// CommitFrame applies the staged camera.
func (s *CascadeShadowSlotter) CommitFrame() error { return errNotImplemented }

// Camera reports the camera that is active for the current frame.
func (s *CascadeShadowSlotter) Camera() (Camera, error) {
	return Camera{}, errNotImplemented
}

// Hysteresis sets the enter and exit thresholds of the cascade switch.
func (s *CascadeShadowSlotter) Hysteresis(enter, exit float64) error {
	return errNotImplemented
}

// SelectCascade reports the cascade that covers the given view distance.
func (s *CascadeShadowSlotter) SelectCascade(distance float64) (int, error) {
	return 0, errNotImplemented
}

// Slots reports the configured cascade table.
func (s *CascadeShadowSlotter) Slots() []Cascade { return nil }

// Snapshot serialises the cascade table, the camera and the slot assignment.
func (s *CascadeShadowSlotter) Snapshot() ([]byte, error) { return nil, errNotImplemented }

// Restore replaces the slotter state with a snapshot, all or nothing.
func (s *CascadeShadowSlotter) Restore(data []byte) error { return errNotImplemented }
