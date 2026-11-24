package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
)

// Sentinels reported by the interest grid broker.
var (
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
	mu sync.Mutex

	cellSize  float64
	enterR    float64
	leaveR    float64
	cellLimit int

	entities map[EntityID]entityRec
	clients  map[string]*clientState

	trimmedCells int
}

type clientState struct {
	pos        Vec2
	subscribed map[Cell]struct{}
	delivered  map[EntityID]deliveryRec
}

type entityRec struct {
	pos     Vec2
	version int64
}

type deliveryRec struct {
	pos     Vec2
	version int64
}

// NewInterestGridBroker builds a broker over a square grid of the given cell size.
func NewInterestGridBroker(cellSize float64) *InterestGridBroker {
	return &InterestGridBroker{
		cellSize:  cellSize,
		enterR:    1,
		leaveR:    2,
		cellLimit: 64,
		entities:  make(map[EntityID]entityRec),
		clients:   make(map[string]*clientState),
	}
}

// CellOf maps a world position to its grid cell, flooring negative coordinates.
func (b *InterestGridBroker) CellOf(pos Vec2) Cell {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cellOf(pos)
}

func (b *InterestGridBroker) cellOf(pos Vec2) Cell {
	return Cell{
		X: int(math.Floor(pos.X / b.cellSize)),
		Y: int(math.Floor(pos.Y / b.cellSize)),
	}
}

func chebyshev(a, b Cell) int {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

// candidateCells enumerates the cells within the enter radius around pos.
func (b *InterestGridBroker) candidateCells(pos Vec2) []Cell {
	center := b.cellOf(pos)
	radius := int(math.Floor(b.enterR))
	cells := make([]Cell, 0, (2*radius+1)*(2*radius+1))
	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			cell := Cell{X: center.X + dx, Y: center.Y + dy}
			if float64(chebyshev(center, cell)) <= b.enterR {
				cells = append(cells, cell)
			}
		}
	}
	sort.Slice(cells, func(i, j int) bool {
		di := chebyshev(center, cells[i])
		dj := chebyshev(center, cells[j])
		if di != dj {
			return di < dj
		}
		if cells[i].X != cells[j].X {
			return cells[i].X < cells[j].X
		}
		return cells[i].Y < cells[j].Y
	})
	return cells
}

// buildSubscription applies the per-client cell limit, trimming farthest cells
// first (Chebyshev distance, then X/Y ascending tie break) and accumulating
// the trimmed count into the broker stats.
func (b *InterestGridBroker) buildSubscription(pos Vec2) map[Cell]struct{} {
	cells := b.candidateCells(pos)
	if len(cells) > b.cellLimit {
		b.trimmedCells += len(cells) - b.cellLimit
		cells = cells[:b.cellLimit]
	}
	sub := make(map[Cell]struct{}, len(cells))
	for _, cell := range cells {
		sub[cell] = struct{}{}
	}
	return sub
}

// rebuildSubscriptions re-applies the current limit/radius to every client.
func (b *InterestGridBroker) rebuildSubscriptions() {
	for _, client := range b.clients {
		client.subscribed = b.buildSubscription(client.pos)
	}
}

// reconcile derives the pending delta against the latest world state. Hysteresis
// is applied against the delivered set so entities oscillating inside the band
// never re-enter or leave; repeated moves coalesce to the final position.
func (b *InterestGridBroker) reconcile(client *clientState) Delta {
	center := b.cellOf(client.pos)
	entered := make([]EntityID, 0)
	left := make([]EntityID, 0)
	updated := make([]EntityUpdate, 0)

	for id, rec := range b.entities {
		prev, wasDelivered := client.delivered[id]
		dist := float64(chebyshev(center, b.cellOf(rec.pos)))

		if !wasDelivered {
			if _, visible := client.subscribed[b.cellOf(rec.pos)]; visible && dist <= b.enterR {
				client.delivered[id] = deliveryRec{pos: rec.pos, version: rec.version}
				entered = append(entered, id)
			}
			continue
		}

		if dist > b.leaveR {
			delete(client.delivered, id)
			left = append(left, id)
			continue
		}

		// Version comparison guarantees one update per moved entity even when
		// concurrent moves land back on a previously delivered position.
		if rec.version > prev.version {
			client.delivered[id] = deliveryRec{pos: rec.pos, version: rec.version}
			updated = append(updated, EntityUpdate{Entity: id, Pos: rec.pos})
		}
	}

	sort.Slice(entered, func(i, j int) bool { return entered[i] < entered[j] })
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(updated, func(i, j int) bool { return updated[i].Entity < updated[j].Entity })

	return Delta{Entered: entered, Left: left, Updated: updated}
}

