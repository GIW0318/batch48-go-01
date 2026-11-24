// Package task keeps the light to cluster index of a clustered forward
// renderer up to date while lights move between frames.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Vec3 is a position in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Light is one punctual light registered with the tracker.
type Light struct {
	ID     int
	Center Vec3
	Radius float64
}

// ClusterKey addresses one cell of the cluster grid.
type ClusterKey struct {
	X int
	Y int
	Z int
}

// TrackerConfig carries the grid metrics and the per frame budgets.
type TrackerConfig struct {
	CellSize        float64
	ClusterCapacity int
	DirtyBudget     int
}

// TrackerStats is a snapshot of the tracker counters.
type TrackerStats struct {
	Lights     int
	Clusters   int
	Dirty      int
	Rebuilds   uint64
	Degraded   bool
	Generation uint64
}

// Errors reported by the tracker.
var (
	ErrLightExists  = errors.New("light id is already registered")
	ErrLightMissing = errors.New("light id is not registered")
	ErrBadRadius    = errors.New("light radius must be positive")
	ErrBadCellSize  = errors.New("cluster cell size must be positive")
	ErrBadCluster   = errors.New("cluster key is outside the configured grid")
	ErrBadBudget    = errors.New("dirty budget must be positive")
	ErrClusterFull  = errors.New("cluster already holds the maximum number of lights")
	ErrBadSnapshot  = errors.New("snapshot payload is invalid")
)

// LightClusterTracker owns the light table and the derived cluster index.
type LightClusterTracker struct {
	mu  sync.RWMutex
	cfg TrackerConfig
}

// NewLightClusterTracker builds an empty tracker for the given grid metrics.
func NewLightClusterTracker(cfg TrackerConfig) *LightClusterTracker {
	return &LightClusterTracker{cfg: cfg}
}

// AddLight registers one light and returns the id it was stored under.
func (t *LightClusterTracker) AddLight(light Light) (int, error) {
	return 0, errNotImplemented
}

// MoveLight replaces the center and radius of one registered light.
func (t *LightClusterTracker) MoveLight(id int, center Vec3, radius float64) error {
	return errNotImplemented
}

// RemoveLight unregisters one light from every cluster it touches.
func (t *LightClusterTracker) RemoveLight(id int) error {
	return errNotImplemented
}

// ClusterAt returns the sorted light ids and generation of one cluster.
func (t *LightClusterTracker) ClusterAt(key ClusterKey) ([]int, uint64) {
	return nil, 0
}

// ClusterOf maps a world position onto its cluster cell.
func (t *LightClusterTracker) ClusterOf(point Vec3) (ClusterKey, error) {
	return ClusterKey{}, errNotImplemented
}

// Rebuild recomputes the whole index and advances every generation.
func (t *LightClusterTracker) Rebuild() error {
	return errNotImplemented
}

// SetDirtyBudget changes how many clusters one update may recompute.
func (t *LightClusterTracker) SetDirtyBudget(n int) error {
	return errNotImplemented
}

// ClearDirty drops the dirty set accumulated since the previous clear.
func (t *LightClusterTracker) ClearDirty() error {
	return errNotImplemented
}

// DirtyClusters lists the clusters touched since the last clear.
func (t *LightClusterTracker) DirtyClusters() []ClusterKey {
	return nil
}

// Generation reports the current generation of one cluster.
func (t *LightClusterTracker) Generation(key ClusterKey) uint64 {
	return 0
}

// Stats reports the live counters of the tracker.
func (t *LightClusterTracker) Stats() TrackerStats {
	return TrackerStats{}
}

// Snapshot serialises the light table, the cluster index and generations.
func (t *LightClusterTracker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the tracker from a snapshot, either completely or not at all.
func (t *LightClusterTracker) Restore(payload []byte) error {
	return errNotImplemented
}

// Reset clears the lights, the dirty set and the counters.
func (t *LightClusterTracker) Reset() error {
	return errNotImplemented
}
