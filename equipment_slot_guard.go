package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

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

type characterState struct {
	info  Character
	slots map[string]Item
}

// EquipmentSlotGuard manages equipment slots of every character.
type EquipmentSlotGuard struct {
	mu      sync.RWMutex
	opts    Options
	slotSet map[string]struct{}
	chars   map[string]*characterState
}

const (
	snapshotKind    = "equipment-slot-guard"
	snapshotVersion = 1
)

type snapshotCharacter struct {
	Character Character       `json:"character"`
	Slots     map[string]Item `json:"slots"`
}

type snapshotState struct {
	Options    Options                      `json:"options"`
	Characters map[string]snapshotCharacter `json:"characters"`
}

type snapshotEnvelope struct {
	Kind     string          `json:"kind"`
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// NewEquipmentSlotGuard builds a guard with the supplied base attributes.
func NewEquipmentSlotGuard(opts Options) *EquipmentSlotGuard {
	opts = cloneOptions(opts)
	return &EquipmentSlotGuard{
		opts:    opts,
		slotSet: makeSlotSet(opts.Slots),
		chars:   make(map[string]*characterState),
	}
}

// Register adds a character with its level and class.
func (g *EquipmentSlotGuard) Register(charID string, c Character) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.chars[charID]; ok {
		return ErrDuplicateCharacter
	}
	g.chars[charID] = &characterState{
		info:  c,
		slots: make(map[string]Item),
	}
	return nil
}

// Equip places an item into a slot and returns the applied change.
func (g *EquipmentSlotGuard) Equip(charID string, item Item, slot string) (Change, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	char, ok := g.chars[charID]
	if !ok {
		return Change{}, ErrUnknownCharacter
	}

	next := cloneSlots(char.slots)
	change, err := applyEquip(g.slotSet, next, char.info, item, slot)
	if err != nil {
		return Change{}, err
	}
	char.slots = next
	return change, nil
}

// Unequip removes the item of a slot and returns the applied change.
func (g *EquipmentSlotGuard) Unequip(charID, slot string) (Change, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	char, ok := g.chars[charID]
	if !ok {
		return Change{}, ErrUnknownCharacter
	}

	next := cloneSlots(char.slots)
	change, err := applyUnequip(g.slotSet, next, slot)
	if err != nil {
		return Change{}, err
	}
	char.slots = next
	return change, nil
}

// Stats returns the aggregated attributes of a character.
func (g *EquipmentSlotGuard) Stats(charID string) Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	char, ok := g.chars[charID]
	if !ok {
		return Stats{}
	}
	return statsFor(g.opts.Base, char.slots)
}

// SetBonus returns the active set bonuses of a character.
func (g *EquipmentSlotGuard) SetBonus(charID string) []Bonus {
	g.mu.RLock()
	defer g.mu.RUnlock()

	char, ok := g.chars[charID]
	if !ok {
		return nil
	}
	return bonusesFor(char.slots)
}

// Preview computes the attributes of a set of changes without applying them.
func (g *EquipmentSlotGuard) Preview(charID string, changes []Change) (Stats, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	char, ok := g.chars[charID]
	if !ok {
		return Stats{}, ErrUnknownCharacter
	}

	next := cloneSlots(char.slots)
	for _, change := range changes {
		var err error
		if change.New.ID != "" {
			_, err = applyEquip(g.slotSet, next, char.info, change.New, change.Slot)
		} else {
			_, err = applyUnequip(g.slotSet, next, change.Slot)
		}
		if err != nil {
			return Stats{}, err
		}
	}
	return statsFor(g.opts.Base, next), nil
}

// Repair recomputes the aggregated attributes from the equipped items.
func (g *EquipmentSlotGuard) Repair(charID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	char, ok := g.chars[charID]
	if !ok {
		return ErrUnknownCharacter
	}

	// Stats and bonuses are derived from the committed slot map, so observing
	// the map recomputes both without changing any slot content.
	_ = statsFor(g.opts.Base, char.slots)
	_ = bonusesFor(char.slots)
	return nil
}

// Slots returns a copy of the slot to item map of a character.
func (g *EquipmentSlotGuard) Slots(charID string) map[string]Item {
	g.mu.RLock()
	defer g.mu.RUnlock()

	char, ok := g.chars[charID]
	if !ok {
		return nil
	}
	return cloneSlots(char.slots)
}

// SlotItem returns the item equipped in one slot.
func (g *EquipmentSlotGuard) SlotItem(charID, slot string) (Item, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	char, ok := g.chars[charID]
	if !ok {
		return Item{}, false
	}
	item, ok := char.slots[slot]
	return item, ok
}

