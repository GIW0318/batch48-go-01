// Package task reassembles application messages from numbered fragments that
// arrive interleaved, out of order or twice, while bounding memory and
// skipping head holes that waited too long.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// Errors reported by the collector.
var (
	ErrBadLimits       = errors.New("limits are invalid")
	ErrBadFragment     = errors.New("fragment is invalid")
	ErrTooManySegments = errors.New("message declares too many segments")
	ErrMessageTooLarge = errors.New("message does not fit the global byte budget")
	ErrCountMismatch   = errors.New("fragment segment count disagrees with the message")
	ErrSegmentConflict = errors.New("fragment conflicts with the received content")
	ErrUnknownMessage  = errors.New("message is unknown")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Limits caps a collector.
type Limits struct {
	MaxSegmentsPerMessage int
	MaxPending            int
	MaxBytes              int
	MaxIdle               time.Duration
	HeadHoleTimeout       time.Duration
}

// DefaultLimits returns the operating limits of a collector.
func DefaultLimits() Limits {
	return Limits{
		MaxSegmentsPerMessage: 256,
		MaxPending:            64,
		MaxBytes:              1 << 20,
		MaxIdle:               time.Minute,
		HeadHoleTimeout:       5 * time.Second,
	}
}

// Fragment is one numbered piece of a message.
type Fragment struct {
	MessageID    uint64
	SegmentIndex uint32
	SegmentCount uint32
	Payload      []byte
	Arrived      time.Time
}

// Message is a fully collected message.
type Message struct {
	MessageID uint64
	Payload   []byte
	Segments  int
	Skipped   int
}

// PendingInfo is a read-only view of one unfinished message.
type PendingInfo struct {
	MessageID  uint64
	Received   int
	Skipped    int
	Bytes      int
	FirstSeen  time.Time
	LastActive time.Time
}

type pendingMsg struct {
	id        uint64
	count     uint32
	got       map[uint32][]byte
	skipped   map[uint32]bool
	bytes     int
	first     time.Time
	last      time.Time
	activity  uint64
	headIdx   uint32
	headSet   bool
	headSince time.Time
	complete  bool
}

// HoleCollector owns every unfinished message and the delivery queue.
type HoleCollector struct {
	mu         sync.Mutex
	lim        Limits
	msgs       map[uint64]*pendingMsg
	activity   uint64
	evicted    []uint64
	skippedIDs []uint64
	delivered  uint64
}

// NewHoleCollector builds an empty collector.
func NewHoleCollector(lim Limits) *HoleCollector {
	return &HoleCollector{
		lim:  lim,
		msgs: make(map[uint64]*pendingMsg),
	}
}

// Add inserts one fragment and returns a message when the oldest outstanding
// message becomes deliverable.
func (g *HoleCollector) Add(f Fragment) (Message, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if f.SegmentCount == 0 || f.SegmentIndex >= f.SegmentCount || len(f.Payload) == 0 {
		return Message{}, false, ErrBadFragment
	}
	if int64(f.SegmentCount) > int64(g.lim.MaxSegmentsPerMessage) {
		return Message{}, false, ErrTooManySegments
	}

	arrived := f.Arrived
	if arrived.IsZero() {
		arrived = time.Now()
	}

	m := g.msgs[f.MessageID]
	if m != nil {
		if m.count != f.SegmentCount {
			return Message{}, false, ErrCountMismatch
		}
		if old, ok := m.got[f.SegmentIndex]; ok {
			if !bytes.Equal(old, f.Payload) {
				delete(g.msgs, m.id)
				return Message{}, false, ErrSegmentConflict
			}
			g.touchLocked(m, arrived)
			msg, ok := g.deliverLocked()
			return msg, ok, nil
		}
		if m.skipped[f.SegmentIndex] {
			return Message{}, false, nil
		}
	}

	addBytes := len(f.Payload)
	if addBytes > g.lim.MaxBytes {
		return Message{}, false, ErrMessageTooLarge
	}
	if m != nil && m.bytes+addBytes > g.lim.MaxBytes {
		return Message{}, false, ErrMessageTooLarge
	}
	addCount := 0
	if m == nil {
		addCount = 1
	}
	if err := g.makeRoomLocked(addCount, addBytes, m); err != nil {
		return Message{}, false, err
	}

	if m == nil {
		m = &pendingMsg{
			id:        f.MessageID,
			count:     f.SegmentCount,
			got:       make(map[uint32][]byte),
			skipped:   make(map[uint32]bool),
			first:     arrived,
			last:      arrived,
			headIdx:   0,
			headSet:   true,
			headSince: arrived,
		}
		g.msgs[m.id] = m
	}

	m.got[f.SegmentIndex] = append([]byte(nil), f.Payload...)
	m.bytes += addBytes
	g.touchLocked(m, arrived)
	m.advanceHead(arrived)

	msg, ok := g.deliverLocked()
	return msg, ok, nil
}

// Close drops one unfinished message.
func (g *HoleCollector) Close(messageID uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.msgs[messageID]; !ok {
		return ErrUnknownMessage
	}
	delete(g.msgs, messageID)
	return nil
}

// Sweep evicts idle messages, then skips head holes that waited past the
// timeout. It returns the identifiers evicted for inactivity.
func (g *HoleCollector) Sweep(now time.Time) []uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	var evicted []uint64
	if g.lim.MaxIdle > 0 {
		ids := make([]uint64, 0, len(g.msgs))
		for id, m := range g.msgs {
			if now.Sub(m.last) >= g.lim.MaxIdle {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			delete(g.msgs, id)
			g.evicted = append(g.evicted, id)
			evicted = append(evicted, id)
		}
	}

	if g.lim.HeadHoleTimeout > 0 {
		ids := make([]uint64, 0, len(g.msgs))
		for id := range g.msgs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			m := g.msgs[id]
			if m == nil || m.complete || !m.headSet || m.headIdx >= m.count {
				continue
			}
			if now.Sub(m.headSince) < g.lim.HeadHoleTimeout {
				continue
			}

			m.skipped[m.headIdx] = true
			m.headIdx++
			for m.headIdx < m.count {
				if _, ok := m.got[m.headIdx]; ok {
					m.headIdx++
					continue
				}
				if m.skipped[m.headIdx] {
					m.headIdx++
					continue
				}
				break
			}
			m.complete = m.isComplete()
			if m.headIdx < m.count {
				m.headSet = true
				m.headSince = now
			}
			g.recordSkippedLocked(id)
		}
	}

	return evicted
}

