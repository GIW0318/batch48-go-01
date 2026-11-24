package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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
	return &SpawnPointGuard{opts: opts, phase: "default", points: make(map[string]*pointState)}
}

// AddPoint registers one spawn point definition.
func (g *SpawnPointGuard) AddPoint(p Point) error {
	return errNotImplemented
}

// Kill removes one living spawn and restarts the refill timer.
func (g *SpawnPointGuard) Kill(point, spawnID string, at int64) error {
	return errNotImplemented
}

// Ready reports how many spawns the point could produce right now.
func (g *SpawnPointGuard) Ready(point string, at int64) (int, error) {
	return 0, errNotImplemented
}

// Take claims up to n spawn slots and returns the ones that landed.
func (g *SpawnPointGuard) Take(point string, n int, at int64) ([]Spawn, error) {
	return nil, errNotImplemented
}

// SetPhase switches the active phase and drops old phase queues.
func (g *SpawnPointGuard) SetPhase(phase string) error {
	return errNotImplemented
}

// Density reports the living spawn count of one region.
func (g *SpawnPointGuard) Density(region string) int {
	return 0
}

// Pending reports the queued spawn count of one point.
func (g *SpawnPointGuard) Pending(point string) int {
	return 0
}

// Phase reports the active phase name.
func (g *SpawnPointGuard) Phase() string {
	return ""
}

// Snapshot serialises points, timers, living spawns and phase.
func (g *SpawnPointGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot.
func (g *SpawnPointGuard) Restore(data []byte) error {
	return errNotImplemented
}
