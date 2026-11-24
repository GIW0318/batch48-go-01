package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// Errors reported by the compositor guard.
var (
	ErrLayoutInvalid = errors.New("layout template is not usable")
	ErrRegionOverlap = errors.New("layout regions overlap")
	ErrTrackInvalid  = errors.New("track is not usable")
	ErrTrackExists   = errors.New("track id already exists")
	ErrUnknownTrack  = errors.New("track id is unknown")
	ErrUnknownRegion = errors.New("region name is not in the layout")
	ErrRegionTaken   = errors.New("region is already assigned")
	ErrNoRegion      = errors.New("no free region is left")
	ErrFrameOrder    = errors.New("output frame number is not increasing")
	ErrStateCorrupt  = errors.New("compositor guard state is corrupt")
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

type trackState struct {
	track       Track
	sourceFrame uint64
}

// CompositorGuard owns the mix layout, the speaker state and the output clock.
type CompositorGuard struct {
	mu sync.Mutex

	layout  Layout
	config  Config
	tracks  map[string]*trackState
	order   []string
	regions map[string]string

	activeSpeaker  string
	candidate      string
	candidateCount int

	lastFrameNo uint64
	stats       GuardStats
}

// NewCompositorGuard builds a guard for one layout template.
func NewCompositorGuard(layout Layout, cfg Config) (*CompositorGuard, error) {
	if err := validateLayout(layout); err != nil {
		return nil, err
	}
	if cfg.OutputFPS <= 0 {
		cfg.OutputFPS = 1
	}
	if cfg.HysteresisCount <= 0 {
		cfg.HysteresisCount = 1
	}
	return &CompositorGuard{
		layout:  cloneLayout(layout),
		config:  cfg,
		tracks:  make(map[string]*trackState),
		regions: make(map[string]string),
	}, nil
}

// AddTrack adds one input track and assigns it a region.
func (g *CompositorGuard) AddTrack(track Track) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if track.ID == "" || track.Width <= 0 || track.Height <= 0 || track.FPS <= 0 {
		return ErrTrackInvalid
	}
	if _, exists := g.tracks[track.ID]; exists {
		return ErrTrackExists
	}

	region := track.Region
	if region != "" {
		if _, exists := g.regionByName(region); !exists {
			return ErrUnknownRegion
		}
		if _, taken := g.regions[region]; taken {
			return ErrRegionTaken
		}
	} else {
		for _, r := range g.layout.Regions {
			if _, taken := g.regions[r.Name]; !taken {
				region = r.Name
				break
			}
		}
		if region == "" {
			return ErrNoRegion
		}
	}

	track.Region = region
	g.tracks[track.ID] = &trackState{track: track}
	g.order = append(g.order, track.ID)
	g.regions[region] = track.ID
	g.stats.TrackAdds++
	if g.activeSpeaker == "" {
		g.activeSpeaker = track.ID
	}
	return nil
}

// RemoveTrack removes one input track and frees its region.
func (g *CompositorGuard) RemoveTrack(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	ts, exists := g.tracks[id]
	if !exists {
		return ErrUnknownTrack
	}
	delete(g.tracks, id)
	delete(g.regions, ts.track.Region)
	for i, current := range g.order {
		if current == id {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	if g.activeSpeaker == id {
		g.activeSpeaker = ""
	}
	if g.candidate == id {
		g.candidate = ""
		g.candidateCount = 0
	}
	g.stats.TrackRemoves++
	return nil
}

// SetLayout replaces the layout template and reassigns the tracks.
func (g *CompositorGuard) SetLayout(layout Layout) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := validateLayout(layout); err != nil {
		return err
	}

	newRegions := make(map[string]struct{}, len(layout.Regions))
	for _, region := range layout.Regions {
		newRegions[region.Name] = struct{}{}
	}

	assigned := make(map[string]string, len(g.tracks))
	used := make(map[string]string, len(g.tracks))
	pending := make([]string, 0, len(g.tracks))

	for _, id := range g.order {
		ts := g.tracks[id]
		if _, exists := newRegions[ts.track.Region]; exists {
			assigned[id] = ts.track.Region
			used[ts.track.Region] = id
		} else {
			pending = append(pending, id)
		}
	}

	for _, id := range pending {
		region := ""
		for _, r := range layout.Regions {
			if _, taken := used[r.Name]; !taken {
				region = r.Name
				break
			}
		}
		if region == "" {
			return ErrNoRegion
		}
		assigned[id] = region
		used[region] = id
	}

	g.layout = cloneLayout(layout)
	g.regions = used
	for id, region := range assigned {
		if ts := g.tracks[id]; ts != nil {
			ts.track.Region = region
		}
	}
	return nil
}

// SetActiveSpeaker reports one nomination of the active speaker.
func (g *CompositorGuard) SetActiveSpeaker(trackID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.tracks[trackID]; !exists {
		return ErrUnknownTrack
	}
	if g.activeSpeaker == trackID {
		g.candidate = ""
		g.candidateCount = 0
		return nil
	}

	if g.candidate == trackID {
		g.candidateCount++
	} else {
		g.candidate = trackID
		g.candidateCount = 1
	}

	if g.candidateCount >= g.hysteresisCount() {
		g.activeSpeaker = trackID
		g.candidate = ""
		g.candidateCount = 0
		g.stats.SpeakerSwitches++
	}
	return nil
}

// ActiveSpeaker reports the speaker that survived the hysteresis.
func (g *CompositorGuard) ActiveSpeaker() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeSpeaker
}

