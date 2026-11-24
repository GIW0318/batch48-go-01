// Package task places battle rooms onto game server nodes: it packs by free
// seats inside a region, books migration reservations and drains nodes.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Seats is a seat count.
type Seats int

// NodeID identifies one game server node.
type NodeID string

// Node is the caller facing view of one server node.
type Node struct {
	ID       NodeID `json:"id"`
	Region   string `json:"region"`
	Capacity int    `json:"capacity"`
	Used     int    `json:"used"`
	Reserved int    `json:"reserved"`
	Draining bool   `json:"draining"`
}

// Placement records where one room lives and where it is booked to move.
type Placement struct {
	RoomID   string `json:"room_id"`
	Node     NodeID `json:"node"`
	Seats    Seats  `json:"seats"`
	Region   string `json:"region"`
	Reserved NodeID `json:"reserved"`
}

// Errors returned by the scheduler.
var (
	ErrInvalidConfig        = errors.New("node limit is invalid")
	ErrInvalidNode          = errors.New("node id is required")
	ErrInvalidRegion        = errors.New("region is required")
	ErrInvalidCapacity      = errors.New("node capacity must be positive")
	ErrNodeExists           = errors.New("node already exists")
	ErrNodeLimit            = errors.New("node limit reached")
	ErrNodeNotFound         = errors.New("node not found")
	ErrInvalidRoom          = errors.New("room id is required")
	ErrInvalidSeats         = errors.New("seat count must be positive")
	ErrRoomExists           = errors.New("room is already placed")
	ErrRoomNotFound         = errors.New("room placement not found")
	ErrInsufficientCapacity = errors.New("node has not enough free seats")
	ErrNoNodeInRegion       = errors.New("no node serves this region")
	ErrPlacementNotFound    = errors.New("room is not placed on this node")
	ErrAlreadyReserved      = errors.New("room already has a migration reservation")
	ErrSameNode             = errors.New("source and target node are the same")
	ErrNodeDraining         = errors.New("node is draining")
	ErrAlreadyDraining      = errors.New("node is already draining")
	ErrReservedElsewhere    = errors.New("room is reserved on another node")
	ErrBadSnapshot          = errors.New("snapshot payload is invalid")
)

const (
	snapshotVersion = 1
	// The node cap is explicit. Reservations are bounded too so a long running
	// process cannot grow that table without limit; the cap scales with the
	// configured node table.
	reservationsPerNode = 1024
)

type reservationEntry struct {
	RoomID string `json:"room_id"`
	Node   NodeID `json:"node"`
}

type schedulerSnapshot struct {
	Version      int                `json:"version"`
	Nodes        []Node             `json:"nodes"`
	Placements   []Placement        `json:"placements"`
	Reservations []reservationEntry `json:"reservations"`
}

// RoomPlacementScheduler owns the node table and the room placements.
type RoomPlacementScheduler struct {
	mu           sync.Mutex
	maxNodes     int
	maxReserves  int
	nodes        map[NodeID]*Node
	order        []NodeID
	placements   map[string]*Placement
	reservations map[string]NodeID
}

// NewRoomPlacementScheduler builds a scheduler with a hard node cap.
func NewRoomPlacementScheduler(maxNodes int) (*RoomPlacementScheduler, error) {
	if maxNodes <= 0 {
		return nil, ErrInvalidConfig
	}
	return &RoomPlacementScheduler{
		maxNodes:     maxNodes,
		maxReserves:  reserveLimit(maxNodes),
		nodes:        make(map[NodeID]*Node),
		placements:   make(map[string]*Placement),
		reservations: make(map[string]NodeID),
	}, nil
}