// Poll removes and returns the next deliverable message.
func (g *HoleCollector) Poll() (Message, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliverLocked()
}

// Pending lists unfinished messages ordered by identifier.
func (g *HoleCollector) Pending() []PendingInfo {
	g.mu.Lock()
	defer g.mu.Unlock()

	ids := make([]uint64, 0, len(g.msgs))
	for id := range g.msgs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]PendingInfo, 0, len(ids))
	for _, id := range ids {
		m := g.msgs[id]
		out = append(out, PendingInfo{
			MessageID:  m.id,
			Received:   len(m.got),
			Skipped:    len(m.skipped),
			Bytes:      m.bytes,
			FirstSeen:  m.first,
			LastActive: m.last,
		})
	}
	return out
}

// PendingCount reports how many messages are unfinished.
func (g *HoleCollector) PendingCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.msgs)
}

// PendingBytes reports how many payload bytes the unfinished messages hold.
func (g *HoleCollector) PendingBytes() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pendingBytesLocked()
}

// Ready reports how many collected messages are fully assembled.
func (g *HoleCollector) Ready() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	n := 0
	for _, m := range g.msgs {
		if m.complete {
			n++
		}
	}
	return n
}

// Evicted lists the identifiers dropped by the capacity or idle policy.
func (g *HoleCollector) Evicted() []uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]uint64(nil), g.evicted...)
}

// SkippedIDs lists the messages that had a head hole skipped.
func (g *HoleCollector) SkippedIDs() []uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]uint64(nil), g.skippedIDs...)
}

// Delivered reports how many messages have been returned to the caller.
func (g *HoleCollector) Delivered() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.delivered
}

