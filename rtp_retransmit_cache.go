package task

import (
	"errors"
	"sort"
	"sync"
)

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

type cacheEntry struct {
	packet        Packet
	extSeq        uint64
	retransmits   int
	lastRequestMs int64
	order         uint64
}

type ssrcStream struct {
	highExt     uint64
	highSeq     uint16
	initialized bool
}

// RetransmitCache keeps the recently sent RTP packets of every SSRC so that
// retransmit requests can be served.
type RetransmitCache struct {
	mu sync.Mutex

	limits  Limits
	entries []*cacheEntry
	index   map[uint32]map[uint64]*cacheEntry
	streams map[uint32]*ssrcStream
	rttMs   map[uint32]int64

	stats     CacheStats
	bytes     int
	nextOrder uint64
}

func validLimits(limits Limits) bool {
	return limits.MaxPackets > 0 &&
		limits.MaxBytes > 0 &&
		limits.MaxPacketBytes > 0 &&
		limits.MaxRetransmits > 0 &&
		limits.CoalesceMs >= 0
}

func clonePayload(payload []byte) []byte {
	if payload == nil {
		return nil
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out
}

func clonePacket(p Packet) Packet {
	p.Payload = clonePayload(p.Payload)
	return p
}

// NewRetransmitCache builds a cache with the given limits.
func NewRetransmitCache(limits Limits) (*RetransmitCache, error) {
	if !validLimits(limits) {
		return nil, ErrLimitsUnusable
	}
	return &RetransmitCache{
		limits:  limits,
		index:   make(map[uint32]map[uint64]*cacheEntry),
		streams: make(map[uint32]*ssrcStream),
		rttMs:   make(map[uint32]int64),
	}, nil
}

// extendedSeqLocked resolves seq against the highest extended sequence seen
// for the SSRC. An out-of-order sequence can point before the stream's first
// packet; in that case all entries of the stream are rebased once so that the
// extended sequence stays non-negative.
func (c *RetransmitCache) extendedSeqLocked(ssrc uint32, seq uint16, mayRebase bool) uint64 {
	st, ok := c.streams[ssrc]
	if !ok {
		c.streams[ssrc] = &ssrcStream{
			highExt:     uint64(seq),
			highSeq:     seq,
			initialized: true,
		}
		return uint64(seq)
	}

	delta := int64(int16(seq - st.highSeq))
	ext := int64(st.highExt) + delta
	if ext < 0 && mayRebase {
		c.rebaseStreamLocked(ssrc, 1<<16)
		st = c.streams[ssrc]
		ext = int64(st.highExt) + delta
	}
	if ext < 0 {
		return 0
	}

	resolved := uint64(ext)
	if resolved > st.highExt {
		st.highExt = resolved
		st.highSeq = seq
	}
	return resolved
}

func (c *RetransmitCache) resolveSeqLocked(ssrc uint32, seq uint16) (uint64, bool) {
	st, ok := c.streams[ssrc]
	if !ok || !st.initialized {
		return 0, false
	}
	ext := int64(st.highExt) + int64(int16(seq-st.highSeq))
	if ext < 0 {
		return 0, false
	}
	return uint64(ext), true
}

func (c *RetransmitCache) rebaseStreamLocked(ssrc uint32, shift uint64) {
	st, ok := c.streams[ssrc]
	if !ok {
		return
	}
	st.highExt += shift
	oldIndex := c.index[ssrc]
	newIndex := make(map[uint64]*cacheEntry, len(oldIndex))
	for _, entry := range oldIndex {
		entry.extSeq += shift
		newIndex[entry.extSeq] = entry
	}
	c.index[ssrc] = newIndex
}

func (c *RetransmitCache) unlinkEntryLocked(entry *cacheEntry) {
	if byExt, ok := c.index[entry.packet.SSRC]; ok {
		delete(byExt, entry.extSeq)
		if len(byExt) == 0 {
			delete(c.index, entry.packet.SSRC)
		}
	}
	c.bytes -= len(entry.packet.Payload)
}

func (c *RetransmitCache) removeEntryLocked(entry *cacheEntry) bool {
	idx := -1
	for i, candidate := range c.entries {
		if candidate == entry {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}

	c.entries = append(c.entries[:idx], c.entries[idx+1:]...)
	c.unlinkEntryLocked(entry)
	return true
}

func (c *RetransmitCache) pickEvictionLocked() *cacheEntry {
	var oldestNormal, oldestKeyframe *cacheEntry
	for _, entry := range c.entries {
		if entry.packet.Keyframe {
			if oldestKeyframe == nil || entry.packet.SentMs < oldestKeyframe.packet.SentMs {
				oldestKeyframe = entry
			}
			continue
		}
		if oldestNormal == nil || entry.packet.SentMs < oldestNormal.packet.SentMs {
			oldestNormal = entry
		}
	}
	if oldestNormal != nil {
		return oldestNormal
	}
	return oldestKeyframe
}

func (c *RetransmitCache) enforceLimitsLocked() {
	for len(c.entries) > c.limits.MaxPackets || c.bytes > c.limits.MaxBytes {
		entry := c.pickEvictionLocked()
		if entry == nil {
			return
		}
		if !c.removeEntryLocked(entry) {
			return
		}
		c.stats.Evicted++
	}
}

// Store keeps one sent packet.
func (c *RetransmitCache) Store(p Packet) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(p.Payload) == 0 {
		return ErrPayloadEmpty
	}
	if len(p.Payload) > c.limits.MaxPacketBytes {
		return ErrPacketTooLarge
	}
	if len(p.Payload) > c.limits.MaxBytes {
		return ErrCapacity
	}

	ext := c.extendedSeqLocked(p.SSRC, p.Seq, true)
	byExt := c.index[p.SSRC]
	if byExt != nil {
		if _, exists := byExt[ext]; exists {
			c.stats.Duplicates++
			return ErrDuplicatePacket
		}
	}

	entry := &cacheEntry{
		packet: Packet{
			SSRC:     p.SSRC,
			Seq:      p.Seq,
			SentMs:   p.SentMs,
			Keyframe: p.Keyframe,
			Payload:  clonePayload(p.Payload),
		},
		extSeq: ext,
		order:  c.nextOrder,
	}
	c.nextOrder++

	if byExt == nil {
		byExt = make(map[uint64]*cacheEntry)
		c.index[p.SSRC] = byExt
	}
	byExt[ext] = entry
	c.entries = append(c.entries, entry)
	c.bytes += len(entry.packet.Payload)
	c.stats.Stored++

	c.enforceLimitsLocked()
	return nil
}

// Request serves one retransmit request.
func (c *RetransmitCache) Request(ssrc uint32, seq uint16, nowMs int64) (Packet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ext, resolved := c.resolveSeqLocked(ssrc, seq)
	entry := (*cacheEntry)(nil)
	if resolved {
		entry = c.index[ssrc][ext]
	}
	if entry == nil {
		c.stats.Misses++
		return Packet{}, ErrNotFound
	}

	if c.limits.CoalesceMs > 0 &&
		entry.retransmits > 0 &&
		nowMs >= entry.lastRequestMs &&
		nowMs-entry.lastRequestMs < c.limits.CoalesceMs {
		c.stats.Coalesced++
		return Packet{}, ErrDuplicateRequest
	}
	if entry.retransmits >= c.limits.MaxRetransmits {
		return Packet{}, ErrRetransmitLimit
	}

	entry.retransmits++
	entry.lastRequestMs = nowMs
	c.stats.Retransmits++
	return clonePacket(entry.packet), nil
}

// Lookup reports one cached packet without consuming retransmit budget.
func (c *RetransmitCache) Lookup(ssrc uint32, seq uint16) (Packet, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ext, resolved := c.resolveSeqLocked(ssrc, seq)
	if !resolved {
		return Packet{}, false
	}
	entry := c.index[ssrc][ext]
	if entry == nil {
		return Packet{}, false
	}
	return clonePacket(entry.packet), true
}

// Purge drops the packets older than the retention window.
func (c *RetransmitCache) Purge(nowMs int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) == 0 {
		return 0
	}

	expired := make([]*cacheEntry, 0)
	for _, entry := range c.entries {
		ttl := c.limits.MinTTLMs
		if rtt, ok := c.rttMs[entry.packet.SSRC]; ok {
			if rttTTL := 4 * rtt; rttTTL > ttl {
				ttl = rttTTL
			}
		}
		if ttl < 0 {
			ttl = 0
		}
		if nowMs-entry.packet.SentMs > ttl {
			expired = append(expired, entry)
		}
	}

	sort.SliceStable(expired, func(i, j int) bool {
		if expired[i].packet.SentMs != expired[j].packet.SentMs {
			return expired[i].packet.SentMs < expired[j].packet.SentMs
		}
		return expired[i].order < expired[j].order
	})

	remove := make(map[*cacheEntry]struct{}, len(expired))
	for _, entry := range expired {
		remove[entry] = struct{}{}
	}

	purged := 0
	kept := c.entries[:0]
	for _, entry := range c.entries {
		if _, ok := remove[entry]; ok {
			c.unlinkEntryLocked(entry)
			c.stats.Expired++
			purged++
			continue
		}
		kept = append(kept, entry)
	}
	c.entries = kept
	return purged
}

