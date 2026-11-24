package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the compositor guard.
var (
	ErrLayoutInvalid  = errors.New("layout template is not usable")
	ErrRegionOverlap  = errors.New("layout regions overlap")
	ErrTrackInvalid   = errors.New("track is not usable")
	ErrTrackExists    = errors.New("track id already exists")
	ErrUnknownTrack   = errors.New("track id is unknown")
	ErrUnknownRegion  = errors.New("region name is not in the layout")
	ErrRegionTaken    = errors.New("region is already assigned")
	ErrNoRegion       = errors.New("no free region is left")
	ErrFrameOrder     = errors.New("output frame number is not increasing")
	ErrStateCorrupt   = errors.New("compositor guard state is corrupt")
)

// Region is one rectangle of the layout template.
type Region struct {
	Name string
	X    int
	Y    int
	W    int
	H    int
}

// Layout is the template the compositor arranges tracks into.
type Layout struct {
	Name    string
	Regions []Region
}

// Track is one video input of the mix.
type Track struct {
	ID     string
	Width  int
	Height int
	FPS    int
	Region string
}

// Cell is one region of a composed output frame.
type Cell struct {
	Region  string
	TrackID string
	Filled  bool
	Valid   bool
	FrameNo uint64
}

// Composition is one composed output frame.
type Composition struct {
	FrameNo   uint64
	Timestamp int64
	Cells     []Cell
}

// Config configures the output rate and the speaker hysteresis.
type Config struct {
	OutputFPS       int
	HysteresisCount int
}

// GuardStats reports the counters of the compositor guard.
type GuardStats struct {
	Frames          uint64
	Cells           uint64
	Filled          uint64
	InvalidCells    uint64
	Dropped         uint64
	SpeakerSwitches uint64
	TrackAdds       uint64
	TrackRemoves    uint64
}

// CompositorGuard owns the mix layout, the speaker state and the output clock.
type CompositorGuard struct {
	mu sync.Mutex
}

// NewCompositorGuard builds a guard for one layout template.
func NewCompositorGuard(layout Layout, cfg Config) (*CompositorGuard, error) {
	return nil, errNotImplemented
}

// AddTrack adds one input track and assigns it a region.
func (g *CompositorGuard) AddTrack(track Track) error {
	return errNotImplemented
}

// RemoveTrack removes one input track and frees its region.
func (g *CompositorGuard) RemoveTrack(id string) error {
	return errNotImplemented
}

// SetLayout replaces the layout template and reassigns the tracks.
func (g *CompositorGuard) SetLayout(layout Layout) error {
	return errNotImplemented
}

// SetActiveSpeaker reports one nomination of the active speaker.
func (g *CompositorGuard) SetActiveSpeaker(trackID string) error {
	return errNotImplemented
}

// ActiveSpeaker reports the speaker that survived the hysteresis.
func (g *CompositorGuard) ActiveSpeaker() string {
	return ""
}

// Compose produces the output frame for the given output frame number.
func (g *CompositorGuard) Compose(frameNo uint64, timestamp int64) (*Composition, error) {
	return nil, errNotImplemented
}

// Regions reports the current layout template.
func (g *CompositorGuard) Regions() []Region {
	return nil
}

// TrackRegion reports the region assigned to one track.
func (g *CompositorGuard) TrackRegion(id string) (string, bool) {
	return "", false
}

// Snapshot serialises tracks, layout, assignments, speaker state and counters.
func (g *CompositorGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard state from a snapshot.
func (g *CompositorGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Stats reports the compositor counters.
func (g *CompositorGuard) Stats() GuardStats {
	return GuardStats{}
}

// Reset clears the tracks, the layout, the speaker state and the counters.
func (g *CompositorGuard) Reset() error {
	return errNotImplemented
}
