// Package task schedules texture mip uploads against a per frame bandwidth
// budget and keeps the resident mip level of every texture monotonic.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

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

// MipStreamScheduler orders mip uploads by priority inside a byte budget.
type MipStreamScheduler struct {
	maxLevels int
}

// NewMipStreamScheduler returns a scheduler capped at maxLevels mip levels.
func NewMipStreamScheduler(maxLevels int) *MipStreamScheduler {
	return &MipStreamScheduler{maxLevels: maxLevels}
}

// SetMaxLevels changes the hard cap of mip levels per texture.
func (s *MipStreamScheduler) SetMaxLevels(n int) error { return errNotImplemented }

// RegisterTexture records the dimensions of a texture and its mip chain.
func (s *MipStreamScheduler) RegisterTexture(texID, width, height int) error {
	return errNotImplemented
}

// TextureLevels returns the number of mip levels of a registered texture.
func (s *MipStreamScheduler) TextureLevels(texID int) (int, error) { return 0, errNotImplemented }

// MipBytes returns the upload size of one mip level in bytes.
func (s *MipStreamScheduler) MipBytes(texID, mip int) (int, error) { return 0, errNotImplemented }

// Request asks for one mip level to be uploaded.
func (s *MipStreamScheduler) Request(texID, mip int, priority float64) error {
	return errNotImplemented
}

// Drain selects the uploads that fit into the given byte budget of one frame.
func (s *MipStreamScheduler) Drain(budget int) ([]Upload, error) { return nil, errNotImplemented }

// Cancel drops a pending mip request.
func (s *MipStreamScheduler) Cancel(texID, mip int) error { return errNotImplemented }

// Resident returns the highest resident mip level, or -1 when none is resident.
func (s *MipStreamScheduler) Resident(texID int) (int, error) { return 0, errNotImplemented }

// PendingLevels returns the pending mip levels of a texture in ascending order.
func (s *MipStreamScheduler) PendingLevels(texID int) ([]int, error) {
	return nil, errNotImplemented
}

// PendingCount returns the number of queued mip requests.
func (s *MipStreamScheduler) PendingCount() int { return 0 }

// Frame returns how many Drain calls have completed.
func (s *MipStreamScheduler) Frame() uint64 { return 0 }

// SetMaxPending changes the hard limit of queued mip requests.
func (s *MipStreamScheduler) SetMaxPending(n int) error { return errNotImplemented }

// Stats returns a consistent snapshot of the scheduler counters.
func (s *MipStreamScheduler) Stats() SchedulerStats { return SchedulerStats{} }
