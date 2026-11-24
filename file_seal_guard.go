// Package task keeps seal flags for shared memory files and rejects writes or
// resizes that a seal combination no longer allows.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// SealFlags is a bit set of restrictions that only ever grows.
type SealFlags uint32

// Individual seal bits.
const (
	SealNoWrite SealFlags = 1 << iota
	SealNoGrow
	SealNoShrink
	SealNoAppend
)

// knownSealFlags is the union of every seal bit understood by this build.
const knownSealFlags = SealNoWrite | SealNoGrow | SealNoShrink | SealNoAppend

// Zero Options fall back to generous built-in ceilings so every guard still
// enforces a hard file count and a hard per-file size cap.
const (
	defaultMaxFiles = 1 << 20
	defaultMaxSize  = 1 << 30
	snapshotVersion = 1
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
	opts  Options
	mu    sync.RWMutex
	files map[string]*sealFile
}

// sealFile is the per-file shared state. mu serialises Write and Truncate on
// the same file; read-only observers take the read lock.
type sealFile struct {
	mu    sync.RWMutex
	name  string
	data  []byte
	seals SealFlags
}

// snapshotEnvelope is the persisted form of a guard. Seal flags travel next
// to names, sizes and contents so a restart never silently drops a seal.
type snapshotEnvelope struct {
	Version int            `json:"version"`
	Files   []snapshotFile `json:"files"`
}

type snapshotFile struct {
	Name     string    `json:"name"`
	Size     int       `json:"size"`
	Seals    SealFlags `json:"seals"`
	Contents []byte    `json:"contents"`
}

// NewFileSealGuard builds a guard with the given ceilings.
func NewFileSealGuard(opts Options) *FileSealGuard {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = defaultMaxFiles
	}
	if opts.MaxSize <= 0 {
		opts.MaxSize = defaultMaxSize
	}
	return &FileSealGuard{
		opts:  opts,
		files: make(map[string]*sealFile),
	}
}

// Create registers a new zero filled file of the given size.
func (g *FileSealGuard) Create(name string, size int) (*Sealed, error) {
	if name == "" {
		return nil, ErrInvalidName
	}
	if size < 0 || size > g.opts.MaxSize {
		return nil, ErrInvalidSize
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.files[name]; exists {
		return nil, ErrNameTaken
	}
	if len(g.files) >= g.opts.MaxFiles {
		return nil, ErrFileLimit
	}

	g.files[name] = &sealFile{
		name: name,
		data: make([]byte, size),
	}

	return &Sealed{
		Name:  name,
		Size:  size,
		Seals: 0,
		guard: g,
	}, nil
}

// Seal adds restriction bits to a file; bits can never be removed.
func (g *FileSealGuard) Seal(name string, flags SealFlags) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return err
	}

	file.mu.Lock()
	defer file.mu.Unlock()

	if flags&^knownSealFlags != 0 {
		return ErrSealBits
	}

	file.seals |= flags
	return nil
}

// Seals returns the current restriction bits for a file.
func (g *FileSealGuard) Seals(name string) (SealFlags, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return 0, err
	}

	file.mu.RLock()
	defer file.mu.RUnlock()
	return file.seals, nil
}

// Write stores data at an offset, appending when the offset equals the size.
func (g *FileSealGuard) Write(name string, off int, data []byte) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return err
	}

	file.mu.Lock()
	defer file.mu.Unlock()

	size := len(file.data)
	if off < 0 || off > size {
		return ErrWriteRange
	}
	if len(data) == 0 {
		return nil
	}

	if off < size {
		if file.seals&SealNoWrite != 0 {
			return ErrSealViolation
		}
		copy(file.data[off:], data)
		return nil
	}

	// off == size makes this an append, which also grows the file. The
	// grow and append seals both veto it; OR-composition means no later
	// seal call can weaken an earlier denial.
	if file.seals&(SealNoGrow|SealNoAppend) != 0 {
		return ErrSealViolation
	}
	if len(data) > g.opts.MaxSize-size {
		return ErrInvalidSize
	}

	file.data = append(file.data, data...)
	return nil
}

