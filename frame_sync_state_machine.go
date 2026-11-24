// Package task drives a frame synchronised battle: it buffers per frame
// inputs, decides whether to wait, advance or degrade, and keeps a
// recomputable checksum per advanced frame.
package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"
)

// Input is one player command addressed to one logical frame.
type Input struct {
	PlayerID string `json:"player_id"`
	Frame    uint64 `json:"frame"`
	Seq      int    `json:"seq"`
	Action   string `json:"action"`
}

// FrameSyncConfig carries the wait and catch up budgets.
type FrameSyncConfig struct {
	MaxWaitFrames  int `json:"max_wait_frames"`
	CatchUpPerTick int `json:"catch_up_per_tick"`
}

// TickResult is what one Tick produced.
type TickResult struct {
	Advanced int    `json:"advanced"`
	Frame    uint64 `json:"frame"`
	Checksum uint64 `json:"checksum"`
	Waited   bool   `json:"waited"`
	Degraded bool   `json:"degraded"`
	Lag      int    `json:"lag"`
}

// Errors returned by the machine.
var (
	ErrInvalidConfig  = errors.New("frame sync config is invalid")
	ErrInvalidFrame   = errors.New("frame numbers start at one")
	ErrUnknownPlayer  = errors.New("player is not part of this battle")
	ErrDuplicateInput = errors.New("input already submitted for this frame")
	ErrFrameClosed    = errors.New("frame has already been applied")
	ErrInvalidBudget  = errors.New("catch up budget must be positive")
	ErrPaused         = errors.New("machine is paused")
	ErrAlreadyPaused  = errors.New("machine is already paused")
	ErrNotPaused      = errors.New("machine is not paused")
	ErrBadSnapshot    = errors.New("snapshot payload is invalid")
)

const (
	snapshotMagic   = "gsb/frame-sync-state-machine"
	snapshotVersion = 1
)

// FrameSyncMachine is the deterministic frame pump of one battle.
type FrameSyncMachine struct {
	mu sync.Mutex

	players   []string
	playerSet map[string]struct{}
	cfg       FrameSyncConfig

	frame     uint64
	paused    bool
	waitTicks int

	pending    map[uint64]map[string]Input
	checksums  map[uint64]uint64
	degraded   []uint64
	degradedAt map[uint64]struct{}
}

type snapshotChecksum struct {
	Frame    uint64 `json:"frame"`
	Checksum uint64 `json:"checksum"`
}

type snapshotInput struct {
	PlayerID string `json:"player_id"`
	Frame    uint64 `json:"frame"`
	Seq      int    `json:"seq"`
	Action   string `json:"action"`
}

type snapshotData struct {
	Magic         string             `json:"magic"`
	Version       int                `json:"version"`
	Frame         uint64             `json:"frame"`
	Paused        bool               `json:"paused"`
	WaitTicks     int                `json:"wait_ticks"`
	Checksums     []snapshotChecksum `json:"checksums"`
	Degraded      []uint64           `json:"degraded_frames"`
	PendingInputs []snapshotInput    `json:"pending_inputs"`
}

// NewFrameSyncMachine builds a machine for the given roster and budgets.
func NewFrameSyncMachine(players []string, cfg FrameSyncConfig) (*FrameSyncMachine, error) {
	if len(players) == 0 || cfg.MaxWaitFrames <= 0 || cfg.CatchUpPerTick <= 0 {
		return nil, ErrInvalidConfig
	}

	roster := make([]string, len(players))
	playerSet := make(map[string]struct{}, len(players))
	for i, player := range players {
		if strings.TrimSpace(player) == "" {
			return nil, ErrInvalidConfig
		}
		if _, exists := playerSet[player]; exists {
			return nil, ErrInvalidConfig
		}
		roster[i] = player
		playerSet[player] = struct{}{}
	}

	return &FrameSyncMachine{
		players:    roster,
		playerSet:  playerSet,
		cfg:        cfg,
		pending:    make(map[uint64]map[string]Input),
		checksums:  make(map[uint64]uint64),
		degradedAt: make(map[uint64]struct{}),
	}, nil
}