// RegisterClient adds a viewer that starts receiving deltas from an empty delivered set.
func (b *InterestGridBroker) RegisterClient(clientID string, pos Vec2) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[clientID]; ok {
		return ErrClientExists
	}
	b.clients[clientID] = &clientState{
		pos:        pos,
		subscribed: b.buildSubscription(pos),
		delivered:  make(map[EntityID]deliveryRec),
	}
	return nil
}

// RemoveClient drops a viewer together with its subscription and delivered set.
func (b *InterestGridBroker) RemoveClient(clientID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[clientID]; !ok {
		return ErrClientUnknown
	}
	delete(b.clients, clientID)
	return nil
}

// RegisterEntity indexes an entity at its initial position.
func (b *InterestGridBroker) RegisterEntity(entityID EntityID, pos Vec2) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entities[entityID]; ok {
		return ErrEntityExists
	}
	b.entities[entityID] = entityRec{pos: pos}
	return nil
}

// MoveViewer moves a viewer observation point and rebuilds its cell subscription.
func (b *InterestGridBroker) MoveViewer(clientID string, pos Vec2) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	client, ok := b.clients[clientID]
	if !ok {
		return ErrClientUnknown
	}
	// Every move is treated as a full recompute against the new observation
	// point: long teleports never emit per-cell path enter/leave noise, and the
	// hysteresis delivered set keeps band entities from flickering.
	client.pos = pos
	client.subscribed = b.buildSubscription(pos)
	return nil
}

// MoveEntity updates an entity position.
func (b *InterestGridBroker) MoveEntity(entityID EntityID, pos Vec2) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entities[entityID]; !ok {
		return ErrEntityUnknown
	}
	// Only the final position is retained and the version advances. Diff
	// reconciles once under the same lock, so concurrent moves coalesce into a
	// single update per entity regardless of the ending position.
	rec := b.entities[entityID]
	rec.version++
	rec.pos = pos
	b.entities[entityID] = rec
	return nil
}

// Diff returns the entered, left and updated sets for a client and consumes them.
func (b *InterestGridBroker) Diff(clientID string) (Delta, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client, ok := b.clients[clientID]
	if !ok {
		return Delta{}, ErrClientUnknown
	}
	return b.reconcile(client), nil
}

// SetRadius configures the enter and leave hysteresis radii expressed in cells.
func (b *InterestGridBroker) SetRadius(enter, leave float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if enter <= 0 || leave <= enter {
		return ErrInvalidRadius
	}
	b.enterR = enter
	b.leaveR = leave
	b.rebuildSubscriptions()
	return nil
}

// SetCellLimit caps the number of subscribed cells per client.
func (b *InterestGridBroker) SetCellLimit(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 1 {
		return ErrInvalidCellLimit
	}
	b.cellLimit = n
	b.rebuildSubscriptions()
	return nil
}

// SubscribedCells reports the currently subscribed cells of a client.
func (b *InterestGridBroker) SubscribedCells(clientID string) ([]Cell, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client, ok := b.clients[clientID]
	if !ok {
		return nil, ErrClientUnknown
	}
	cells := make([]Cell, 0, len(client.subscribed))
	for cell := range client.subscribed {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].X != cells[j].X {
			return cells[i].X < cells[j].X
		}
		return cells[i].Y < cells[j].Y
	})
	return cells, nil
}

