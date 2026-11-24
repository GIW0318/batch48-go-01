package task

import "errors"

// Errors reported by the retransmit cache.
var (
	ErrLimitsUnusable   = errors.New("retransmit limits are not usable")
	ErrPayloadEmpty     = errors.New("packet payload is empty")
	ErrPacketTooLarge   = errors.New("packet exceeds the per packet byte cap")
	ErrDuplicatePacket  = errors.New("packet is already cached")
	ErrNotFound         = errors.New("no cached packet matches the request")
	ErrDuplicateRequest = errors.New("request landed inside the coalescing window")
	ErrRetransmitLimit  = errors.New("packet retransmit budget is exhausted")
	ErrCapacity         = errors.New("cache cannot hold the packet")
)

// Limits caps the cache and the retransmit policy.
type Limits struct {
	MaxPackets     int
	MaxBytes       int
	MaxPacketBytes int
	MaxRetransmits int
	CoalesceMs     int64
	MinTTLMs       int64
}

// Packet is one sent RTP packet kept for retransmission.
type Packet struct {
	SSRC     uint32
	Seq      uint16
	SentMs   int64
	Keyframe bool
	Payload  []byte
}

// CachedPacket is one cache entry with its bookkeeping.
type CachedPacket struct {
	Packet        Packet
	ExtSeq        uint64
	Retransmits   int
	LastRequestMs int64
}

// CacheStats counts cache activity.
type CacheStats struct {
	Stored      int
	Duplicates  int
	Evicted     int
	Expired     int
	Retransmits int
	Coalesced   int
	Misses      int
	Bytes       int
}

// CacheState is the restorable state of one cache.
type CacheState struct {
	Entries []CachedPacket
	Limits  Limits
	RTTMs   map[uint32]int64
	Stats   CacheStats
}

// RetransmitCache keeps the recently sent RTP packets of every SSRC so that
// retransmit requests can be served.
type RetransmitCache struct {
	limits Limits
}

// NewRetransmitCache builds a cache with the given limits.
func NewRetransmitCache(limits Limits) (*RetransmitCache, error) {
	return &RetransmitCache{limits: limits}, nil
}

// Store keeps one sent packet.
func (c *RetransmitCache) Store(p Packet) error {
	return nil
}

// Request serves one retransmit request.
func (c *RetransmitCache) Request(ssrc uint32, seq uint16, nowMs int64) (Packet, error) {
	return Packet{}, nil
}

// Lookup reports one cached packet without consuming retransmit budget.
func (c *RetransmitCache) Lookup(ssrc uint32, seq uint16) (Packet, bool) {
	return Packet{}, false
}

// Purge drops the packets older than the retention window.
func (c *RetransmitCache) Purge(nowMs int64) int {
	return 0
}

// SetLimits replaces the cache and retransmit limits.
func (c *RetransmitCache) SetLimits(limits Limits) error {
	return nil
}

// Pending reports how many packets are cached.
func (c *RetransmitCache) Pending() int {
	return 0
}

// Bytes reports the payload bytes held by the cache.
func (c *RetransmitCache) Bytes() int {
	return 0
}

// Snapshot captures the restorable state.
func (c *RetransmitCache) Snapshot() (CacheState, error) {
	return CacheState{}, nil
}

// Restore loads a previously captured state.
func (c *RetransmitCache) Restore(st CacheState) error {
	return nil
}

// Stats reports the cache counters.
func (c *RetransmitCache) Stats() CacheStats {
	return CacheStats{}
}
