// Package task keeps seal flags for shared memory files and rejects writes or
// resizes that a seal combination no longer allows.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// SealFlags is a bit set of restrictions that only ever grows.
type SealFlags uint32

// Individual seal bits.
const (
	SealNoWrite SealFlags = 1 << iota
	SealNoGrow
	SealNoShrink
	SealNoAppend
)

// Errors reported by the guard.
var (
	ErrInvalidName     = errors.New("file name is incomplete")
	ErrInvalidSize     = errors.New("file size is out of range")
	ErrNameTaken       = errors.New("file name already exists")
	ErrFileLimit       = errors.New("file limit reached")
	ErrNoSuchFile      = errors.New("sealed file not found")
	ErrSealBits        = errors.New("unknown seal bits")
	ErrSealViolation   = errors.New("operation blocked by a seal")
	ErrWriteRange      = errors.New("write range is outside the file")
	ErrSnapshotDamaged = errors.New("seal snapshot is damaged")
)

// Sealed is one mapped shared memory file handle.
type Sealed struct {
	Name  string    `json:"name"`
	Size  int       `json:"size"`
	Seals SealFlags `json:"seals"`
	guard *FileSealGuard
}

// Options configures file count and size ceilings.
type Options struct {
	MaxFiles int `json:"max_files"`
	MaxSize  int `json:"max_size"`
}

// FileSealGuard owns every sealed file and its flag set.
type FileSealGuard struct {
	opts Options
	mu   sync.RWMutex
}

// NewFileSealGuard builds a guard with the given ceilings.
func NewFileSealGuard(opts Options) *FileSealGuard {
	return &FileSealGuard{opts: opts}
}

// Create registers a new zero filled file of the given size.
func (g *FileSealGuard) Create(name string, size int) (*Sealed, error) {
	return nil, errNotImplemented
}

// Seal adds restriction bits to a file; bits can never be removed.
func (g *FileSealGuard) Seal(name string, flags SealFlags) error {
	return errNotImplemented
}

// Seals returns the current restriction bits for a file.
func (g *FileSealGuard) Seals(name string) (SealFlags, error) {
	return 0, errNotImplemented
}

// Write stores data at an offset, appending when the offset equals the size.
func (g *FileSealGuard) Write(name string, off int, data []byte) error {
	return errNotImplemented
}

// Truncate resizes a file subject to the grow and shrink seals.
func (g *FileSealGuard) Truncate(name string, size int) error {
	return errNotImplemented
}

// Release unmaps a file and forgets its seal state.
func (g *FileSealGuard) Release(name string) error {
	return errNotImplemented
}

// Snapshot serialises names, sizes, contents and seal flags.
func (g *FileSealGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole sealed file table from a snapshot.
func (g *FileSealGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Size returns the current byte length of a file.
func (g *FileSealGuard) Size(name string) (int, error) {
	return 0, errNotImplemented
}

// Contents copies the whole file payload.
func (g *FileSealGuard) Contents(name string) ([]byte, error) {
	return nil, errNotImplemented
}

// Files lists registered file names in stable order.
func (g *FileSealGuard) Files() []string {
	return nil
}

// Write routes a write through the handle that created the file.
func (s *Sealed) Write(off int, data []byte) error {
	return errNotImplemented
}
