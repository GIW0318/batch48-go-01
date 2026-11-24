package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors reported by the spawn point guard.
var (
	ErrInvalidPoint   = errors.New("spawn point definition rejected")
	ErrDuplicatePoint = errors.New("spawn point already registered")
	ErrUnknownPoint   = errors.New("spawn point does not exist")
	ErrUnknownSpawn   = errors.New("spawn id does not exist")
	ErrInvalidCount   = errors.New("spawn count must be positive")
	ErrNotReady       = errors.New("spawn point is not ready")
	ErrInvalidPhase   = errors.New("phase name rejected")
	ErrBadSnapshot    = errors.New("snapshot payload rejected")
)

const (
	defaultPhase    = "default"
	snapshotVersion = 1
)

// Point is one spawn point definition.
type Point struct {
	Name     string
	Region   string
	Group    string
	Interval int64
	Capacity int
}

// Spawn is one monster instance produced by the guard.
type Spawn struct {
	ID    string
	Point string
	Phase string
	At    int64
}

// Options carries the density ceiling of every region.
type Options struct {
	RegionCap map[string]int
	StartAt   int64
}

type pointState struct {
	def       Point
	alive     map[string]Spawn
	lastTake  int64
	lastDeath int64
	pending   int
	seq       int
}

// SpawnPointGuard controls refill timers, capacities and region density.
type SpawnPointGuard struct {
	mu     sync.Mutex
	opts   Options
	phase  string
	points map[string]*pointState
}

// NewSpawnPointGuard builds a guard with the given density ceilings.
func NewSpawnPointGuard(opts Options) *SpawnPointGuard {
	return &SpawnPointGuard{
		opts:   opts,
		phase:  defaultPhase,
		points: make(map[string]*pointState),
	}
}

// groupKey identifies the shared timer group of a point. Points without an
// explicit Group get a private singleton group so they never share a timer.
func groupKey(p Point) string {
	if p.Group != "" {
		return "group:" + p.Group
	}
	return "point:" + p.Name
}

// baselineLocked returns the timer base: the point's last Take and the
// group's last death (propagated onto lastDeath) take the later value.
func baselineLocked(ps *pointState) int64 {
	if ps.lastDeath > ps.lastTake {
		return ps.lastDeath
	}
	return ps.lastTake
}

// AddPoint registers one spawn point definition.
func (g *SpawnPointGuard) AddPoint(p Point) error {
	if p.Name == "" || p.Interval <= 0 || p.Capacity <= 0 {
		return ErrInvalidPoint
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.points[p.Name]; exists {
		return ErrDuplicatePoint
	}

	// A point joining an existing group inherits the group's last death.
	lastDeath := g.opts.StartAt
	key := groupKey(p)
	for _, other := range g.points {
		if groupKey(other.def) == key && other.lastDeath > lastDeath {
			lastDeath = other.lastDeath
		}
	}

	g.points[p.Name] = &pointState{
		def:       p,
		alive:     make(map[string]Spawn),
		lastTake:  g.opts.StartAt,
		lastDeath: lastDeath,
	}
	return nil
}

// Kill removes one living spawn and restarts the shared group timer.
func (g *SpawnPointGuard) Kill(point, spawnID string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	ps, ok := g.points[point]
	if !ok {
		return ErrUnknownPoint
	}
	if _, ok := ps.alive[spawnID]; !ok {
		return ErrUnknownSpawn
	}

	// The slot release happens in the same critical section as every density
	// read, so Kill and Density can never observe a half-released quota.
	delete(ps.alive, spawnID)

	key := groupKey(ps.def)
	for _, other := range g.points {
		if groupKey(other.def) == key && at > other.lastDeath {
			other.lastDeath = at
		}
	}
	return nil
}

// Ready reports how many spawns the point could produce right now. It never
// reserves a slot; region density is enforced separately by Take.
func (g *SpawnPointGuard) Ready(point string, at int64) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ps, ok := g.points[point]
	if !ok {
		return 0, ErrUnknownPoint
	}
	if at < baselineLocked(ps)+ps.def.Interval {
		return 0, nil
	}

	available := ps.def.Capacity - len(ps.alive)
	if available < 0 {
		return 0, nil
	}
	return available, nil
}

// Take claims up to n spawn slots and returns the ones that landed.
func (g *SpawnPointGuard) Take(point string, n int, at int64) ([]Spawn, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ps, ok := g.points[point]
	if !ok {
		return nil, ErrUnknownPoint
	}
	if n <= 0 {
		return nil, ErrInvalidCount
	}
	if at < baselineLocked(ps)+ps.def.Interval {
		return nil, ErrNotReady
	}

	// Every accepted Take establishes a new per-point timer base.
	ps.lastTake = at

	capFree := ps.def.Capacity - len(ps.alive)
	if capFree < 0 {
		capFree = 0
	}
	grant := n
	if grant > capFree {
		grant = capFree
	}

	// Region density only clips Take; Ready keeps reporting point capacity.
	if regionCap, limited := g.opts.RegionCap[ps.def.Region]; limited {
		regionFree := regionCap - g.regionDensityLocked(ps.def.Region)
		if regionFree < 0 {
			regionFree = 0
		}
		capacityGrant := grant
		if grant > regionFree {
			grant = regionFree
		}
		if blocked := capacityGrant - grant; blocked > 0 {
			ps.pending += blocked
		}
	}

	if grant <= 0 {
		return nil, nil
	}

	produced := make([]Spawn, 0, grant)
	for i := 0; i < grant; i++ {
		ps.seq++
		s := Spawn{
			ID:    fmt.Sprintf("%s-%d", point, ps.seq),
			Point: point,
			Phase: g.phase,
			At:    at,
		}
		// Inserted into the single shared quota table before returning, so
		// concurrent Takes can never hand the same slot to two callers.
		ps.alive[s.ID] = s
		produced = append(produced, s)
	}
	return produced, nil
}