// Stats reports broker counters.
func (b *InterestGridBroker) Stats() BrokerStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	cellSet := make(map[Cell]struct{})
	for _, rec := range b.entities {
		cellSet[b.cellOf(rec.pos)] = struct{}{}
	}
	return BrokerStats{
		Clients:      len(b.clients),
		Entities:     len(b.entities),
		Cells:        len(cellSet),
		TrimmedCells: b.trimmedCells,
	}
}

type snapshotClient struct {
	Pos        Vec2                          `json:"pos"`
	Subscribed []Cell                        `json:"subscribed"`
	Delivered  map[EntityID]snapshotDelivery `json:"delivered"`
}

type snapshotEntity struct {
	Pos     Vec2  `json:"pos"`
	Version int64 `json:"version"`
}

type snapshotDelivery struct {
	Pos     Vec2  `json:"pos"`
	Version int64 `json:"version"`
}

type snapshotPayload struct {
	CellSize     float64                     `json:"cell_size"`
	EnterRadius  float64                     `json:"enter_radius"`
	LeaveRadius  float64                     `json:"leave_radius"`
	CellLimit    int                         `json:"cell_limit"`
	TrimmedCells int                         `json:"trimmed_cells"`
	Entities     map[EntityID]snapshotEntity `json:"entities"`
	Clients      map[string]snapshotClient   `json:"clients"`
}

// Snapshot serialises the whole broker state.
func (b *InterestGridBroker) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	payload := snapshotPayload{
		CellSize:     b.cellSize,
		EnterRadius:  b.enterR,
		LeaveRadius:  b.leaveR,
		CellLimit:    b.cellLimit,
		TrimmedCells: b.trimmedCells,
		Entities:     make(map[EntityID]snapshotEntity, len(b.entities)),
		Clients:      make(map[string]snapshotClient, len(b.clients)),
	}
	for id, rec := range b.entities {
		payload.Entities[id] = snapshotEntity{Pos: rec.pos, Version: rec.version}
	}
	for id, client := range b.clients {
		sub := make([]Cell, 0, len(client.subscribed))
		for cell := range client.subscribed {
			sub = append(sub, cell)
		}
		delivered := make(map[EntityID]snapshotDelivery, len(client.delivered))
		for entity, rec := range client.delivered {
			delivered[entity] = snapshotDelivery{Pos: rec.pos, Version: rec.version}
		}
		payload.Clients[id] = snapshotClient{
			Pos:        client.pos,
			Subscribed: sub,
			Delivered:  delivered,
		}
	}
	return json.Marshal(payload)
}

// Restore rebuilds the broker state from a snapshot payload.
func (b *InterestGridBroker) Restore(payload []byte) error {
	if len(bytes.TrimSpace(payload)) == 0 {
		return ErrBadSnapshot
	}

	var snap snapshotPayload
	if err := json.Unmarshal(payload, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.CellSize <= 0 || snap.EnterRadius <= 0 || snap.LeaveRadius <= snap.EnterRadius || snap.CellLimit < 1 {
		return ErrBadSnapshot
	}
	if snap.Entities == nil || snap.Clients == nil {
		return ErrBadSnapshot
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.cellSize = snap.CellSize
	b.enterR = snap.EnterRadius
	b.leaveR = snap.LeaveRadius
	b.cellLimit = snap.CellLimit
	b.trimmedCells = snap.TrimmedCells

	b.entities = make(map[EntityID]entityRec, len(snap.Entities))
	for id, se := range snap.Entities {
		b.entities[id] = entityRec{pos: se.Pos, version: se.Version}
	}

	b.clients = make(map[string]*clientState, len(snap.Clients))
	for id, sc := range snap.Clients {
		if len(sc.Subscribed) > b.cellLimit {
			return ErrBadSnapshot
		}
		sub := make(map[Cell]struct{}, len(sc.Subscribed))
		for _, cell := range sc.Subscribed {
			sub[cell] = struct{}{}
		}
		delivered := make(map[EntityID]deliveryRec, len(sc.Delivered))
		for entity, rec := range sc.Delivered {
			delivered[entity] = deliveryRec{pos: rec.Pos, version: rec.Version}
		}
		b.clients[id] = &clientState{
			pos:        sc.Pos,
			subscribed: sub,
			delivered:  delivered,
		}
	}
	return nil
}
