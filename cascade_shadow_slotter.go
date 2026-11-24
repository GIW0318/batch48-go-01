// Package task splits a camera range into shadow cascades and maps them onto
// the shadow map slots of the frame.
package task

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
)

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

	// Active configuration, visible to readers until the next frame boundary.
	hasCamera bool
	camera    Camera
	cascades  []Cascade

	// Camera staged by SetCamera, applied atomically on CommitFrame.
	hasStaged bool
	staged    Camera

	// Cascade switch hysteresis and the currently held selection.
	hasHysteresis bool
	enter         float64
	exit          float64
	selected      int

	// Bumped every time the cascade table or slot numbering changes so that
	// samples referencing the old numbering are invalidated together.
	generation uint64
}

// NewCascadeShadowSlotter builds a slotter without a configured camera.
func NewCascadeShadowSlotter() *CascadeShadowSlotter {
	return &CascadeShadowSlotter{}
}

// validCamera reports whether the camera describes a usable frustum.
func validCamera(cam Camera) bool {
	return cam.Near > 0 &&
		cam.Far > cam.Near &&
		cam.FovY > 0 && cam.FovY < 180 &&
		cam.Aspect > 0
}

// resolutionOf derives the shadow map size of one cascade level.
func resolutionOf(cascadeID int) int {
	size := BaseResolution >> uint(cascadeID)
	if size < MinResolution {
		return MinResolution
	}
	return size
}

// Split divides the camera range into cascades with a constant ratio.
func (s *CascadeShadowSlotter) Split(cam Camera, cascades int) ([]Cascade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validCamera(cam) {
		return nil, ErrBadCamera
	}
	if cascades < 1 || cascades > MaxCascades {
		return nil, ErrBadCascadeCount
	}

	ratio := math.Pow(cam.Far/cam.Near, 1.0/float64(cascades))
	boundaries := make([]float64, cascades+1)
	boundaries[0] = cam.Near
	for i := 1; i <= cascades; i++ {
		boundaries[i] = boundaries[i-1] * ratio
	}
	// Pin the ends so the table is seamless even with floating point drift.
	boundaries[0] = cam.Near
	boundaries[cascades] = cam.Far

	out := make([]Cascade, cascades)
	for i := 0; i < cascades; i++ {
		size := resolutionOf(i)
		out[i] = Cascade{
			ID:     i,
			Near:   boundaries[i],
			Far:    boundaries[i+1],
			Slot:   -1,
			Width:  size,
			Height: size,
		}
	}
	return out, nil
}

// AssignSlots maps the cascades onto the available shadow map slots.
func (s *CascadeShadowSlotter) AssignSlots(cascades []Cascade, slots int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(cascades) < 1 || len(cascades) > MaxCascades {
		return ErrBadCascadeCount
	}
	if slots < 1 || slots > MaxSlots {
		return ErrBadSlotCount
	}

	stored := make([]Cascade, len(cascades))
	for i, c := range cascades {
		c.ID = i
		if i < slots {
			c.Slot = i
		} else {
			c.Slot = -1
		}
		if c.Width <= 0 || c.Height <= 0 {
			c.Width = resolutionOf(i)
			c.Height = c.Width
		}
		stored[i] = c
	}

	// Replace the whole table and its numbering in one critical section so
	// readers never mix old and new cascade ids within a frame.
	s.cascades = stored
	s.selected = 0
	s.generation++
	return nil
}

// Resolution reports the shadow map size of one cascade.
func (s *CascadeShadowSlotter) Resolution(cascadeID int) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.cascades) == 0 {
		return 0, 0, ErrNotConfigured
	}
	if cascadeID < 0 || cascadeID >= len(s.cascades) {
		return 0, 0, ErrNoSuchCascade
	}
	size := resolutionOf(cascadeID)
	return size, size, nil
}

// SetCamera stages a camera that takes effect on the next frame boundary.
func (s *CascadeShadowSlotter) SetCamera(cam Camera) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validCamera(cam) {
		return ErrBadCamera
	}
	s.staged = cam
	s.hasStaged = true
	return nil
}

