package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// Sentinels reported by the input redundancy framer.
var (
	ErrPlayerUnknown      = errors.New("player not registered")
	ErrPlayerExists       = errors.New("player already registered")
	ErrInvalidFrame       = errors.New("frame number must be positive")
	ErrInvalidRedundancy  = errors.New("redundancy must be between 1 and 8")
	ErrInvalidBufferLimit = errors.New("buffer limit must be at least one")
	ErrInvalidGrace       = errors.New("afk grace must be at least one")
	ErrInputConflict      = errors.New("conflicting input for the same player and frame")
	ErrBufferFull         = errors.New("pending frame buffer is full")
	ErrBadSnapshot        = errors.New("snapshot payload rejected")
)

const (
	minRedundancy      = 1
	maxRedundancy      = 8
	defaultBufferLimit = 64
	defaultAFKGrace    = 3
	snapshotVersion    = 1
)

// PlayerID identifies one input source.
type PlayerID int

// Input is the per frame input of one player.
type Input struct {
	Player  PlayerID
	MoveX   int
	MoveY   int
	Actions uint32
}

// Packet carries one redundant copy of a frame input.
type Packet struct {
	Player PlayerID
	Frame  uint64
	Copy   int
	Input  Input
}

// FramerStats is the observable framer counter block.
type FramerStats struct {
	Players    int
	Duplicates uint64
	Expired    uint64
	Pending    int
}

// playerState holds the reassembly state of one registered player.
type playerState struct {
	joinFrame      uint64
	consumedBefore uint64
	lastSeen       uint64
	seen           bool
	frames         map[uint64]Input
}

// InputRedundancyFramer encodes and reassembles redundant frame inputs.
type InputRedundancyFramer struct {
	mu          sync.Mutex
	redundancy  int
	bufferLimit int
	afkGrace    int
	players     map[PlayerID]*playerState
	duplicates  uint64
	expired     uint64
}

// NewInputRedundancyFramer builds a framer that repeats each input in n packets.
func NewInputRedundancyFramer(redundancy int) *InputRedundancyFramer {
	if redundancy < minRedundancy {
		redundancy = minRedundancy
	} else if redundancy > maxRedundancy {
		redundancy = maxRedundancy
	}
	return &InputRedundancyFramer{
		redundancy:  redundancy,
		bufferLimit: defaultBufferLimit,
		afkGrace:    defaultAFKGrace,
		players:     make(map[PlayerID]*playerState),
	}
}

// SetRedundancy changes how many packets repeat each input.
func (f *InputRedundancyFramer) SetRedundancy(n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < minRedundancy || n > maxRedundancy {
		return ErrInvalidRedundancy
	}
	f.redundancy = n
	return nil
}

// SetBufferLimit caps the number of pending frames kept per player.
func (f *InputRedundancyFramer) SetBufferLimit(n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 {
		return ErrInvalidBufferLimit
	}
	f.bufferLimit = n
	return nil
}

// SetAFKGrace configures how many frames without input mark a player as afk.
func (f *InputRedundancyFramer) SetAFKGrace(n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 {
		return ErrInvalidGrace
	}
	f.afkGrace = n
	return nil
}

// AddPlayer registers a player that participates from its join frame on.
func (f *InputRedundancyFramer) AddPlayer(player PlayerID, joinFrame uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.players[player]; ok {
		return ErrPlayerExists
	}
	f.players[player] = &playerState{
		joinFrame: joinFrame,
		frames:    make(map[uint64]Input),
	}
	return nil
}

// RemovePlayer drops a player from the readiness requirement.
func (f *InputRedundancyFramer) RemovePlayer(player PlayerID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.players[player]; !ok {
		return ErrPlayerUnknown
	}
	delete(f.players, player)
	return nil
}

// Encode repeats one frame input into the configured number of packets.
func (f *InputRedundancyFramer) Encode(frame uint64, in Input) ([]Packet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.players[in.Player]; !ok {
		return nil, ErrPlayerUnknown
	}
	if frame == 0 {
		return nil, ErrInvalidFrame
	}
	packets := make([]Packet, 0, f.redundancy)
	for i := 0; i < f.redundancy; i++ {
		packets = append(packets, Packet{
			Player: in.Player,
			Frame:  frame,
			Copy:   i,
			Input:  in,
		})
	}
	return packets, nil
}

// Ingest accepts one redundant packet and reports the frame it belongs to.
func (f *InputRedundancyFramer) Ingest(p Packet) (frame uint64, in Input, ok bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	state, ok := f.players[p.Player]
	if !ok {
		return 0, Input{}, false, ErrPlayerUnknown
	}
	if p.Frame == 0 {
		return 0, Input{}, false, ErrInvalidFrame
	}

	// Frames already consumed by DropBefore must never be delivered again.
	if p.Frame < state.consumedBefore {
		f.expired++
		return p.Frame, Input{}, false, nil
	}

	if existing, ok := state.frames[p.Frame]; ok {
		if existing == p.Input {
			f.duplicates++
			return p.Frame, Input{}, false, nil
		}
		return 0, Input{}, false, ErrInputConflict
	}

	if len(state.frames) >= f.bufferLimit {
		return 0, Input{}, false, ErrBufferFull
	}

	state.frames[p.Frame] = p.Input
	if !state.seen || p.Frame > state.lastSeen {
		state.lastSeen = p.Frame
		state.seen = true
	}
	return p.Frame, p.Input, true, nil
}

