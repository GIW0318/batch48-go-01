package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

var (
	// ErrUnknownCharacter is returned for a character id that was never registered.
	ErrUnknownCharacter = errors.New("equip: unknown character")
	// ErrDuplicateCharacter is returned when a character is registered twice.
	ErrDuplicateCharacter = errors.New("equip: character already registered")
	// ErrUnknownSlot is returned for a slot outside the configured slot table.
	ErrUnknownSlot = errors.New("equip: unknown slot")
	// ErrSlotEmpty is returned when unequipping an empty slot.
	ErrSlotEmpty = errors.New("equip: slot is empty")
	// ErrLevelTooLow is returned when the item level exceeds the character level.
	ErrLevelTooLow = errors.New("equip: item level too high")
	// ErrWrongClass is returned when the item class does not match the character.
	ErrWrongClass = errors.New("equip: item class mismatch")
	// ErrOffhandBlocked is returned when the offhand is blocked by a two-handed weapon.
	ErrOffhandBlocked = errors.New("equip: offhand blocked by two-handed weapon")
	// ErrDuplicateItem is returned when the same item id is equipped twice.
	ErrDuplicateItem = errors.New("equip: item already equipped")
	// ErrBadSnapshot is returned when snapshot bytes cannot be decoded.
	ErrBadSnapshot = errors.New("equip: bad snapshot")
)

// Stats is the flat attribute block of an item or character.
type Stats struct {
	Strength int
	Agility  int
	Stamina  int
	Armor    int
}

// Item is one equippable item.
type Item struct {
	ID        string
	Slot      string
	TwoHanded bool
	Level     int
	Class     string
	SetID     string
	Stats     Stats
}

// Bonus is one active set bonus.
type Bonus struct {
	SetID  string
	Pieces int
	Armor  int
}

// Change is one requested or applied slot mutation.
type Change struct {
	Slot string
	Old  Item
	New  Item
}

// Character is the level and class of a registered character.
type Character struct {
	Level int
	Class string
}

// Options configures a EquipmentSlotGuard.
type Options struct {
	Base  Stats
	Slots []string
}

// EquipmentSlotGuard manages equipment slots of every character.
type EquipmentSlotGuard struct {
	opts Options
}

// NewEquipmentSlotGuard builds a guard with the supplied base attributes.
func NewEquipmentSlotGuard(opts Options) *EquipmentSlotGuard {
	return &EquipmentSlotGuard{opts: opts}
}

// Register adds a character with its level and class.
func (g *EquipmentSlotGuard) Register(charID string, c Character) error {
	return errNotImplemented
}

// Equip places an item into a slot and returns the applied change.
func (g *EquipmentSlotGuard) Equip(charID string, item Item, slot string) (Change, error) {
	return Change{}, errNotImplemented
}

// Unequip removes the item of a slot and returns the applied change.
func (g *EquipmentSlotGuard) Unequip(charID, slot string) (Change, error) {
	return Change{}, errNotImplemented
}

// Stats returns the aggregated attributes of a character.
func (g *EquipmentSlotGuard) Stats(charID string) Stats {
	return Stats{}
}

// SetBonus returns the active set bonuses of a character.
func (g *EquipmentSlotGuard) SetBonus(charID string) []Bonus {
	return nil
}

// Preview computes the attributes of a set of changes without applying them.
func (g *EquipmentSlotGuard) Preview(charID string, changes []Change) (Stats, error) {
	return Stats{}, errNotImplemented
}

// Repair recomputes the aggregated attributes from the equipped items.
func (g *EquipmentSlotGuard) Repair(charID string) error {
	return errNotImplemented
}

// Slots returns a copy of the slot to item map of a character.
func (g *EquipmentSlotGuard) Slots(charID string) map[string]Item {
	return nil
}

// SlotItem returns the item equipped in one slot.
func (g *EquipmentSlotGuard) SlotItem(charID, slot string) (Item, bool) {
	return Item{}, false
}

// Snapshot serializes options, characters and equipped items.
func (g *EquipmentSlotGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state with a previously taken snapshot.
func (g *EquipmentSlotGuard) Restore(data []byte) error {
	return errNotImplemented
}
