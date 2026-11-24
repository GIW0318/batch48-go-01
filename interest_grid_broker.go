package task

import "errors"

// Sentinels reported by the interest grid broker.
var (
	errNotImplemented   = errors.New("not implemented")
	ErrClientUnknown    = errors.New("client not registered")
	ErrClientExists     = errors.New("client already registered")
	ErrEntityUnknown    = errors.New("entity not registered")
	ErrEntityExists     = errors.New("entity already registered")
	ErrInvalidRadius    = errors.New("enter radius must be positive and smaller than leave radius")
	ErrInvalidCellLimit = errors.New("cell limit must be at least one")
	ErrBadSnapshot      = errors.New("snapshot payload rejected")
)

// Vec2 is a world space position.
type Vec2 struct {
	X float64
	Y float64
}

// EntityID identifies a replicable world entity.
type EntityID int

// Cell is one grid cell coordinate.
type Cell struct {
	X int
	Y int
}

// EntityUpdate carries the final position of an entity that moved while subscribed.
type EntityUpdate struct {
	Entity EntityID
	Pos    Vec2
}

// Delta is the subscription difference generated for one client.
type Delta struct {
	Entered []EntityID
	Left    []EntityID
	Updated []EntityUpdate
}

// BrokerStats is the observable broker counter block.
type BrokerStats struct {
	Clients      int
	Entities     int
	Cells        int
	TrimmedCells int
}

// InterestGridBroker keeps per client grid cell subscriptions and entity indexing.
type InterestGridBroker struct {
}

// NewInterestGridBroker builds a broker over a square grid of the given cell size.
func NewInterestGridBroker(cellSize float64) *InterestGridBroker {
	return &InterestGridBroker{}
}

// CellOf maps a world position to its grid cell, flooring negative coordinates.
func (b *InterestGridBroker) CellOf(pos Vec2) Cell {
	return Cell{}
}

// RegisterClient adds a viewer that starts receiving deltas from an empty delivered set.
func (b *InterestGridBroker) RegisterClient(clientID string, pos Vec2) error {
	return errNotImplemented
}

// RemoveClient drops a viewer together with its subscription and delivered set.
func (b *InterestGridBroker) RemoveClient(clientID string) error {
	return errNotImplemented
}

// RegisterEntity indexes an entity at its initial position.
func (b *InterestGridBroker) RegisterEntity(entityID EntityID, pos Vec2) error {
	return errNotImplemented
}

// MoveViewer moves a viewer observation point and rebuilds its cell subscription.
func (b *InterestGridBroker) MoveViewer(clientID string, pos Vec2) error {
	return errNotImplemented
}

// MoveEntity updates an entity position.
func (b *InterestGridBroker) MoveEntity(entityID EntityID, pos Vec2) error {
	return errNotImplemented
}

// Diff returns the entered, left and updated sets for a client and consumes them.
func (b *InterestGridBroker) Diff(clientID string) (Delta, error) {
	return Delta{}, errNotImplemented
}

// SetRadius configures the enter and leave hysteresis radii expressed in cells.
func (b *InterestGridBroker) SetRadius(enter, leave float64) error {
	return errNotImplemented
}

// SetCellLimit caps the number of subscribed cells per client.
func (b *InterestGridBroker) SetCellLimit(n int) error {
	return errNotImplemented
}

// SubscribedCells reports the currently subscribed cells of a client.
func (b *InterestGridBroker) SubscribedCells(clientID string) ([]Cell, error) {
	return nil, errNotImplemented
}

// Stats reports broker counters.
func (b *InterestGridBroker) Stats() BrokerStats {
	return BrokerStats{}
}

// Snapshot serialises the whole broker state.
func (b *InterestGridBroker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the broker state from a snapshot payload.
func (b *InterestGridBroker) Restore(payload []byte) error {
	return errNotImplemented
}