// Ready reports whether every participating player has delivered a frame.
func (f *InputRedundancyFramer) Ready(frame uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if frame == 0 {
		return false
	}
	for _, state := range f.players {
		// Late joiners only participate from their join frame on.
		if state.joinFrame > frame {
			continue
		}
		if _, ok := state.frames[frame]; ok {
			continue
		}
		// Missing inputs count as empty input once the AFK grace elapses.
		if f.isAFKFilled(state, frame) {
			continue
		}
		return false
	}
	return true
}

// isAFKFilled reports whether frame may be treated as an empty AFK input.
// Caller must hold f.mu and the player participates in frame.
func (f *InputRedundancyFramer) isAFKFilled(state *playerState, frame uint64) bool {
	grace := uint64(f.afkGrace)
	if !state.seen {
		return frame-state.joinFrame > grace
	}
	if frame <= state.lastSeen {
		return false
	}
	return frame-state.lastSeen > grace
}

// DropBefore discards pending frames older than the given frame.
func (f *InputRedundancyFramer) DropBefore(frame uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, state := range f.players {
		if frame > state.consumedBefore {
			state.consumedBefore = frame
		}
		for pending := range state.frames {
			if pending < frame {
				delete(state.frames, pending)
			}
		}
	}
}

// PendingFrames reports how many distinct frames wait for a player.
func (f *InputRedundancyFramer) PendingFrames(player PlayerID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.players[player]
	if !ok {
		return 0
	}
	return len(state.frames)
}

// Stats reports framer counters.
func (f *InputRedundancyFramer) Stats() FramerStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	pending := 0
	for _, state := range f.players {
		pending += len(state.frames)
	}
	return FramerStats{
		Players:    len(f.players),
		Duplicates: f.duplicates,
		Expired:    f.expired,
		Pending:    pending,
	}
}

type playerSnapshot struct {
	ID             PlayerID         `json:"id"`
	JoinFrame      uint64           `json:"join_frame"`
	ConsumedBefore uint64           `json:"consumed_before"`
	LastSeen       uint64           `json:"last_seen"`
	Seen           bool             `json:"seen"`
	Frames         map[uint64]Input `json:"frames"`
}

type framerSnapshot struct {
	Version     int              `json:"version"`
	Redundancy  int              `json:"redundancy"`
	BufferLimit int              `json:"buffer_limit"`
	AFKGrace    int              `json:"afk_grace"`
	Duplicates  uint64           `json:"duplicates"`
	Expired     uint64           `json:"expired"`
	Players     []playerSnapshot `json:"players"`
}

// Snapshot serialises the whole framer state.
func (f *InputRedundancyFramer) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := framerSnapshot{
		Version:     snapshotVersion,
		Redundancy:  f.redundancy,
		BufferLimit: f.bufferLimit,
		AFKGrace:    f.afkGrace,
		Duplicates:  f.duplicates,
		Expired:     f.expired,
		Players:     make([]playerSnapshot, 0, len(f.players)),
	}
	for id, state := range f.players {
		frames := make(map[uint64]Input, len(state.frames))
		for frame, input := range state.frames {
			frames[frame] = input
		}
		snap.Players = append(snap.Players, playerSnapshot{
			ID:             id,
			JoinFrame:      state.joinFrame,
			ConsumedBefore: state.consumedBefore,
			LastSeen:       state.lastSeen,
			Seen:           state.seen,
			Frames:         frames,
		})
	}
	return json.Marshal(snap)
}

// Restore rebuilds the framer state from a snapshot payload.
func (f *InputRedundancyFramer) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}
	var snap framerSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.Version != snapshotVersion ||
		snap.Redundancy < minRedundancy || snap.Redundancy > maxRedundancy ||
		snap.BufferLimit < 1 || snap.AFKGrace < 1 {
		return ErrBadSnapshot
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	players := make(map[PlayerID]*playerState, len(snap.Players))
	for _, saved := range snap.Players {
		if _, exists := players[saved.ID]; exists {
			return ErrBadSnapshot
		}
		if len(saved.Frames) > snap.BufferLimit {
			return ErrBadSnapshot
		}
		frames := saved.Frames
		if frames == nil {
			frames = make(map[uint64]Input)
		}
		players[saved.ID] = &playerState{
			joinFrame:      saved.JoinFrame,
			consumedBefore: saved.ConsumedBefore,
			lastSeen:       saved.LastSeen,
			seen:           saved.Seen,
			frames:         frames,
		}
	}

	f.redundancy = snap.Redundancy
	f.bufferLimit = snap.BufferLimit
	f.afkGrace = snap.AFKGrace
	f.duplicates = snap.Duplicates
	f.expired = snap.Expired
	f.players = players
	return nil
}
