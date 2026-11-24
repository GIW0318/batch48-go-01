package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

var (
	// ErrBagExists is returned when a player bag is registered twice.
	ErrBagExists = errors.New("bag: bag already registered")
	// ErrNoSuchBag is returned for a player without a registered bag.
	ErrNoSuchBag = errors.New("bag: unknown player bag")
	// ErrNoSpace is returned when no free rectangle fits the item.
	ErrNoSpace = errors.New("bag: no space left")
	// ErrItemTooLarge is returned when an item cannot fit the grid in any orientation.
	ErrItemTooLarge = errors.New("bag: item larger than the grid")
	// ErrDuplicateItem is returned when an item id is inserted twice.
	ErrDuplicateItem = errors.New("bag: duplicate item id")
	// ErrNoSuchItem is returned when an item id is not stored in the bag.
	ErrNoSuchItem = errors.New("bag: unknown item id")
	// ErrSlotOccupied is returned when a move target overlaps another item.
	ErrSlotOccupied = errors.New("bag: target cells occupied")
	// ErrBadPlacement is returned for invalid sizes or move coordinates.
	ErrBadPlacement = errors.New("bag: invalid placement")
	// ErrBadSnapshot is returned when snapshot bytes cannot be decoded.
	ErrBadSnapshot = errors.New("bag: bad snapshot")
)

// Item is one item stored in a bag grid.
type Item struct {
	ID        string
	W         int
	H         int
	Rotatable bool
}

// Placement is the rectangle an item occupies in the grid.
type Placement struct {
	X       int
	Y       int
	W       int
	H       int
	Rotated bool
}

// Options configures a BagSlotAllocator.
type Options struct {
	DefaultW    int
	DefaultH    int
	AllowRotate bool
}

// BagSlotAllocator places items into per player grids.
type BagSlotAllocator struct {
	opts Options
}

// NewBagSlotAllocator builds an allocator with the supplied defaults.
func NewBagSlotAllocator(opts Options) *BagSlotAllocator {
	return &BagSlotAllocator{opts: opts}
}

// Register creates the grid of one player.
func (a *BagSlotAllocator) Register(player string, w, h int) error {
	return errNotImplemented
}

// Insert finds a free rectangle for the item and stores it.
func (a *BagSlotAllocator) Insert(player string, item Item) (Placement, error) {
	return Placement{}, errNotImplemented
}

// Remove deletes an item and frees its cells.
func (a *BagSlotAllocator) Remove(player, itemID string) error {
	return errNotImplemented
}

// Find reports where a w by h rectangle would fit without storing anything.
func (a *BagSlotAllocator) Find(player string, w, h int) (Placement, bool) {
	return Placement{}, false
}

// Compact repacks the bag without holes and returns how many items moved.
func (a *BagSlotAllocator) Compact(player string) (int, error) {
	return 0, errNotImplemented
}

// Move relocates a stored item to the target coordinates.
func (a *BagSlotAllocator) Move(player, itemID string, at Placement) error {
	return errNotImplemented
}

// Usage returns used cells and total cells of one bag.
func (a *BagSlotAllocator) Usage(player string) (int, int) {
	return 0, 0
}

// At returns the item id occupying one cell.
func (a *BagSlotAllocator) At(player string, x, y int) (string, bool) {
	return "", false
}

// ItemPlacement returns the rectangle of one stored item.
func (a *BagSlotAllocator) ItemPlacement(player, itemID string) (Placement, bool) {
	return Placement{}, false
}

// Items returns the stored item ids in ascending order.
func (a *BagSlotAllocator) Items(player string) []string {
	return nil
}

// Snapshot serializes every bag including the occupancy bitmap.
func (a *BagSlotAllocator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the allocator state with a previously taken snapshot.
func (a *BagSlotAllocator) Restore(data []byte) error {
	return errNotImplemented
}