// Submit buffers one player input for a future frame.
func (m *FrameSyncMachine) Submit(playerID string, frame uint64, in Input) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.playerSet[playerID]; !ok {
		return ErrUnknownPlayer
	}
	if frame == 0 {
		return ErrInvalidFrame
	}
	if frame <= m.frame {
		return ErrFrameClosed
	}

	inputs, ok := m.pending[frame]
	if !ok {
		inputs = make(map[string]Input, len(m.players))
		m.pending[frame] = inputs
	}
	if _, exists := inputs[playerID]; exists {
		return ErrDuplicateInput
	}

	in.PlayerID = playerID
	in.Frame = frame
	inputs[playerID] = in
	return nil
}

// Tick advances up to the configured budget, degrading after a long wait.
func (m *FrameSyncMachine) Tick(now time.Time) (TickResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := TickResult{
		Frame:    m.frame,
		Checksum: m.checksums[m.frame],
	}
	if m.paused {
		result.Lag = m.lagLocked()
		return result, nil
	}

	blocked := false
	for result.Advanced < m.cfg.CatchUpPerTick {
		next := m.frame + 1
		if m.completeLocked(next) {
			m.applyLocked(next, false)
			result.Advanced++
			continue
		}

		if !blocked {
			blocked = true
			m.waitTicks++
		}
		if m.waitTicks < m.cfg.MaxWaitFrames {
			break
		}

		m.applyLocked(next, true)
		result.Advanced++
		result.Degraded = true
	}

	result.Frame = m.frame
	result.Checksum = m.checksums[m.frame]
	result.Lag = m.lagLocked()
	result.Waited = result.Advanced == 0 && blocked
	return result, nil
}

// WaitDepth counts the frames blocked on a missing player input.
func (m *FrameSyncMachine) WaitDepth() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	highest := m.frame
	for frame := range m.pending {
		if frame > highest {
			highest = frame
		}
	}

	depth := 0
	for frame := m.frame + 1; frame <= highest && frame > m.frame; frame++ {
		if !m.completeLocked(frame) {
			depth++
		}
	}
	if depth == 0 && m.waitTicks > 0 && highest == m.frame {
		depth = 1
	}
	return depth
}

// CatchUp applies at most maxFrames fully collected frames.
func (m *FrameSyncMachine) CatchUp(maxFrames int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if maxFrames <= 0 {
		return 0, ErrInvalidBudget
	}
	if m.paused {
		return 0, ErrPaused
	}

	advanced := 0
	for advanced < maxFrames {
		next := m.frame + 1
		if !m.completeLocked(next) {
			break
		}
		m.applyLocked(next, false)
		advanced++
	}
	return advanced, nil
}

// Pause freezes advancement but keeps buffering inputs.
func (m *FrameSyncMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.paused {
		return ErrAlreadyPaused
	}
	m.paused = true
	return nil
}

// Resume lets Tick advance again.
func (m *FrameSyncMachine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.paused {
		return ErrNotPaused
	}
	m.paused = false
	return nil
}

// Frame reports the last applied frame.
func (m *FrameSyncMachine) Frame() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frame
}

// ChecksumAt reads the stored checksum of one applied frame.
func (m *FrameSyncMachine) ChecksumAt(frame uint64) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if frame == 0 || frame > m.frame {
		return 0, false
	}
	checksum, ok := m.checksums[frame]
	return checksum, ok
}

// DegradedFrames lists the frames that advanced without every input.
func (m *FrameSyncMachine) DegradedFrames() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.degraded) == 0 {
		return nil
	}
	out := make([]uint64, len(m.degraded))
	copy(out, m.degraded)
	return out
}

// Snapshot serialises the frame, the checksum history and the input log.
func (m *FrameSyncMachine) Snapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data := snapshotData{
		Magic:     snapshotMagic,
		Version:   snapshotVersion,
		Frame:     m.frame,
		Paused:    m.paused,
		WaitTicks: m.waitTicks,
		Degraded:  append([]uint64(nil), m.degraded...),
	}

	frames := make([]uint64, 0, len(m.checksums))
	for frame := range m.checksums {
		frames = append(frames, frame)
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	for _, frame := range frames {
		data.Checksums = append(data.Checksums, snapshotChecksum{
			Frame:    frame,
			Checksum: m.checksums[frame],
		})
	}

	for frame, inputs := range m.pending {
		players := make([]string, 0, len(inputs))
		for player := range inputs {
			players = append(players, player)
		}
		sort.Strings(players)
		for _, player := range players {
			in := inputs[player]
			data.PendingInputs = append(data.PendingInputs, snapshotInput{
				PlayerID: player,
				Frame:    frame,
				Seq:      in.Seq,
				Action:   in.Action,
			})
		}
	}
	sort.Slice(data.PendingInputs, func(i, j int) bool {
		if data.PendingInputs[i].Frame != data.PendingInputs[j].Frame {
			return data.PendingInputs[i].Frame < data.PendingInputs[j].Frame
		}
		return data.PendingInputs[i].PlayerID < data.PendingInputs[j].PlayerID
	})

	return json.Marshal(data)
}