// Snapshot serializes options, characters and equipped items.
func (g *EquipmentSlotGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := snapshotState{
		Options:    cloneOptions(g.opts),
		Characters: make(map[string]snapshotCharacter, len(g.chars)),
	}
	for id, char := range g.chars {
		state.Characters[id] = snapshotCharacter{
			Character: char.info,
			Slots:     cloneSlots(char.slots),
		}
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	envelope := snapshotEnvelope{
		Kind:     snapshotKind,
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(sum[:]),
		Payload:  payload,
	}
	return json.Marshal(envelope)
}

// Restore replaces the guard state with a previously taken snapshot.
func (g *EquipmentSlotGuard) Restore(data []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Kind != snapshotKind || envelope.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	sum := sha256.Sum256(envelope.Payload)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return ErrBadSnapshot
	}

	var state snapshotState
	if err := json.Unmarshal(envelope.Payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if err := validateSnapshot(state); err != nil {
		return ErrBadSnapshot
	}

	opts := cloneOptions(state.Options)
	chars := make(map[string]*characterState, len(state.Characters))
	for id, char := range state.Characters {
		chars[id] = &characterState{
			info:  char.Character,
			slots: cloneSlots(char.Slots),
		}
	}

	g.mu.Lock()
	g.opts = opts
	g.slotSet = makeSlotSet(opts.Slots)
	g.chars = chars
	g.mu.Unlock()
	return nil
}

func applyEquip(slotSet map[string]struct{}, slots map[string]Item, char Character, item Item, slot string) (Change, error) {
	if _, ok := slotSet[slot]; !ok {
		return Change{}, ErrUnknownSlot
	}
	if item.TwoHanded && slot != "main" {
		return Change{}, ErrUnknownSlot
	}
	if item.Level > char.Level {
		return Change{}, ErrLevelTooLow
	}
	if item.Class != "" && item.Class != char.Class {
		return Change{}, ErrWrongClass
	}
	if slot == "off" {
		if main, ok := slots["main"]; ok && main.TwoHanded {
			return Change{}, ErrOffhandBlocked
		}
	}

	for otherSlot, equipped := range slots {
		if otherSlot == slot || item.ID == "" || equipped.ID != item.ID {
			continue
		}
		if item.TwoHanded && slot == "main" && otherSlot == "off" && equipped == item {
			continue
		}
		return Change{}, ErrDuplicateItem
	}

	old := slots[slot]
	change := Change{Slot: slot, Old: old, New: item}

	if item.TwoHanded {
		slots["main"] = item
		slots["off"] = item
		return change, nil
	}
	if slot == "main" && old.TwoHanded {
		delete(slots, "off")
	}
	slots[slot] = item
	return change, nil
}

func applyUnequip(slotSet map[string]struct{}, slots map[string]Item, slot string) (Change, error) {
	if _, ok := slotSet[slot]; !ok {
		return Change{}, ErrUnknownSlot
	}

	old, ok := slots[slot]
	if !ok {
		return Change{}, ErrSlotEmpty
	}
	delete(slots, slot)

	if slot == "main" && old.TwoHanded {
		delete(slots, "off")
	}
	if slot == "off" && old.TwoHanded {
		if main, ok := slots["main"]; ok && main.TwoHanded && main == old {
			delete(slots, "main")
		}
	}

	return Change{Slot: slot, Old: old}, nil
}

func statsFor(base Stats, slots map[string]Item) Stats {
	result := base
	seen := make(map[string]struct{})
	for _, slot := range sortedSlotNames(slots) {
		item := slots[slot]
		if isMirroredOffhand(slots, slot, item) {
			continue
		}
		if item.ID != "" {
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
		}
		result.Strength += item.Stats.Strength
		result.Agility += item.Stats.Agility
		result.Stamina += item.Stats.Stamina
		result.Armor += item.Stats.Armor
	}
	return result
}

func bonusesFor(slots map[string]Item) []Bonus {
	counts := make(map[string]int)
	seen := make(map[string]struct{})

	for _, slot := range sortedSlotNames(slots) {
		item := slots[slot]
		if isMirroredOffhand(slots, slot, item) {
			continue
		}
		if item.SetID == "" {
			continue
		}
		key := "slot:" + slot
		if item.ID != "" {
			key = "item:" + item.ID
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		counts[item.SetID]++
	}

	bonuses := make([]Bonus, 0, len(counts))
	for setID, pieces := range counts {
		if pieces < 2 {
			continue
		}
		bonuses = append(bonuses, Bonus{
			SetID:  setID,
			Pieces: pieces,
			Armor:  10 * pieces,
		})
	}
	sort.Slice(bonuses, func(i, j int) bool {
		return bonuses[i].SetID < bonuses[j].SetID
	})
	return bonuses
}

func isMirroredOffhand(slots map[string]Item, slot string, item Item) bool {
	if slot != "off" {
		return false
	}
	main, ok := slots["main"]
	return ok && main.TwoHanded && main == item
}

func validateSnapshot(state snapshotState) error {
	slotSet := makeSlotSet(state.Options.Slots)

	for _, char := range state.Characters {
		main, hasMain := char.Slots["main"]
		off, hasOff := char.Slots["off"]

		for slot, item := range char.Slots {
			if _, ok := slotSet[slot]; ok {
				continue
			}
			if slot == "off" && item.TwoHanded && hasMain && main == item {
				continue
			}
			return ErrBadSnapshot
		}

		if hasMain && main.TwoHanded {
			if !hasOff || off != main {
				return ErrBadSnapshot
			}
		}
		if hasOff && off.TwoHanded {
			if !hasMain || main != off {
				return ErrBadSnapshot
			}
		}

		seen := make(map[string]string)
		for slot, item := range char.Slots {
			if item.ID == "" {
				continue
			}
			if previous, ok := seen[item.ID]; ok {
				if isPairedTwoHanded(char.Slots, previous, slot, item) {
					continue
				}
				return ErrBadSnapshot
			}
			seen[item.ID] = slot
		}
	}
	return nil
}

func isPairedTwoHanded(slots map[string]Item, a, b string, item Item) bool {
	if !item.TwoHanded {
		return false
	}
	if a == "main" && b == "off" {
		return slots["main"] == item && slots["off"] == item
	}
	if a == "off" && b == "main" {
		return slots["main"] == item && slots["off"] == item
	}
	return false
}

func cloneSlots(slots map[string]Item) map[string]Item {
	result := make(map[string]Item, len(slots))
	for slot, item := range slots {
		result[slot] = item
	}
	return result
}

func cloneOptions(opts Options) Options {
	opts.Slots = append([]string(nil), opts.Slots...)
	return opts
}

func makeSlotSet(slots []string) map[string]struct{} {
	result := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		result[slot] = struct{}{}
	}
	return result
}

func sortedSlotNames(slots map[string]Item) []string {
	names := make([]string, 0, len(slots))
	for name := range slots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
