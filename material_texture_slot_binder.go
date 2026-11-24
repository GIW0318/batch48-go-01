// Package task binds material texture and sampler pairs to physical texture
// slots and resolves the bindings back for the draw pass.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the slot binder.
var (
	ErrEmptyMaterial      = errors.New("material id is required")
	ErrEmptySlotName      = errors.New("slot name is required")
	ErrEmptyTexture       = errors.New("texture name is required")
	ErrBadSampler         = errors.New("sampler state is invalid")
	ErrBadSlotLimit       = errors.New("slot limit must be positive")
	ErrSlotLimitBelowLive = errors.New("slot limit is below the live slot count")
	ErrBadMaterialLimit   = errors.New("material limit must be positive")
	ErrMaterialLimit      = errors.New("material sampler limit reached")
	ErrSlotNotBound       = errors.New("slot is not bound for the material")
	ErrDuplicateSlotName  = errors.New("slot name is already bound")
)

// Sampler is the fixed sampler state stored next to a texture binding.
type Sampler struct {
	WrapU      string
	WrapV      string
	Filter     string
	Anisotropy int
}

// Slot is one named texture binding requested by a material.
type Slot struct {
	Name    string
	Texture string
	Sampler Sampler
}

// BoundSlot is the resolved state of one material slot.
type BoundSlot struct {
	Name       string
	Texture    string
	Sampler    Sampler
	Slot       int
	Degraded   bool
	Generation uint64
}

// BinderStats is a point in time summary of the binder.
type BinderStats struct {
	PhysicalSlots     int
	LiveBindings      int
	PendingReclaim    int
	DegradedMaterials int
}

// TextureSlotBinder assigns material texture bindings to physical slots.
type TextureSlotBinder struct {
	defaultTexture string
	slotLimit      int
	materialLimit  int
}

// NewTextureSlotBinder returns a binder that falls back to defaultTexture.
func NewTextureSlotBinder(defaultTexture string, slotLimit int) *TextureSlotBinder {
	return &TextureSlotBinder{defaultTexture: defaultTexture, slotLimit: slotLimit, materialLimit: 8}
}

// SetSlotLimit changes the hard limit of live physical slots.
func (b *TextureSlotBinder) SetSlotLimit(n int) error { return errNotImplemented }

// SetMaterialLimit changes the hard limit of slots one material may bind.
func (b *TextureSlotBinder) SetMaterialLimit(n int) error { return errNotImplemented }

// Bind binds one named texture slot for a material and returns the physical slot.
func (b *TextureSlotBinder) Bind(materialID string, s Slot) (int, error) { return 0, errNotImplemented }

// Unbind releases one material binding by its physical slot.
func (b *TextureSlotBinder) Unbind(materialID string, slot int) error { return errNotImplemented }

// Resolve returns the bindings of one material ordered by physical slot.
func (b *TextureSlotBinder) Resolve(materialID string) ([]BoundSlot, error) {
	return nil, errNotImplemented
}

// ResolveAll returns the resolved bindings of every material.
func (b *TextureSlotBinder) ResolveAll() map[string][]BoundSlot { return nil }

// SlotCount returns the number of live physical slots.
func (b *TextureSlotBinder) SlotCount() int { return 0 }

// RefCount returns how many material bindings reference a physical slot.
func (b *TextureSlotBinder) RefCount(slot int) int { return 0 }

// Degraded reports whether a material has a binding on the fallback texture.
func (b *TextureSlotBinder) Degraded(materialID string) bool { return false }

// ReloadMaterial drops every binding of a material and bumps its generation.
func (b *TextureSlotBinder) ReloadMaterial(materialID string) error { return errNotImplemented }

// Generation returns the hot reload generation of a material.
func (b *TextureSlotBinder) Generation(materialID string) uint64 { return 0 }

// EndFrame releases physical slots that were dropped but still referenced
// by the draw list of the current frame.
func (b *TextureSlotBinder) EndFrame() {}

// Stats returns a consistent snapshot of the binder counters.
func (b *TextureSlotBinder) Stats() BinderStats { return BinderStats{} }