// CommitFrame applies the staged camera.
func (s *CascadeShadowSlotter) CommitFrame() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasStaged {
		return nil
	}
	// The whole new configuration becomes visible at once.
	s.camera = s.staged
	s.hasCamera = true
	s.hasStaged = false
	s.generation++
	return nil
}

// Camera reports the camera that is active for the current frame.
func (s *CascadeShadowSlotter) Camera() (Camera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasCamera {
		return Camera{}, ErrNotConfigured
	}
	return s.camera, nil
}

// Hysteresis sets the enter and exit thresholds of the cascade switch.
func (s *CascadeShadowSlotter) Hysteresis(enter, exit float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !(enter > exit && exit > 0) {
		return ErrBadHysteresis
	}
	s.enter = enter
	s.exit = exit
	s.hasHysteresis = true
	return nil
}

// SelectCascade reports the cascade that covers the given view distance.
func (s *CascadeShadowSlotter) SelectCascade(distance float64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.cascades) == 0 {
		return 0, ErrNotConfigured
	}
	if distance < 0 {
		return 0, ErrBadDistance
	}

	// Clamp onto the furthest cascade instead of wrapping back to the first
	// one so objects beyond the covered range keep the farthest shadow.
	raw := len(s.cascades) - 1
	for i, c := range s.cascades {
		if distance < c.Far {
			raw = i
			break
		}
	}

	if !s.hasHysteresis {
		s.selected = raw
		return raw, nil
	}

	// Hold the current cascade until the distance crosses the widened band:
	// jitter smaller than the thresholds cannot move the selection.
	switch {
	case raw > s.selected:
		threshold := s.cascades[raw].Near + s.enter
		if distance >= threshold {
			s.selected = raw
		}
	case raw < s.selected:
		threshold := s.cascades[s.selected].Near - s.exit
		if distance <= threshold {
			s.selected = raw
		}
	}
	return s.selected, nil
}

// Slots reports a copy of the configured cascade table.
func (s *CascadeShadowSlotter) Slots() []Cascade {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Cascade, len(s.cascades))
	copy(out, s.cascades)
	return out
}

// snapshotState is the serialised frame of the slotter.
type snapshotState struct {
	HasCamera     bool      `json:"has_camera"`
	Camera        Camera    `json:"camera"`
	Cascades      []Cascade `json:"cascades"`
	HasHysteresis bool      `json:"has_hysteresis"`
	Enter         float64   `json:"enter"`
	Exit          float64   `json:"exit"`
	Selected      int       `json:"selected"`
	Generation    uint64    `json:"generation"`
}

// Snapshot serialises the cascade table, the camera and the slot assignment.
func (s *CascadeShadowSlotter) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := snapshotState{
		HasCamera:     s.hasCamera,
		Camera:        s.camera,
		Cascades:      append([]Cascade(nil), s.cascades...),
		HasHysteresis: s.hasHysteresis,
		Enter:         s.enter,
		Exit:          s.exit,
		Selected:      s.selected,
		Generation:    s.generation,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return data, nil
}

// Restore replaces the slotter state with a snapshot, all or nothing.
func (s *CascadeShadowSlotter) Restore(data []byte) error {
	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return ErrBadSnapshot
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Build the replacement state fully before touching the live table so a
	// rejected payload leaves the old configuration completely intact.
	nextCascades := append([]Cascade(nil), state.Cascades...)
	if state.HasCamera && !validCamera(state.Camera) {
		return ErrBadSnapshot
	}
	if state.HasHysteresis && !(state.Enter > state.Exit && state.Exit > 0) {
		return ErrBadSnapshot
	}
	if len(nextCascades) > MaxCascades {
		return ErrBadSnapshot
	}
	if state.Selected < 0 || (len(nextCascades) > 0 && state.Selected >= len(nextCascades)) {
		return ErrBadSnapshot
	}

	s.hasCamera = state.HasCamera
	s.camera = state.Camera
	s.cascades = nextCascades
	s.hasHysteresis = state.HasHysteresis
	s.enter = state.Enter
	s.exit = state.Exit
	s.selected = state.Selected
	s.generation = state.Generation + 1
	s.hasStaged = false
	s.staged = Camera{}
	return nil
}