// Snapshot serialises every unfinished message, its bitmap and its activity
// order.
func (g *HoleCollector) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := snapshot{
		Version:    snapshotVersion,
		Activity:   g.activity,
		Evicted:    append([]uint64(nil), g.evicted...),
		SkippedIDs: append([]uint64(nil), g.skippedIDs...),
		Delivered:  g.delivered,
	}

	ids := make([]uint64, 0, len(g.msgs))
	for id := range g.msgs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	snap.Messages = make([]snapshotMessage, 0, len(ids))
	for _, id := range ids {
		m := g.msgs[id]
		sm := snapshotMessage{
			ID:        m.id,
			Count:     m.count,
			Got:       make(map[uint32][]byte, len(m.got)),
			Skipped:   make([]uint32, 0, len(m.skipped)),
			Bytes:     m.bytes,
			First:     m.first,
			Last:      m.last,
			Activity:  m.activity,
			HeadIdx:   m.headIdx,
			HeadSet:   m.headSet,
			HeadSince: m.headSince,
			Complete:  m.complete,
		}
		for idx, p := range m.got {
			sm.Got[idx] = append([]byte(nil), p...)
		}
		for idx := range m.skipped {
			sm.Skipped = append(sm.Skipped, idx)
		}
		sort.Slice(sm.Skipped, func(i, j int) bool { return sm.Skipped[i] < sm.Skipped[j] })
		snap.Messages = append(snap.Messages, sm)
	}

	return json.Marshal(snap)
}

// Restore rebuilds the collector from a snapshot, either completely or not at
// all.
func (g *HoleCollector) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}

	var snap snapshot
	if err := json.Unmarshal(payload, &snap); err != nil || snap.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	msgs := make(map[uint64]*pendingMsg, len(snap.Messages))
	totalBytes := 0
	maxActivity := snap.Activity

	for _, sm := range snap.Messages {
		if _, exists := msgs[sm.ID]; exists {
			return ErrBadSnapshot
		}
		if sm.Count == 0 || int64(sm.Count) > int64(g.lim.MaxSegmentsPerMessage) {
			return ErrBadSnapshot
		}
		if sm.HeadIdx > sm.Count {
			return ErrBadSnapshot
		}
		if sm.Last.Before(sm.First) {
			return ErrBadSnapshot
		}

		m := &pendingMsg{
			id:        sm.ID,
			count:     sm.Count,
			got:       make(map[uint32][]byte, len(sm.Got)),
			skipped:   make(map[uint32]bool, len(sm.Skipped)),
			first:     sm.First,
			last:      sm.Last,
			activity:  sm.Activity,
			headIdx:   sm.HeadIdx,
			headSet:   sm.HeadSet,
			headSince: sm.HeadSince,
		}

		for idx, p := range sm.Got {
			if idx >= sm.Count || len(p) == 0 {
				return ErrBadSnapshot
			}
			m.got[idx] = append([]byte(nil), p...)
			m.bytes += len(p)
		}
		for _, idx := range sm.Skipped {
			if idx >= sm.Count {
				return ErrBadSnapshot
			}
			if _, ok := m.got[idx]; ok {
				return ErrBadSnapshot
			}
			if m.skipped[idx] {
				return ErrBadSnapshot
			}
			m.skipped[idx] = true
		}
		if sm.Bytes != m.bytes {
			return ErrBadSnapshot
		}

		expectedHead := uint32(0)
		for expectedHead < sm.Count {
			if _, ok := m.got[expectedHead]; ok {
				expectedHead++
				continue
			}
			if m.skipped[expectedHead] {
				expectedHead++
				continue
			}
			break
		}
		if sm.HeadIdx != expectedHead {
			return ErrBadSnapshot
		}

		m.complete = m.isComplete()
		if sm.Complete != m.complete {
			return ErrBadSnapshot
		}
		if !m.complete && !m.headSet {
			return ErrBadSnapshot
		}
		if m.activity > maxActivity {
			maxActivity = m.activity
		}
		totalBytes += m.bytes
		msgs[m.id] = m
	}

	if len(msgs) > g.lim.MaxPending {
		return ErrBadSnapshot
	}
	if totalBytes > g.lim.MaxBytes {
		return ErrBadSnapshot
	}

	g.msgs = msgs
	g.activity = maxActivity
	g.evicted = append([]uint64(nil), snap.Evicted...)
	g.skippedIDs = append([]uint64(nil), snap.SkippedIDs...)
	g.delivered = snap.Delivered
	return nil
}

