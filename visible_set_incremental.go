// Package task maintains the visible object set of every spatial cell and
// hands filtered deltas to its subscribers.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// ObjectID identifies one object of the scene.
type ObjectID uint32

// AllMask matches every class bit.
const AllMask uint64 = ^uint64(0)

// Defaults used when a limit is left at zero.
const (
	DefaultMaxCells          = 4096
	DefaultMaxSubs           = 512
	DefaultMaxObjectsPerCell = 2048
)

// Errors reported by the index.
var (
	ErrBadCell      = errors.New("cell id is required")
	ErrBadSub       = errors.New("subscriber id is required")
	ErrNoSuchCell   = errors.New("cell is not bound")
	ErrNoSuchSub    = errors.New("subscriber does not exist")
	ErrDuplicateSub = errors.New("subscriber already exists")
	ErrNoSuchObject = errors.New("object is unknown")
	ErrCellLimit    = errors.New("cell limit reached")
	ErrSubLimit     = errors.New("subscriber limit reached")
	ErrObjectLimit  = errors.New("object limit reached for the cell")
	ErrNotInCell    = errors.New("object is not in the source cell")
	ErrStaleVersion = errors.New("version is not the current one")
	ErrBadSnapshot  = errors.New("snapshot payload is invalid")
)

// Limits bounds the cells, subscribers and objects the index accepts.
type Limits struct {
	MaxCells          int
	MaxSubs           int
	MaxObjectsPerCell int
}

// SubState is the caller facing view of one subscription.
type SubState struct {
	ID      string   `json:"id"`
	Cells   []string `json:"cells"`
	Filter  uint64   `json:"filter"`
	Version uint64   `json:"version"`
}

// VisibleSetIndex owns the cell object lists and the subscriptions.
type VisibleSetIndex struct {
	mu     sync.Mutex
	limits Limits
}

// NewVisibleSetIndex builds an index honouring the given limits.
func NewVisibleSetIndex(limits Limits) *VisibleSetIndex {
	return &VisibleSetIndex{limits: limits}
}

// Subscribe registers a subscriber for the given cells.
func (x *VisibleSetIndex) Subscribe(subID string, cells []string) error {
	return errNotImplemented
}

// Unsubscribe drops a subscriber and its pending state.
func (x *VisibleSetIndex) Unsubscribe(subID string) error {
	return errNotImplemented
}

// Bind replaces the object list of one cell.
func (x *VisibleSetIndex) Bind(cellID string, objects []ObjectID) error {
	return errNotImplemented
}

// Classify assigns the class mask of one object.
func (x *VisibleSetIndex) Classify(objID ObjectID, mask uint64) error {
	return errNotImplemented
}

// Mask reads the class mask of one object.
func (x *VisibleSetIndex) Mask(objID ObjectID) (uint64, bool) {
	return 0, false
}

// Move moves one object between two bound cells.
func (x *VisibleSetIndex) Move(objID ObjectID, fromCell, toCell string) error {
	return errNotImplemented
}

// SetFilter replaces the class filter of one subscriber.
func (x *VisibleSetIndex) SetFilter(subID string, mask uint64) (uint64, error) {
	return 0, errNotImplemented
}

// Diff reports the delta between the acked baseline and the current set.
func (x *VisibleSetIndex) Diff(subID string) (added, removed []ObjectID, err error) {
	return nil, nil, errNotImplemented
}

// Ack confirms the subscriber saw the given version.
func (x *VisibleSetIndex) Ack(subID string, version uint64) error {
	return errNotImplemented
}

// Version reports the current version of one subscription.
func (x *VisibleSetIndex) Version(subID string) uint64 { return 0 }

// Cells lists every bound cell in ascending order.
func (x *VisibleSetIndex) Cells() []string { return nil }

// Objects lists the objects of one cell in ascending order.
func (x *VisibleSetIndex) Objects(cellID string) []ObjectID { return nil }

// Subscriber reads the view of one subscription.
func (x *VisibleSetIndex) Subscriber(subID string) (SubState, bool) {
	return SubState{}, false
}

// Snapshot serialises the cells, masks, subscriptions and baselines.
func (x *VisibleSetIndex) Snapshot() ([]byte, error) { return nil, errNotImplemented }

// Restore replaces the whole index with a snapshot, all or nothing.
func (x *VisibleSetIndex) Restore(data []byte) error { return errNotImplemented }