// Compose produces the output frame for the given output frame number.
func (g *CompositorGuard) Compose(frameNo uint64, timestamp int64) (*Composition, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if frameNo == 0 || frameNo <= g.lastFrameNo {
		return nil, ErrFrameOrder
	}

	cells := make([]Cell, 0, len(g.layout.Regions))
	for _, region := range g.layout.Regions {
		id, occupied := g.regions[region.Name]
		if !occupied {
			continue
		}
		ts := g.tracks[id]
		if ts == nil {
			continue
		}

		consumed := g.inputFramesFor(frameNo, ts.track.FPS)
		filled := consumed == 0
		if filled {
			g.stats.Filled++
		} else {
			ts.sourceFrame += consumed
			if consumed > 1 {
				g.stats.Dropped += consumed - 1
			}
		}

		valid := !filled
		if !valid {
			g.stats.InvalidCells++
		}
		cells = append(cells, Cell{
			Region:  region.Name,
			TrackID: id,
			Filled:  filled,
			Valid:   valid,
			FrameNo: ts.sourceFrame,
		})
	}

	g.stats.Frames++
	g.stats.Cells += uint64(len(cells))
	g.lastFrameNo = frameNo
	return &Composition{
		FrameNo:   frameNo,
		Timestamp: timestamp,
		Cells:     cells,
	}, nil
}

// Regions reports the current layout template.
func (g *CompositorGuard) Regions() []Region {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cloneRegions(g.layout.Regions)
}

// TrackRegion reports the region assigned to one track.
func (g *CompositorGuard) TrackRegion(id string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ts, exists := g.tracks[id]
	if !exists {
		return "", false
	}
	return ts.track.Region, true
}

type snapshotTrack struct {
	Track       Track  `json:"track"`
	SourceFrame uint64 `json:"source_frame"`
}

type snapshotState struct {
	Version        int             `json:"version"`
	Layout         Layout          `json:"layout"`
	Config         Config          `json:"config"`
	Tracks         []snapshotTrack `json:"tracks"`
	ActiveSpeaker  string          `json:"active_speaker"`
	Candidate      string          `json:"candidate"`
	CandidateCount int             `json:"candidate_count"`
	LastFrameNo    uint64          `json:"last_frame_no"`
	Stats          GuardStats      `json:"stats"`
}

const snapshotVersion = 1

// Snapshot serialises tracks, layout, assignments, speaker state and counters.
func (g *CompositorGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := snapshotState{
		Version:        snapshotVersion,
		Layout:         cloneLayout(g.layout),
		Config:         g.config,
		Tracks:         make([]snapshotTrack, 0, len(g.order)),
		ActiveSpeaker:  g.activeSpeaker,
		Candidate:      g.candidate,
		CandidateCount: g.candidateCount,
		LastFrameNo:    g.lastFrameNo,
		Stats:          g.stats,
	}
	for _, id := range g.order {
		ts := g.tracks[id]
		if ts == nil {
			continue
		}
		state.Tracks = append(state.Tracks, snapshotTrack{
			Track:       ts.track,
			SourceFrame: ts.sourceFrame,
		})
	}
	return json.Marshal(state)
}