type snapshot struct {
	Version    int               `json:"version"`
	Messages   []snapshotMessage `json:"messages"`
	Activity   uint64            `json:"activity"`
	Evicted    []uint64          `json:"evicted"`
	SkippedIDs []uint64          `json:"skipped_ids"`
	Delivered  uint64            `json:"delivered"`
}

type snapshotMessage struct {
	ID        uint64            `json:"id"`
	Count     uint32            `json:"count"`
	Got       map[uint32][]byte `json:"got"`
	Skipped   []uint32          `json:"skipped"`
	Bytes     int               `json:"bytes"`
	First     time.Time         `json:"first"`
	Last      time.Time         `json:"last"`
	Activity  uint64            `json:"activity"`
	HeadIdx   uint32            `json:"head_idx"`
	HeadSet   bool              `json:"head_set"`
	HeadSince time.Time         `json:"head_since"`
	Complete  bool              `json:"complete"`
}

func (m *pendingMsg) isComplete() bool {
	return m.count > 0 && uint64(len(m.got))+uint64(len(m.skipped)) >= uint64(m.count)
}

// advanceHead moves the head past contiguous received or skipped segments.
// The timer for the new head hole starts at at.
func (m *pendingMsg) advanceHead(at time.Time) {
	advanced := false
	for m.headIdx < m.count {
		if _, ok := m.got[m.headIdx]; ok {
			m.headIdx++
			advanced = true
			continue
		}
		if m.skipped[m.headIdx] {
			m.headIdx++
			advanced = true
			continue
		}
		break
	}
	if m.headIdx < m.count && (advanced || !m.headSet) {
		m.headSet = true
		m.headSince = at
	}
	m.complete = m.isComplete()
}

func (g *HoleCollector) touchLocked(m *pendingMsg, at time.Time) {
	g.activity++
	m.activity = g.activity
	if at.After(m.last) {
		m.last = at
	}
	if at.Before(m.first) {
		m.first = at
	}
}

func (g *HoleCollector) pendingBytesLocked() int {
	total := 0
	for _, m := range g.msgs {
		total += m.bytes
	}
	return total
}

func (g *HoleCollector) makeRoomLocked(addCount, addBytes int, exclude *pendingMsg) error {
	for {
		if len(g.msgs)+addCount <= g.lim.MaxPending &&
			g.pendingBytesLocked()+addBytes <= g.lim.MaxBytes {
			return nil
		}
		victim := g.oldestLocked(exclude)
		if victim == nil {
			return ErrMessageTooLarge
		}
		delete(g.msgs, victim.id)
		g.evicted = append(g.evicted, victim.id)
	}
}

func (g *HoleCollector) oldestLocked(exclude *pendingMsg) *pendingMsg {
	var victim *pendingMsg
	for _, m := range g.msgs {
		if m == exclude {
			continue
		}
		if victim == nil || older(m, victim) {
			victim = m
		}
	}
	return victim
}

func older(a, b *pendingMsg) bool {
	if !a.last.Equal(b.last) {
		return a.last.Before(b.last)
	}
	if a.activity != b.activity {
		return a.activity < b.activity
	}
	return a.id < b.id
}

func (g *HoleCollector) deliverLocked() (Message, bool) {
	var id uint64
	found := false
	for candidate := range g.msgs {
		if !found || candidate < id {
			id = candidate
			found = true
		}
	}
	if !found {
		return Message{}, false
	}
	m := g.msgs[id]
	if !m.complete {
		return Message{}, false
	}

	delete(g.msgs, id)
	g.delivered++

	payload := make([]byte, 0, m.bytes)
	for i := uint32(0); i < m.count; i++ {
		if p, ok := m.got[i]; ok {
			payload = append(payload, p...)
		}
	}
	return Message{
		MessageID: m.id,
		Payload:   payload,
		Segments:  int(m.count),
		Skipped:   len(m.skipped),
	}, true
}

func (g *HoleCollector) recordSkippedLocked(id uint64) {
	for _, seen := range g.skippedIDs {
		if seen == id {
			return
		}
	}
	g.skippedIDs = append(g.skippedIDs, id)
}
