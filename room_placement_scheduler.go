// Package task places battle rooms onto game server nodes: it packs by free
// seats inside a region, books migration reservations and drains nodes.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// RoomPlacementScheduler owns the node table and the room placements.
type RoomPlacementScheduler struct {
	mu         sync.Mutex
	maxNodes   int
	nodes      map[NodeID]*Node
	order      []NodeID
	placements map[string]*Placement
}

// NewRoomPlacementScheduler builds a scheduler with a hard node cap.
func NewRoomPlacementScheduler(maxNodes int) (*RoomPlacementScheduler, error) {
	return nil, errNotImplemented
}

// AddNode registers one game server node.
func (s *RoomPlacementScheduler) AddNode(id NodeID, region string, capacity int) error {
	return errNotImplemented
}

// Place packs a room onto the tightest fitting node of the requested region.
func (s *RoomPlacementScheduler) Place(roomID string, need Seats, region string) (NodeID, error) {
	return "", errNotImplemented
}

// Reserve books the room seat count on a migration target node.
func (s *RoomPlacementScheduler) Reserve(nodeID NodeID, roomID string) error {
	return errNotImplemented
}

// Release removes a placement and returns its seats to the node.
func (s *RoomPlacementScheduler) Release(nodeID NodeID, roomID string) error {
	return errNotImplemented
}

// Drain stops a node from taking new rooms.
func (s *RoomPlacementScheduler) Drain(nodeID NodeID) error {
	return errNotImplemented
}

// Migrate moves one room to another node, consuming a reservation when the
// target matches it.
func (s *RoomPlacementScheduler) Migrate(roomID string, toNode NodeID) error {
	return errNotImplemented
}

// Node reads one node.
func (s *RoomPlacementScheduler) Node(id NodeID) (Node, bool) {
	return Node{}, false
}

// Placement reads one room placement.
func (s *RoomPlacementScheduler) Placement(roomID string) (Placement, bool) {
	return Placement{}, false
}

// FreeSeats reports the seats a node can still hand out.
func (s *RoomPlacementScheduler) FreeSeats(nodeID NodeID) (int, error) {
	return 0, errNotImplemented
}

// Snapshot serialises the node table and every placement.
func (s *RoomPlacementScheduler) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the scheduler from a snapshot, all or nothing.
func (s *RoomPlacementScheduler) Restore(data []byte) error {
	return errNotImplemented
}