// Restore rebuilds the machine from a snapshot, all or nothing.
func (m *FrameSyncMachine) Restore(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var snapshot snapshotData
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Magic != snapshotMagic || snapshot.Version != snapshotVersion ||
		snapshot.WaitTicks < 0 {
		return ErrBadSnapshot
	}

	if uint64(len(snapshot.Checksums)) != snapshot.Frame {
		return ErrBadSnapshot
	}
	checksums := make(map[uint64]uint64, len(snapshot.Checksums))
	for i, entry := range snapshot.Checksums {
		frame := uint64(i) + 1
		if entry.Frame != frame {
			return ErrBadSnapshot
		}
		checksums[frame] = entry.Checksum
	}

	degraded := make([]uint64, 0, len(snapshot.Degraded))
	degradedAt := make(map[uint64]struct{}, len(snapshot.Degraded))
	for i, frame := range snapshot.Degraded {
		if frame == 0 || frame > snapshot.Frame {
			return ErrBadSnapshot
		}
		if i > 0 && frame <= snapshot.Degraded[i-1] {
			return ErrBadSnapshot
		}
		degraded = append(degraded, frame)
		degradedAt[frame] = struct{}{}
	}

	pending := make(map[uint64]map[string]Input)
	for _, entry := range snapshot.PendingInputs {
		if entry.Frame <= snapshot.Frame {
			return ErrBadSnapshot
		}
		if _, ok := m.playerSet[entry.PlayerID]; !ok {
			return ErrBadSnapshot
		}
		inputs, ok := pending[entry.Frame]
		if !ok {
			inputs = make(map[string]Input, len(m.players))
			pending[entry.Frame] = inputs
		}
		if _, exists := inputs[entry.PlayerID]; exists {
			return ErrBadSnapshot
		}
		inputs[entry.PlayerID] = Input{
			PlayerID: entry.PlayerID,
			Frame:    entry.Frame,
			Seq:      entry.Seq,
			Action:   entry.Action,
		}
	}

	m.frame = snapshot.Frame
	m.paused = snapshot.Paused
	m.waitTicks = snapshot.WaitTicks
	m.checksums = checksums
	m.degraded = degraded
	m.degradedAt = degradedAt
	m.pending = pending
	return nil
}

func (m *FrameSyncMachine) completeLocked(frame uint64) bool {
	inputs, ok := m.pending[frame]
	if !ok || len(inputs) != len(m.players) {
		return false
	}
	for _, player := range m.players {
		if _, exists := inputs[player]; !exists {
			return false
		}
	}
	return true
}

func (m *FrameSyncMachine) applyLocked(frame uint64, degraded bool) {
	inputs := m.pending[frame]
	m.checksums[frame] = checksumFor(frame, inputs)
	m.frame = frame
	delete(m.pending, frame)
	m.waitTicks = 0

	if degraded {
		m.degraded = append(m.degraded, frame)
		m.degradedAt[frame] = struct{}{}
	}
}

func (m *FrameSyncMachine) lagLocked() int {
	lag := 0
	for frame := m.frame + 1; ; frame++ {
		if !m.completeLocked(frame) {
			return lag
		}
		lag++
	}
}

func checksumFor(frame uint64, inputs map[string]Input) uint64 {
	h := fnv.New64a()
	writeUint64(h, frame)

	players := make([]string, 0, len(inputs))
	for player := range inputs {
		players = append(players, player)
	}
	sort.Strings(players)
	for _, player := range players {
		in := inputs[player]
		writeString(h, player)
		writeUint64(h, uint64(in.Seq))
		writeString(h, in.Action)
	}
	return h.Sum64()
}

func writeUint64(h interface{ Write([]byte) (int, error) }, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeString(h interface{ Write([]byte) (int, error) }, value string) {
	writeUint64(h, uint64(len(value)))
	_, _ = h.Write([]byte(value))
}