// Restore rebuilds the guard state from a snapshot.
func (g *CompositorGuard) Restore(payload []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var state snapshotState
	if len(payload) == 0 || json.Unmarshal(payload, &state) != nil {
		return ErrStateCorrupt
	}
	if state.Version != snapshotVersion {
		return ErrStateCorrupt
	}
	if validateLayout(state.Layout) != nil {
		return ErrStateCorrupt
	}
	if state.Config.OutputFPS <= 0 || state.Config.HysteresisCount <= 0 {
		return ErrStateCorrupt
	}

	regionNames := make(map[string]struct{}, len(state.Layout.Regions))
	for _, region := range state.Layout.Regions {
		regionNames[region.Name] = struct{}{}
	}

	tracks := make(map[string]*trackState, len(state.Tracks))
	order := make([]string, 0, len(state.Tracks))
	occupied := make(map[string]string, len(state.Tracks))
	for _, saved := range state.Tracks {
		track := saved.Track
		if track.ID == "" || track.Width <= 0 || track.Height <= 0 || track.FPS <= 0 || track.Region == "" {
			return ErrStateCorrupt
		}
		if _, exists := tracks[track.ID]; exists {
			return ErrStateCorrupt
		}
		if _, exists := regionNames[track.Region]; !exists {
			return ErrStateCorrupt
		}
		if _, taken := occupied[track.Region]; taken {
			return ErrStateCorrupt
		}
		tracks[track.ID] = &trackState{track: track, sourceFrame: saved.SourceFrame}
		order = append(order, track.ID)
		occupied[track.Region] = track.ID
	}

	if state.ActiveSpeaker != "" {
		if _, exists := tracks[state.ActiveSpeaker]; !exists {
			return ErrStateCorrupt
		}
	}
	if state.CandidateCount < 0 {
		return ErrStateCorrupt
	}
	if state.Candidate == "" {
		if state.CandidateCount != 0 {
			return ErrStateCorrupt
		}
	} else {
		if state.CandidateCount <= 0 {
			return ErrStateCorrupt
		}
		if _, exists := tracks[state.Candidate]; !exists {
			return ErrStateCorrupt
		}
		if state.CandidateCount >= state.Config.HysteresisCount {
			return ErrStateCorrupt
		}
	}

	g.layout = cloneLayout(state.Layout)
	g.config = state.Config
	g.tracks = tracks
	g.order = order
	g.regions = occupied
	g.activeSpeaker = state.ActiveSpeaker
	g.candidate = state.Candidate
	g.candidateCount = state.CandidateCount
	g.lastFrameNo = state.LastFrameNo
	g.stats = state.Stats
	return nil
}

// Stats reports the compositor counters.
func (g *CompositorGuard) Stats() GuardStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

// Reset clears the tracks, the layout, the speaker state and the counters.
func (g *CompositorGuard) Reset() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.layout = Layout{}
	g.tracks = make(map[string]*trackState)
	g.order = nil
	g.regions = make(map[string]string)
	g.activeSpeaker = ""
	g.candidate = ""
	g.candidateCount = 0
	g.lastFrameNo = 0
	g.stats = GuardStats{}
	return nil
}

func (g *CompositorGuard) regionByName(name string) (Region, bool) {
	for _, region := range g.layout.Regions {
		if region.Name == name {
			return region, true
		}
	}
	return Region{}, false
}

func (g *CompositorGuard) hysteresisCount() int {
	if g.config.HysteresisCount < 1 {
		return 1
	}
	return g.config.HysteresisCount
}

func (g *CompositorGuard) inputFramesFor(frameNo uint64, fps int) uint64 {
	if frameNo <= 1 {
		return 1
	}
	outputFPS := uint64(g.config.OutputFPS)
	f := uint64(fps)
	previous := (frameNo - 1) * f / outputFPS
	beforePrevious := (frameNo - 2) * f / outputFPS
	return previous - beforePrevious
}

func validateLayout(layout Layout) error {
	names := make(map[string]struct{}, len(layout.Regions))
	for _, region := range layout.Regions {
		if region.Name == "" || region.W <= 0 || region.H <= 0 {
			return ErrLayoutInvalid
		}
		if _, exists := names[region.Name]; exists {
			return ErrLayoutInvalid
		}
		names[region.Name] = struct{}{}
	}
	for i := 0; i < len(layout.Regions); i++ {
		for j := i + 1; j < len(layout.Regions); j++ {
			if regionsOverlap(layout.Regions[i], layout.Regions[j]) {
				return ErrRegionOverlap
			}
		}
	}
	return nil
}

func regionsOverlap(a, b Region) bool {
	ax1, ay1 := int64(a.X), int64(a.Y)
	ax2, ay2 := ax1+int64(a.W), ay1+int64(a.H)
	bx1, by1 := int64(b.X), int64(b.Y)
	bx2, by2 := bx1+int64(b.W), by1+int64(b.H)
	return ax1 < bx2 && bx1 < ax2 && ay1 < by2 && by1 < ay2
}

func cloneLayout(layout Layout) Layout {
	return Layout{Name: layout.Name, Regions: cloneRegions(layout.Regions)}
}

func cloneRegions(regions []Region) []Region {
	if regions == nil {
		return nil
	}
	cloned := make([]Region, len(regions))
	copy(cloned, regions)
	return cloned
}
