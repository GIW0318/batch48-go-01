// Package task schedules texture mip uploads against a per frame bandwidth
// budget and keeps the resident mip level of every texture monotonic.
package task

import (
	"errors"
	"math"
	"math/bits"
	"sort"
	"sync"
)

// Errors reported by the mip stream scheduler.
var (
	ErrBadDimensions   = errors.New("texture dimensions must be positive")
	ErrTextureExists   = errors.New("texture already registered with other dimensions")
	ErrUnknownTexture  = errors.New("texture is not registered")
	ErrBadMip          = errors.New("mip level is out of range")
	ErrBadPriority     = errors.New("priority must be finite and non-negative")
	ErrBadBudget       = errors.New("frame budget must be positive")
	ErrBadLimit        = errors.New("limit must be positive")
	ErrAlreadyResident = errors.New("mip level is already resident")
	ErrNotPending      = errors.New("mip level is not pending")
	ErrQueueFull       = errors.New("pending queue is full")
)

// AgingStep is the priority a pending request gains for every skipped frame.
const AgingStep = 0.5

const defaultMaxPending = 1 << 16

// Upload is one mip upload selected by a Drain call.
type Upload struct {
	TexID      int
	Mip        int
	Bytes      int
	Priority   float64
	WaitFrames int
}

// SchedulerStats is a point in time summary of the scheduler.
type SchedulerStats struct {
	Textures int
	Pending  int
	Uploaded int
	Canceled int
}

type textureState struct {
	width    int
	height   int
	levels   int
	resident int
}

type requestKey struct {
	texID int
	mip   int
}

type pendingRequest struct {
	key      requestKey
	priority float64
	wait     int
}

// MipStreamScheduler orders mip uploads by priority inside a byte budget.
type MipStreamScheduler struct {
	mu         sync.Mutex
	maxLevels  int
	maxPending int
	textures   map[int]*textureState
	pending    map[requestKey]*pendingRequest
	frame      uint64
	uploaded   int
	canceled   int
}

// NewMipStreamScheduler returns a scheduler capped at maxLevels mip levels.
func NewMipStreamScheduler(maxLevels int) *MipStreamScheduler {
	if maxLevels < 1 {
		maxLevels = 1
	}
	return &MipStreamScheduler{
		maxLevels:  maxLevels,
		maxPending: defaultMaxPending,
		textures:   make(map[int]*textureState),
		pending:    make(map[requestKey]*pendingRequest),
	}
}

// SetMaxLevels changes the hard cap of mip levels per texture.
func (s *MipStreamScheduler) SetMaxLevels(n int) error {
	if n < 1 {
		return ErrBadLimit
	}
	s.mu.Lock()
	s.maxLevels = n
	s.mu.Unlock()
	return nil
}