// SetLimits replaces the cache and retransmit limits.
func (c *RetransmitCache) SetLimits(limits Limits) error {
	if !validLimits(limits) {
		return ErrLimitsUnusable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.limits = limits
	c.enforceLimitsLocked()
	return nil
}

// Pending reports how many packets are cached.
func (c *RetransmitCache) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Bytes reports the payload bytes held by the cache.
func (c *RetransmitCache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Snapshot captures the restorable state.
func (c *RetransmitCache) Snapshot() (CacheState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries := make([]CachedPacket, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, CachedPacket{
			Packet:        clonePacket(entry.packet),
			ExtSeq:        entry.extSeq,
			Retransmits:   entry.retransmits,
			LastRequestMs: entry.lastRequestMs,
		})
	}

	rtts := make(map[uint32]int64, len(c.rttMs))
	for ssrc, rtt := range c.rttMs {
		rtts[ssrc] = rtt
	}

	stats := c.stats
	stats.Bytes = c.bytes
	return CacheState{
		Entries: entries,
		Limits:  c.limits,
		RTTMs:   rtts,
		Stats:   stats,
	}, nil
}

func (c *RetransmitCache) nextExtForRestore(stream *ssrcStream, cp CachedPacket) uint64 {
	if cp.ExtSeq != 0 {
		return cp.ExtSeq
	}
	if !stream.initialized {
		return uint64(cp.Packet.Seq)
	}
	ext := int64(stream.highExt) + int64(int16(cp.Packet.Seq-stream.highSeq))
	if ext < 0 {
		return 0
	}
	return uint64(ext)
}

// Restore loads a previously captured state.
func (c *RetransmitCache) Restore(st CacheState) error {
	if !validLimits(st.Limits) {
		return ErrLimitsUnusable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entries := make([]*cacheEntry, 0, len(st.Entries))
	index := make(map[uint32]map[uint64]*cacheEntry)
	streams := make(map[uint32]*ssrcStream)
	totalBytes := 0
	var order uint64

	for _, cp := range st.Entries {
		stream := streams[cp.Packet.SSRC]
		if stream == nil {
			stream = &ssrcStream{}
			streams[cp.Packet.SSRC] = stream
		}
		byExt := index[cp.Packet.SSRC]
		if byExt == nil {
			byExt = make(map[uint64]*cacheEntry)
			index[cp.Packet.SSRC] = byExt
		}

		ext := c.nextExtForRestore(stream, cp)
		if _, exists := byExt[ext]; exists {
			return ErrDuplicatePacket
		}

		entry := &cacheEntry{
			packet:        clonePacket(cp.Packet),
			extSeq:        ext,
			retransmits:   cp.Retransmits,
			lastRequestMs: cp.LastRequestMs,
			order:         order,
		}
		order++
		byExt[ext] = entry
		entries = append(entries, entry)
		totalBytes += len(entry.packet.Payload)

		if !stream.initialized {
			stream.initialized = true
			stream.highExt = ext
			stream.highSeq = cp.Packet.Seq
		} else if ext > stream.highExt {
			stream.highExt = ext
			stream.highSeq = cp.Packet.Seq
		}
	}

	rtts := make(map[uint32]int64, len(st.RTTMs))
	for ssrc, rtt := range st.RTTMs {
		rtts[ssrc] = rtt
	}

	c.limits = st.Limits
	c.entries = entries
	c.index = index
	c.streams = streams
	c.rttMs = rtts
	c.stats = st.Stats
	c.bytes = totalBytes
	c.stats.Bytes = totalBytes
	c.nextOrder = order
	c.enforceLimitsLocked()
	return nil
}

// Stats reports the cache counters.
func (c *RetransmitCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	stats.Bytes = c.bytes
	return stats
}
