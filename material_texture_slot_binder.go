// Package task binds material texture and sampler pairs to physical texture
// slots and resolves the bindings back for the draw pass.
package task

import (
	"errors"
	"sort"
	"sync"
)

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

// slotKey identifies a physical slot by the exact texture and sampler state
// it serves. Every sampler field participates in the identity, so a one step
// difference in anisotropy still gets its own physical slot.
type slotKey struct {
	texture string
	sampler Sampler
}

// physicalSlot is one hardware texture slot and its live references.
type physicalSlot struct {
	texture  string
	sampler  Sampler
	refCount int
}

// binding is one named slot request owned by a material. texture keeps the
// originally requested texture even while the binding is degraded.
type binding struct {
	name     string
	texture  string
	sampler  Sampler
	slot     int
	degraded bool
}

type materialState struct {
	bindings   []*binding
	generation uint64
}

// TextureSlotBinder assigns material texture bindings to physical slots.
type TextureSlotBinder struct {
	mu sync.Mutex

	defaultTexture string
	slotLimit      int
	materialLimit  int

	slots     map[int]*physicalSlot
	slotIndex map[slotKey]int
	pending   map[int]struct{}
	materials map[string]*materialState
}

// NewTextureSlotBinder returns a binder that falls back to defaultTexture.
func NewTextureSlotBinder(defaultTexture string, slotLimit int) *TextureSlotBinder {
	return &TextureSlotBinder{
		defaultTexture: defaultTexture,
		slotLimit:      slotLimit,
		materialLimit:  8,
		slots:          make(map[int]*physicalSlot),
		slotIndex:      make(map[slotKey]int),
		pending:        make(map[int]struct{}),
		materials:      make(map[string]*materialState),
	}
}

// SetSlotLimit changes the hard limit of live physical slots.
func (b *TextureSlotBinder) SetSlotLimit(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 {
		return ErrBadSlotLimit
	}
	if n < len(b.slots) {
		return ErrSlotLimitBelowLive
	}
	b.slotLimit = n
	return nil
}

// SetMaterialLimit changes the hard limit of slots one material may bind.
func (b *TextureSlotBinder) SetMaterialLimit(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 {
		return ErrBadMaterialLimit
	}
	b.materialLimit = n
	return nil
}

// Bind binds one named texture slot for a material and returns the physical slot.
func (b *TextureSlotBinder) Bind(materialID string, s Slot) (int, error) {
	if materialID == "" {
		return 0, ErrEmptyMaterial
	}
	if s.Name == "" {
		return 0, ErrEmptySlotName
	}
	if s.Texture == "" {
		return 0, ErrEmptyTexture
	}
	if err := validateSampler(s.Sampler); err != nil {
		return 0, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	mat := b.materials[materialID]
	if mat != nil {
		for _, existing := range mat.bindings {
			if existing.name != s.Name {
				continue
			}
			if existing.texture == s.Texture && existing.sampler == s.Sampler {
				return existing.slot, nil
			}
			return 0, ErrDuplicateSlotName
		}
	} else {
		mat = &materialState{}
		b.materials[materialID] = mat
	}

	if len(mat.bindings) >= b.materialLimit {
		return 0, ErrMaterialLimit
	}

	key := slotKey{texture: s.Texture, sampler: s.Sampler}
	idx, ok := b.slotIndex[key]
	if !ok {
		idx = b.allocateSlot()
		if idx < 0 {
			mat.bindings = append(mat.bindings, &binding{
				name:     s.Name,
				texture:  s.Texture,
				sampler:  s.Sampler,
				slot:     -1,
				degraded: true,
			})
			return -1, nil
		}
		b.slots[idx] = &physicalSlot{texture: s.Texture, sampler: s.Sampler, refCount: 1}
		b.slotIndex[key] = idx
	} else {
		b.slots[idx].refCount++
	}

	mat.bindings = append(mat.bindings, &binding{
		name:    s.Name,
		texture: s.Texture,
		sampler: s.Sampler,
		slot:    idx,
	})
	return idx, nil
}

// Unbind releases one material binding by its physical slot.
func (b *TextureSlotBinder) Unbind(materialID string, slot int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	mat := b.materials[materialID]
	if mat == nil {
		return ErrSlotNotBound
	}
	for i, bd := range mat.bindings {
		if bd.slot != slot {
			continue
		}
		mat.bindings = append(mat.bindings[:i], mat.bindings[i+1:]...)
		if !bd.degraded {
			b.releaseSlot(bd.slot, false)
		}
		return nil
	}
	return ErrSlotNotBound
}

// Resolve returns the bindings of one material ordered by physical slot.
func (b *TextureSlotBinder) Resolve(materialID string) ([]BoundSlot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolveLocked(materialID), nil
}

// ResolveAll returns the resolved bindings of every material.
func (b *TextureSlotBinder) ResolveAll() map[string][]BoundSlot {
	b.mu.Lock()
	defer b.mu.Unlock()

	all := make(map[string][]BoundSlot, len(b.materials))
	for materialID := range b.materials {
		resolved := b.resolveLocked(materialID)
		if len(resolved) > 0 {
			all[materialID] = resolved
		}
	}
	return all
}

// SlotCount returns the number of live physical slots.
func (b *TextureSlotBinder) SlotCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.slots)
}