// RegisterTexture records the dimensions of a texture and its mip chain.
func (s *MipStreamScheduler) RegisterTexture(texID, width, height int) error {
	if width < 1 || height < 1 {
		return ErrBadDimensions
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if tex, ok := s.textures[texID]; ok {
		if tex.width == width && tex.height == height {
			return nil
		}
		return ErrTextureExists
	}

	largest := width
	if height > largest {
		largest = height
	}
	levels := bits.Len(uint(largest))
	if levels > s.maxLevels {
		levels = s.maxLevels
	}

	s.textures[texID] = &textureState{
		width:    width,
		height:   height,
		levels:   levels,
		resident: -1,
	}
	return nil
}

// TextureLevels returns the number of mip levels of a registered texture.
func (s *MipStreamScheduler) TextureLevels(texID int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tex, ok := s.textures[texID]
	if !ok {
		return 0, ErrUnknownTexture
	}
	return tex.levels, nil
}

// MipBytes returns the upload size of one mip level in bytes.
func (s *MipStreamScheduler) MipBytes(texID, mip int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mipBytesLocked(texID, mip)
}

func (s *MipStreamScheduler) mipBytesLocked(texID, mip int) (int, error) {
	tex, ok := s.textures[texID]
	if !ok {
		return 0, ErrUnknownTexture
	}
	if mip < 0 || mip >= tex.levels {
		return 0, ErrBadMip
	}
	return mipSize(tex.width, tex.height, mip), nil
}

func mipSize(width, height, mip int) int {
	w := width >> uint(mip)
	h := height >> uint(mip)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w * h * 4
}

// Request asks for one mip level to be uploaded.
func (s *MipStreamScheduler) Request(texID, mip int, priority float64) error {
	if math.IsNaN(priority) || math.IsInf(priority, 0) || priority < 0 {
		return ErrBadPriority
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tex, ok := s.textures[texID]
	if !ok {
		return ErrUnknownTexture
	}
	if mip < 0 || mip >= tex.levels {
		return ErrBadMip
	}
	if mip <= tex.resident {
		return ErrAlreadyResident
	}

	key := requestKey{texID: texID, mip: mip}
	if req, ok := s.pending[key]; ok {
		if priority > req.priority {
			req.priority = priority
		}
		return nil
	}
	if len(s.pending) >= s.maxPending {
		return ErrQueueFull
	}

	s.pending[key] = &pendingRequest{
		key:      key,
		priority: priority,
	}
	return nil
}

// Drain selects the uploads that fit into the given byte budget of one frame.
func (s *MipStreamScheduler) Drain(budget int) ([]Upload, error) {
	if budget <= 0 {
		return nil, ErrBadBudget
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	type candidate struct {
		req       *pendingRequest
		effective float64
		bytes     int
	}

	candidates := make([]candidate, 0, len(s.pending))
	for _, req := range s.pending {
		tex := s.textures[req.key.texID]
		if req.key.mip != 0 && tex.resident < req.key.mip-1 {
			continue
		}
		candidates = append(candidates, candidate{
			req:       req,
			effective: req.priority + float64(req.wait)*AgingStep,
			bytes:     mipSize(tex.width, tex.height, req.key.mip),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.effective != b.effective {
			return a.effective > b.effective
		}
		if a.req.key.texID != b.req.key.texID {
			return a.req.key.texID < b.req.key.texID
		}
		return a.req.key.mip < b.req.key.mip
	})

	remaining := budget
	uploads := make([]Upload, 0, len(candidates))
	for _, cand := range candidates {
		if cand.bytes > remaining {
			continue
		}
		remaining -= cand.bytes
		uploads = append(uploads, Upload{
			TexID:      cand.req.key.texID,
			Mip:        cand.req.key.mip,
			Bytes:      cand.bytes,
			Priority:   cand.effective,
			WaitFrames: cand.req.wait,
		})
		delete(s.pending, cand.req.key)
	}

	for _, req := range s.pending {
		req.wait++
	}

	for _, upload := range uploads {
		tex := s.textures[upload.TexID]
		if upload.Mip > tex.resident {
			tex.resident = upload.Mip
		}
	}

	s.frame++
	s.uploaded += len(uploads)
	return uploads, nil
}

// Cancel drops a pending mip request.
func (s *MipStreamScheduler) Cancel(texID, mip int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tex, ok := s.textures[texID]
	if !ok {
		return ErrUnknownTexture
	}
	if mip < 0 || mip >= tex.levels {
		return ErrBadMip
	}
	if mip <= tex.resident {
		return ErrAlreadyResident
	}

	key := requestKey{texID: texID, mip: mip}
	if _, ok := s.pending[key]; !ok {
		return ErrNotPending
	}
	delete(s.pending, key)
	s.canceled++
	return nil
}

// Resident returns the highest resident mip level, or -1 when none is resident.
func (s *MipStreamScheduler) Resident(texID int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tex, ok := s.textures[texID]
	if !ok {
		return 0, ErrUnknownTexture
	}
	return tex.resident, nil
}

// PendingLevels returns the pending mip levels of a texture in ascending order.
func (s *MipStreamScheduler) PendingLevels(texID int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.textures[texID]; !ok {
		return nil, ErrUnknownTexture
	}

	levels := make([]int, 0)
	for key := range s.pending {
		if key.texID == texID {
			levels = append(levels, key.mip)
		}
	}
	sort.Ints(levels)
	return levels, nil
}

// PendingCount returns the number of queued mip requests.
func (s *MipStreamScheduler) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Frame returns how many Drain calls have completed.
func (s *MipStreamScheduler) Frame() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

// SetMaxPending changes the hard limit of queued mip requests.
func (s *MipStreamScheduler) SetMaxPending(n int) error {
	if n < 1 {
		return ErrBadLimit
	}
	s.mu.Lock()
	s.maxPending = n
	s.mu.Unlock()
	return nil
}

// Stats returns a consistent snapshot of the scheduler counters.
func (s *MipStreamScheduler) Stats() SchedulerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SchedulerStats{
		Textures: len(s.textures),
		Pending:  len(s.pending),
		Uploaded: s.uploaded,
		Canceled: s.canceled,
	}
}