// SetPhase switches the active phase and drops queued spawn counts.
func (g *SpawnPointGuard) SetPhase(phase string) error {
	if phase == "" {
		return ErrInvalidPhase
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.phase = phase
	for _, ps := range g.points {
		ps.pending = 0
	}
	return nil
}

// Density reports the living spawn count of one region.
func (g *SpawnPointGuard) Density(region string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.regionDensityLocked(region)
}

func (g *SpawnPointGuard) regionDensityLocked(region string) int {
	total := 0
	for _, ps := range g.points {
		if ps.def.Region == region {
			total += len(ps.alive)
		}
	}
	return total
}

// Pending reports the queued spawn count of one point.
func (g *SpawnPointGuard) Pending(point string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ps, ok := g.points[point]; ok {
		return ps.pending
	}
	return 0
}

// Phase reports the active phase name.
func (g *SpawnPointGuard) Phase() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phase
}

type pointSnapshot struct {
	Def       Point            `json:"def"`
	Alive     map[string]Spawn `json:"alive"`
	LastTake  int64            `json:"last_take"`
	LastDeath int64            `json:"last_death"`
	Pending   int              `json:"pending"`
	Seq       int              `json:"seq"`
}

type guardSnapshot struct {
	Version int             `json:"version"`
	Opts    Options         `json:"opts"`
	Phase   string          `json:"phase"`
	Points  []pointSnapshot `json:"points"`
}

// Snapshot serialises points, timers, living spawns, queues and phase.
func (g *SpawnPointGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	doc := guardSnapshot{
		Version: snapshotVersion,
		Opts:    g.opts,
		Phase:   g.phase,
		Points:  make([]pointSnapshot, 0, len(g.points)),
	}
	for _, ps := range g.points {
		alive := make(map[string]Spawn, len(ps.alive))
		for id, s := range ps.alive {
			alive[id] = s
		}
		doc.Points = append(doc.Points, pointSnapshot{
			Def:       ps.def,
			Alive:     alive,
			LastTake:  ps.lastTake,
			LastDeath: ps.lastDeath,
			Pending:   ps.pending,
			Seq:       ps.seq,
		})
	}
	sort.Slice(doc.Points, func(i, j int) bool {
		return doc.Points[i].Def.Name < doc.Points[j].Def.Name
	})
	return json.Marshal(doc)
}

// Restore replaces the guard state from a snapshot. Malformed payloads return
// ErrBadSnapshot without touching the current state.
func (g *SpawnPointGuard) Restore(data []byte) error {
	decoded, err := decodeSnapshot(data)
	if err != nil {
		return ErrBadSnapshot
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.opts = decoded.opts
	g.phase = decoded.phase
	// Never rewind an in-process id high-water mark, even when restoring an
	// older snapshot; a fresh process starts from the persisted mark.
	for name, ps := range decoded.points {
		if old := g.points[name]; old != nil && old.seq > ps.seq {
			ps.seq = old.seq
		}
	}
	g.points = decoded.points
	return nil
}

// decodedSnapshot is the validated, ready-to-install restore state.
type decodedSnapshot struct {
	opts   Options
	phase  string
	points map[string]*pointState
}

func decodeSnapshot(data []byte) (*decodedSnapshot, error) {
	var doc guardSnapshot
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Version != snapshotVersion || doc.Phase == "" {
		return nil, ErrBadSnapshot
	}

	points := make(map[string]*pointState, len(doc.Points))
	knownSpawnIDs := make(map[string]struct{})

	for _, pd := range doc.Points {
		p := pd.Def
		if p.Name == "" || p.Interval <= 0 || p.Capacity <= 0 {
			return nil, ErrBadSnapshot
		}
		if _, dup := points[p.Name]; dup {
			return nil, ErrBadSnapshot
		}
		if pd.Pending < 0 || pd.Seq < 0 || len(pd.Alive) > p.Capacity {
			return nil, ErrBadSnapshot
		}

		alive := make(map[string]Spawn, len(pd.Alive))
		for id, s := range pd.Alive {
			if id == "" || s.ID != id || s.Point != p.Name {
				return nil, ErrBadSnapshot
			}
			if _, dup := knownSpawnIDs[id]; dup {
				return nil, ErrBadSnapshot
			}
			knownSpawnIDs[id] = struct{}{}
			alive[id] = s
		}

		points[p.Name] = &pointState{
			def:       p,
			alive:     alive,
			lastTake:  pd.LastTake,
			lastDeath: pd.LastDeath,
			pending:   pd.Pending,
			seq:       pd.Seq,
		}
	}

	// Re-assert the shared group death high-water mark across members.
	groupDeath := make(map[string]int64)
	groupSeen := make(map[string]bool)
	for _, ps := range points {
		key := groupKey(ps.def)
		if !groupSeen[key] || ps.lastDeath > groupDeath[key] {
			groupDeath[key] = ps.lastDeath
			groupSeen[key] = true
		}
	}
	for _, ps := range points {
		ps.lastDeath = groupDeath[groupKey(ps.def)]
	}

	return &decodedSnapshot{opts: doc.Opts, phase: doc.Phase, points: points}, nil
}