// RefCount returns how many material bindings reference a physical slot.
func (b *TextureSlotBinder) RefCount(slot int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	ps, ok := b.slots[slot]
	if !ok {
		return 0
	}
	return ps.refCount
}

// Degraded reports whether a material has a binding on the fallback texture.
func (b *TextureSlotBinder) Degraded(materialID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	mat := b.materials[materialID]
	if mat == nil {
		return false
	}
	for _, bd := range mat.bindings {
		if bd.degraded {
			return true
		}
	}
	return false
}

// ReloadMaterial drops every binding of a material and bumps its generation.
func (b *TextureSlotBinder) ReloadMaterial(materialID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	mat := b.materials[materialID]
	if mat == nil {
		b.materials[materialID] = &materialState{generation: 1}
		return nil
	}
	for _, bd := range mat.bindings {
		if !bd.degraded {
			b.releaseSlot(bd.slot, true)
		}
	}
	mat.bindings = nil
	mat.generation++
	return nil
}

// Generation returns the hot reload generation of a material.
func (b *TextureSlotBinder) Generation(materialID string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	mat := b.materials[materialID]
	if mat == nil {
		return 0
	}
	return mat.generation
}

// EndFrame releases physical slots that were dropped but still referenced
// by the draw list of the current frame.
func (b *TextureSlotBinder) EndFrame() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = make(map[int]struct{})
}

// Stats returns a consistent snapshot of the binder counters.
func (b *TextureSlotBinder) Stats() BinderStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	stats := BinderStats{
		PhysicalSlots:  len(b.slots),
		PendingReclaim: len(b.pending),
	}
	for _, mat := range b.materials {
		stats.LiveBindings += len(mat.bindings)
		for _, bd := range mat.bindings {
			if bd.degraded {
				stats.DegradedMaterials++
				break
			}
		}
	}
	return stats
}

func validateSampler(s Sampler) error {
	if !validWrap(s.WrapU) || !validWrap(s.WrapV) {
		return ErrBadSampler
	}
	switch s.Filter {
	case "nearest", "linear", "anisotropic":
	default:
		return ErrBadSampler
	}
	if s.Anisotropy < 1 || s.Anisotropy > 16 {
		return ErrBadSampler
	}
	return nil
}

func validWrap(w string) bool {
	switch w {
	case "repeat", "mirror", "clamp":
		return true
	}
	return false
}

// allocateSlot returns the smallest free index that is not held back in the
// pending reclaim list, or -1 when every physical slot is in use.
func (b *TextureSlotBinder) allocateSlot() int {
	for idx := 0; idx < b.slotLimit; idx++ {
		if _, live := b.slots[idx]; live {
			continue
		}
		if _, reclaim := b.pending[idx]; reclaim {
			continue
		}
		return idx
	}
	return -1
}

// releaseSlot drops one reference. Slots released by an explicit unbind are
// reusable immediately; slots released by a hot reload stay in the pending
// list until EndFrame so the in-flight draw list keeps valid indices.
func (b *TextureSlotBinder) releaseSlot(idx int, deferred bool) {
	ps := b.slots[idx]
	ps.refCount--
	if ps.refCount > 0 {
		return
	}
	delete(b.slotIndex, slotKey{texture: ps.texture, sampler: ps.sampler})
	delete(b.slots, idx)
	if deferred {
		b.pending[idx] = struct{}{}
	}
}

func (b *TextureSlotBinder) resolveLocked(materialID string) []BoundSlot {
	mat := b.materials[materialID]
	if mat == nil || len(mat.bindings) == 0 {
		return []BoundSlot{}
	}
	resolved := make([]BoundSlot, 0, len(mat.bindings))
	for _, bd := range mat.bindings {
		texture := bd.texture
		if bd.degraded {
			texture = b.defaultTexture
		}
		resolved = append(resolved, BoundSlot{
			Name:       bd.name,
			Texture:    texture,
			Sampler:    bd.sampler,
			Slot:       bd.slot,
			Degraded:   bd.degraded,
			Generation: mat.generation,
		})
	}
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].Slot < resolved[j].Slot
	})
	return resolved
}