// AddNode registers one game server node.
func (s *RoomPlacementScheduler) AddNode(id NodeID, region string, capacity int) error {
	if id == "" {
		return ErrInvalidNode
	}
	if region == "" {
		return ErrInvalidRegion
	}
	if capacity <= 0 {
		return ErrInvalidCapacity
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.nodes[id]; ok {
		return ErrNodeExists
	}
	if len(s.nodes) >= s.maxNodes {
		return ErrNodeLimit
	}
	s.nodes[id] = &Node{
		ID:       id,
		Region:   region,
		Capacity: capacity,
	}
	s.order = append(s.order, id)
	return nil
}

// Place packs a room onto the tightest fitting node of the requested region.
func (s *RoomPlacementScheduler) Place(roomID string, need Seats, region string) (NodeID, error) {
	if roomID == "" {
		return "", ErrInvalidRoom
	}
	if need <= 0 {
		return "", ErrInvalidSeats
	}
	if region == "" {
		return "", ErrInvalidRegion
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.placements[roomID]; ok {
		return "", ErrRoomExists
	}

	target := s.tightestFit(region, int(need))
	if target == nil {
		if !s.regionHasNode(region) {
			return "", ErrNoNodeInRegion
		}
		// Region affinity is never traded for capacity elsewhere.
		return "", ErrInsufficientCapacity
	}

	target.Used += int(need)
	s.placements[roomID] = &Placement{
		RoomID: roomID,
		Node:   target.ID,
		Seats:  need,
		Region: target.Region,
	}
	return target.ID, nil
}

// Reserve books the room seat count on a migration target node.
func (s *RoomPlacementScheduler) Reserve(nodeID NodeID, roomID string) error {
	if nodeID == "" {
		return ErrInvalidNode
	}
	if roomID == "" {
		return ErrInvalidRoom
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := s.nodes[nodeID]
	if !ok {
		return ErrNodeNotFound
	}
	placement, ok := s.placements[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if placement.Node == nodeID {
		return ErrSameNode
	}
	if placement.Reserved != "" {
		return ErrAlreadyReserved
	}
	if target.Draining {
		return ErrNodeDraining
	}
	if len(s.reservations) >= s.maxReserves {
		return fmt.Errorf("%w: reservation table is full", ErrNodeLimit)
	}
	if s.freeSeats(target) < int(placement.Seats) {
		return ErrInsufficientCapacity
	}

	target.Reserved += int(placement.Seats)
	placement.Reserved = nodeID
	s.reservations[roomID] = nodeID
	return nil
}

// Release removes a placement and returns its seats to the node.
func (s *RoomPlacementScheduler) Release(nodeID NodeID, roomID string) error {
	if nodeID == "" {
		return ErrInvalidNode
	}
	if roomID == "" {
		return ErrInvalidRoom
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return ErrNodeNotFound
	}
	placement, ok := s.placements[roomID]
	if !ok || placement.Node != nodeID {
		return ErrPlacementNotFound
	}

	if placement.Reserved != "" {
		if reservedNode, ok := s.nodes[placement.Reserved]; ok {
			reservedNode.Reserved -= int(placement.Seats)
			if reservedNode.Reserved < 0 {
				reservedNode.Reserved = 0
			}
		}
		delete(s.reservations, roomID)
	}
	node.Used -= int(placement.Seats)
	if node.Used < 0 {
		node.Used = 0
	}
	delete(s.placements, roomID)
	return nil
}

// Drain stops a node from taking new rooms.
func (s *RoomPlacementScheduler) Drain(nodeID NodeID) error {
	if nodeID == "" {
		return ErrInvalidNode
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return ErrNodeNotFound
	}
	if node.Draining {
		return ErrAlreadyDraining
	}
	node.Draining = true
	return nil
}

// Migrate moves one room to another node, consuming a reservation when the
// target matches it.
func (s *RoomPlacementScheduler) Migrate(roomID string, toNode NodeID) error {
	if roomID == "" {
		return ErrInvalidRoom
	}
	if toNode == "" {
		return ErrInvalidNode
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := s.nodes[toNode]
	if !ok {
		return ErrNodeNotFound
	}
	placement, ok := s.placements[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if placement.Node == toNode {
		return ErrSameNode
	}

	reserved := placement.Reserved
	if reserved != "" && reserved != toNode {
		// A booked migration target is an affinity: never fall back to another
		// node, even if that other node has room.
		return ErrReservedElsewhere
	}
	if reserved == "" && target.Draining {
		return ErrNodeDraining
	}

	source, ok := s.nodes[placement.Node]
	if !ok {
		return ErrNodeNotFound
	}

	if reserved == toNode {
		if target.Reserved < int(placement.Seats) {
			return ErrInsufficientCapacity
		}
	} else if s.freeSeats(target) < int(placement.Seats) {
		return ErrInsufficientCapacity
	}

	// All checks pass, so the ledger is changed in one critical section. A
	// failed migration therefore leaves both nodes and the placement untouched.
	if reserved == toNode {
		target.Reserved -= int(placement.Seats)
		delete(s.reservations, roomID)
		placement.Reserved = ""
	}
	source.Used -= int(placement.Seats)
	target.Used += int(placement.Seats)
	placement.Node = toNode
	placement.Region = target.Region
	return nil
}

// Node reads one node.
func (s *RoomPlacementScheduler) Node(id NodeID) (Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *node, true
}

// Placement reads one room placement.
func (s *RoomPlacementScheduler) Placement(roomID string) (Placement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	placement, ok := s.placements[roomID]
	if !ok {
		return Placement{}, false
	}
	return *placement, true
}

// FreeSeats reports the seats a node can still hand out.
func (s *RoomPlacementScheduler) FreeSeats(nodeID NodeID) (int, error) {
	if nodeID == "" {
		return 0, ErrInvalidNode
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return 0, ErrNodeNotFound
	}
	return s.freeSeats(node), nil
}

// Snapshot serialises the node table and every placement.
func (s *RoomPlacementScheduler) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodeIDs := make([]NodeID, 0, len(s.nodes))
	for id := range s.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	nodeViews := make([]Node, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeViews = append(nodeViews, *s.nodes[id])
	}

	roomIDs := make([]string, 0, len(s.placements))
	for id := range s.placements {
		roomIDs = append(roomIDs, id)
	}
	sort.Strings(roomIDs)

	placementViews := make([]Placement, 0, len(roomIDs))
	for _, id := range roomIDs {
		placementViews = append(placementViews, *s.placements[id])
	}

	reservedRooms := make([]string, 0, len(s.reservations))
	for roomID := range s.reservations {
		reservedRooms = append(reservedRooms, roomID)
	}
	sort.Strings(reservedRooms)

	reservationViews := make([]reservationEntry, 0, len(reservedRooms))
	for _, roomID := range reservedRooms {
		reservationViews = append(reservationViews, reservationEntry{
			RoomID: roomID,
			Node:   s.reservations[roomID],
		})
	}

	data, err := json.Marshal(schedulerSnapshot{
		Version:      snapshotVersion,
		Nodes:        nodeViews,
		Placements:   placementViews,
		Reservations: reservationViews,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	return data, nil
}

// Restore rebuilds the scheduler from a snapshot, all or nothing.
func (s *RoomPlacementScheduler) Restore(data []byte) error {
	var snapshot schedulerSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if len(snapshot.Nodes) > s.maxNodes {
		return ErrBadSnapshot
	}

	nodes := make(map[NodeID]*Node, len(snapshot.Nodes))
	order := make([]NodeID, 0, len(snapshot.Nodes))
	for _, view := range snapshot.Nodes {
		node := view
		if !validNodeSnapshot(node) {
			return ErrBadSnapshot
		}
		if _, exists := nodes[node.ID]; exists {
			return ErrBadSnapshot
		}
		nodes[node.ID] = &node
		order = append(order, node.ID)
	}

	placements := make(map[string]*Placement, len(snapshot.Placements))
	usedByNode := make(map[NodeID]int, len(nodes))
	for _, view := range snapshot.Placements {
		placement := view
		if placement.RoomID == "" || placement.Seats <= 0 || placement.Node == "" {
			return ErrBadSnapshot
		}
		if _, exists := placements[placement.RoomID]; exists {
			return ErrBadSnapshot
		}
		node, exists := nodes[placement.Node]
		if !exists || placement.Region != node.Region {
			return ErrBadSnapshot
		}
		if placement.Reserved == placement.Node {
			return ErrBadSnapshot
		}
		placements[placement.RoomID] = &placement
		usedByNode[placement.Node] += int(placement.Seats)
	}

	reservations := make(map[string]NodeID, len(snapshot.Reservations))
	reservedByNode := make(map[NodeID]int, len(nodes))
	for _, entry := range snapshot.Reservations {
		if entry.RoomID == "" || entry.Node == "" {
			return ErrBadSnapshot
		}
		if _, exists := reservations[entry.RoomID]; exists {
			return ErrBadSnapshot
		}
		placement, exists := placements[entry.RoomID]
		if !exists || placement.Reserved != entry.Node {
			return ErrBadSnapshot
		}
		if _, exists := nodes[entry.Node]; !exists {
			return ErrBadSnapshot
		}
		reservations[entry.RoomID] = entry.Node
		reservedByNode[entry.Node] += int(placement.Seats)
	}
	if len(reservations) > s.maxReserves {
		return ErrBadSnapshot
	}

	for roomID, placement := range placements {
		reserved, hasReservation := reservations[roomID]
		if placement.Reserved == "" && hasReservation {
			return ErrBadSnapshot
		}
		if placement.Reserved != "" && (!hasReservation || reserved != placement.Reserved) {
			return ErrBadSnapshot
		}
	}

	for id, node := range nodes {
		if node.Used != usedByNode[id] || node.Reserved != reservedByNode[id] {
			return ErrBadSnapshot
		}
		if node.Used < 0 || node.Reserved < 0 || node.Used+node.Reserved > node.Capacity {
			return ErrBadSnapshot
		}
	}

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	s.mu.Lock()
	s.nodes = nodes
	s.order = order
	s.placements = placements
	s.reservations = reservations
	s.mu.Unlock()
	return nil
}

func (s *RoomPlacementScheduler) freeSeats(node *Node) int {
	free := node.Capacity - node.Used - node.Reserved
	if free < 0 {
		return 0
	}
	return free
}

func (s *RoomPlacementScheduler) regionHasNode(region string) bool {
	for _, node := range s.nodes {
		if node.Region == region {
			return true
		}
	}
	return false
}

// tightestFit returns the eligible node with the least free capacity that can
// still hold need seats. Equal free capacity is broken by node ID.
func (s *RoomPlacementScheduler) tightestFit(region string, need int) *Node {
	var best *Node
	bestFree := 0
	for _, node := range s.nodes {
		if node.Region != region || node.Draining {
			continue
		}
		free := s.freeSeats(node)
		if free < need {
			continue
		}
		if best == nil || free < bestFree || (free == bestFree && node.ID < best.ID) {
			best = node
			bestFree = free
		}
	}
	return best
}

func reserveLimit(maxNodes int) int {
	maxInt := int(^uint(0) >> 1)
	if maxNodes > maxInt/reservationsPerNode {
		return maxInt
	}
	return maxNodes * reservationsPerNode
}

func validNodeSnapshot(node Node) bool {
	return node.ID != "" &&
		node.Region != "" &&
		node.Capacity > 0 &&
		node.Used >= 0 &&
		node.Reserved >= 0 &&
		node.Used+node.Reserved <= node.Capacity
}