// Truncate resizes a file subject to the grow and shrink seals.
func (g *FileSealGuard) Truncate(name string, size int) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return err
	}

	file.mu.Lock()
	defer file.mu.Unlock()

	if size < 0 || size > g.opts.MaxSize {
		return ErrInvalidSize
	}

	current := len(file.data)
	if size == current {
		return nil
	}

	if size > current {
		if file.seals&SealNoGrow != 0 {
			return ErrSealViolation
		}
		file.data = append(file.data, make([]byte, size-current)...)
		return nil
	}

	if file.seals&SealNoShrink != 0 {
		return ErrSealViolation
	}
	// Zero the discarded tail so a later growth cannot resurrect old bytes.
	clear(file.data[size:current])
	file.data = file.data[:size]
	return nil
}

// Release unmaps a file and forgets its seal state.
func (g *FileSealGuard) Release(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.files[name]; !ok {
		return ErrNoSuchFile
	}
	delete(g.files, name)
	return nil
}

// Snapshot serialises names, sizes, contents and seal flags.
func (g *FileSealGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()

	names := make([]string, 0, len(g.files))
	for name := range g.files {
		names = append(names, name)
	}
	sort.Strings(names)

	envelope := snapshotEnvelope{
		Version: snapshotVersion,
		Files:   make([]snapshotFile, 0, len(names)),
	}
	for _, name := range names {
		file := g.files[name]
		file.mu.RLock()
		envelope.Files = append(envelope.Files, snapshotFile{
			Name:     file.name,
			Size:     len(file.data),
			Seals:    file.seals,
			Contents: append([]byte(nil), file.data...),
		})
		file.mu.RUnlock()
	}
	g.mu.RUnlock()

	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// Restore replaces the whole sealed file table from a snapshot. The payload
// is fully parsed and validated before any live state is touched, so a
// damaged snapshot leaves the running guard untouched.
func (g *FileSealGuard) Restore(payload []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ErrSnapshotDamaged
	}
	if envelope.Version != snapshotVersion {
		return ErrSnapshotDamaged
	}

	restored := make(map[string]*sealFile, len(envelope.Files))
	for _, item := range envelope.Files {
		if item.Name == "" {
			return ErrSnapshotDamaged
		}
		if item.Size < 0 || item.Size > g.opts.MaxSize {
			return ErrSnapshotDamaged
		}
		if len(item.Contents) != item.Size {
			return ErrSnapshotDamaged
		}
		if item.Seals&^knownSealFlags != 0 {
			return ErrSnapshotDamaged
		}
		if _, duplicate := restored[item.Name]; duplicate {
			return ErrSnapshotDamaged
		}
		if len(restored) >= g.opts.MaxFiles {
			return ErrSnapshotDamaged
		}

		restored[item.Name] = &sealFile{
			name:  item.Name,
			data:  append([]byte(nil), item.Contents...),
			seals: item.Seals,
		}
	}

	g.mu.Lock()
	g.files = restored
	g.mu.Unlock()
	return nil
}

// Size returns the current byte length of a file.
func (g *FileSealGuard) Size(name string) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return 0, err
	}

	file.mu.RLock()
	defer file.mu.RUnlock()
	return len(file.data), nil
}

// Contents copies the whole file payload.
func (g *FileSealGuard) Contents(name string) ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	file, err := g.lookup(name)
	if err != nil {
		return nil, err
	}

	file.mu.RLock()
	defer file.mu.RUnlock()
	return append([]byte{}, file.data...), nil
}

// Files lists registered file names in stable order.
func (g *FileSealGuard) Files() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.files))
	for name := range g.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Write routes a write through the handle that created the file, so seal
// state always comes from the shared guard rather than the handle.
func (s *Sealed) Write(off int, data []byte) error {
	if s == nil || s.guard == nil {
		return ErrNoSuchFile
	}
	return s.guard.Write(s.Name, off, data)
}

// lookup resolves a live file while the table read lock is held.
func (g *FileSealGuard) lookup(name string) (*sealFile, error) {
	file, ok := g.files[name]
	if !ok {
		return nil, ErrNoSuchFile
	}
	return file, nil
}
