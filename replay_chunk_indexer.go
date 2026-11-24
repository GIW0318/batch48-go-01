// Package task indexes a replay stream into checksummed blocks, keeps
// keyframe marks and answers seeks by replaying forward from a keyframe.
package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// Frame is one replay frame with an absolute tick number.
type Frame struct {
	Tick    uint64        `json:"tick"`
	Time    time.Duration `json:"time"`
	Payload []byte        `json:"payload"`
	Key     bool          `json:"key"`
}

// BlockInfo describes one committed block.
type BlockInfo struct {
	ID        uint64        `json:"id"`
	StartTick uint64        `json:"start_tick"`
	Count     int           `json:"count"`
	StartTime time.Duration `json:"start_time"`
	EndTime   time.Duration `json:"end_time"`
	Checksum  uint64        `json:"checksum"`
	KeyTicks  []uint64      `json:"key_ticks"`
}

// Cursor points into a committed block.
type Cursor struct {
	BlockID  uint64 `json:"block_id"`
	Offset   int    `json:"offset"`
	Tick     uint64 `json:"tick"`
	Degraded bool   `json:"degraded"`
}

// ReplayConfig bounds block size, keyframe spacing and block count.
type ReplayConfig struct {
	BlockFrames int `json:"block_frames"`
	KeyframeGap int `json:"keyframe_gap"`
	MaxBlocks   int `json:"max_blocks"`
}

// Errors returned by the indexer.
var (
	ErrInvalidConfig    = errors.New("replay config is invalid")
	ErrEmptyPayload     = errors.New("frame payload is required")
	ErrInvalidTick      = errors.New("tick numbers start at one")
	ErrNonMonotonicTick = errors.New("tick must increase")
	ErrTimeRegression   = errors.New("frame time must not go backwards")
	ErrNoData           = errors.New("no committed replay data")
	ErrOutOfRange       = errors.New("time is outside the retained replay")
	ErrBlockNotFound    = errors.New("replay block not found")
	ErrBlockCorrupt     = errors.New("replay block checksum mismatch")
	ErrInvalidKeep      = errors.New("keep count must be positive")
	ErrOffsetOutOfRange = errors.New("cursor offset is outside the block")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

const (
	snapshotVersion1 = 1

	// maxSnapshotBytes bounds the encoded snapshot size the indexer is
	// willing to produce or restore.
	maxSnapshotBytes = 64 << 20

	// maxKeyMarks bounds the total number of keyframe marks a restored
	// snapshot may carry. Runtime marks are already bounded by
	// MaxBlocks*BlockFrames.
	maxKeyMarks = 1_000_000
)

// chunkBlock is one stored block. Once published in ReplayIndexer.blocks its
// frames are treated as immutable; only corruption injection mutates bytes,
// and it does so under the write lock.
type chunkBlock struct {
	info   BlockInfo
	frames []Frame
}

// ReplayIndexer owns the committed blocks and the block under construction.
type ReplayIndexer struct {
	mu sync.RWMutex

	cfg ReplayConfig

	// blocks are the published, checksummed blocks in commit order. Readers
	// only ever observe this slice, never open.
	blocks []*chunkBlock
	open   *chunkBlock

	// nextID is the absolute id assigned to the next newly opened block.
	// Ids are never reused or renumbered after eviction.
	nextID uint64

	lastTick uint64
	lastTime time.Duration
	haveLast bool
}

// NewReplayIndexer builds an indexer for the given layout.
func NewReplayIndexer(cfg ReplayConfig) (*ReplayIndexer, error) {
	if cfg.BlockFrames <= 0 || cfg.KeyframeGap <= 0 || cfg.MaxBlocks <= 0 {
		return nil, ErrInvalidConfig
	}
	return &ReplayIndexer{cfg: cfg}, nil
}

// Append buffers one frame and commits a block once it is full.
func (r *ReplayIndexer) Append(f Frame) error {
	// Validation order is part of the contract: payload, tick range,
	// tick ordering then time ordering.
	if len(f.Payload) == 0 {
		return ErrEmptyPayload
	}
	if f.Tick < 1 {
		return ErrInvalidTick
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.haveLast {
		if f.Tick <= r.lastTick {
			return ErrNonMonotonicTick
		}
		if f.Time < r.lastTime {
			return ErrTimeRegression
		}
	} else if f.Tick != 1 {
		return ErrInvalidTick
	}

	frame := Frame{
		Tick:    f.Tick,
		Time:    f.Time,
		Key:     f.Key,
		Payload: append([]byte(nil), f.Payload...),
	}

	if r.open == nil {
		r.open = &chunkBlock{info: BlockInfo{ID: r.nextID}}
		r.nextID++
	}
	r.open.frames = append(r.open.frames, frame)
	r.lastTick = f.Tick
	r.lastTime = f.Time
	r.haveLast = true

	if len(r.open.frames) >= r.cfg.BlockFrames {
		return r.commitOpenLocked()
	}
	return nil
}

// Flush commits the block under construction, if any.
func (r *ReplayIndexer) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open == nil || len(r.open.frames) == 0 {
		return nil
	}
	return r.commitOpenLocked()
}

// commitOpenLocked finalises the open block (metadata + checksum), publishes
// it and applies the block cap. Everything up to the append to r.blocks is
// private work, so readers either see no block or the fully committed one.
// Callers must hold the write lock.
func (r *ReplayIndexer) commitOpenLocked() error {
	block := r.open
	if block == nil || len(block.frames) == 0 {
		r.open = nil
		return nil
	}

	block.info = buildBlockInfo(block.info.ID, block.frames)

	r.blocks = append(r.blocks, block)
	r.open = nil

	if excess := len(r.blocks) - r.cfg.MaxBlocks; excess > 0 {
		// Reallocate so evicted blocks (and their payloads) can be collected
		// and no aliasing remains through the old backing array.
		r.blocks = append([]*chunkBlock(nil), r.blocks[excess:]...)
	}
	return nil
}

// Blocks lists the committed blocks in commit order.
func (r *ReplayIndexer) Blocks() []BlockInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]BlockInfo, len(r.blocks))
	for i, block := range r.blocks {
		out[i] = cloneBlockInfo(block.info)
	}
	return out
}

// Seek returns the keyframe cursor a player must replay from to reach at.
func (r *ReplayIndexer) Seek(at time.Duration) (Cursor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.blocks) == 0 {
		return Cursor{}, ErrNoData
	}
	last := r.blocks[len(r.blocks)-1]
	if at < 0 || at > last.info.EndTime {
		return Cursor{}, ErrOutOfRange
	}

	plan := r.planLocked(at)
	cursor := Cursor{
		BlockID: r.blocks[plan.keyBlockIndex].info.ID,
		Offset:  plan.keyOffset,
		Tick:    plan.keyTick,
	}

	// A block on the forward path that no longer verifies means the player
	// will have to skip a damaged range; flag the gap instead of presenting
	// the path as a continuous replay.
	for i := plan.keyBlockIndex; i <= plan.targetBlockIndex; i++ {
		if r.isCorruptLocked(r.blocks[i]) {
			cursor.Degraded = true
			break
		}
	}
	return cursor, nil
}

// Locate finds the first frame at or after at by replaying from its keyframe.
func (r *ReplayIndexer) Locate(at time.Duration) (uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.blocks) == 0 {
		return 0, ErrNoData
	}
	last := r.blocks[len(r.blocks)-1]
	if at > last.info.EndTime {
		return 0, ErrOutOfRange
	}

	plan := r.planLocked(at)
	return plan.tick, nil
}

// locatePlan is the result of keyframe selection plus the forward scan.
type locatePlan struct {
	keyBlockIndex    int
	keyOffset        int
	keyTick          uint64
	targetBlockIndex int
	tick             uint64
}

// planLocked selects the newest keyframe at or before at and scans forward to
// the first frame at or after at. When no usable earlier keyframe exists it
// falls back to the first retained frame. Sparse keyframes only make the scan
// longer; they never change the answer. Callers must hold the read lock.
func (r *ReplayIndexer) planLocked(at time.Duration) locatePlan {
	first := r.blocks[0]
	plan := locatePlan{
		keyBlockIndex: 0,
		keyOffset:     0,
		keyTick:       first.frames[0].Tick,
	}

	foundKey := false
	for bi, block := range r.blocks {
		for fi, frame := range block.frames {
			if frame.Key && frame.Time <= at {
				plan.keyBlockIndex = bi
				plan.keyOffset = fi
				plan.keyTick = frame.Tick
				foundKey = true
			}
		}
	}
	if !foundKey {
		plan.keyBlockIndex = 0
		plan.keyOffset = 0
		plan.keyTick = first.frames[0].Tick
	}

	for bi := plan.keyBlockIndex; bi < len(r.blocks); bi++ {
		block := r.blocks[bi]
		start := 0
		if bi == plan.keyBlockIndex {
			start = plan.keyOffset
		}
		for fi := start; fi < len(block.frames); fi++ {
			frame := block.frames[fi]
			if frame.Time >= at {
				plan.targetBlockIndex = bi
				plan.tick = frame.Tick
				return plan
			}
		}
	}

	// at <= last EndTime and monotonic times guarantee a match; keep a
	// conservative fallback anyway.
	lastIndex := len(r.blocks) - 1
	lastBlock := r.blocks[lastIndex]
	plan.targetBlockIndex = lastIndex
	plan.tick = lastBlock.frames[len(lastBlock.frames)-1].Tick
	return plan
}

// ReadBlock returns the frames from the cursor to the end of its block.
func (r *ReplayIndexer) ReadBlock(cursor Cursor) ([]Frame, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	block, _ := r.blockByIDLocked(cursor.BlockID)
	if block == nil {
		return nil, ErrBlockNotFound
	}
	if cursor.Offset < 0 || cursor.Offset >= len(block.frames) {
		return nil, ErrOffsetOutOfRange
	}
	// Never hand out bytes from a block that fails verification: callers
	// must treat this as a gap and skip the block explicitly.
	if r.isCorruptLocked(block) {
		return nil, ErrBlockCorrupt
	}

	source := block.frames[cursor.Offset:]
	frames := make([]Frame, len(source))
	for i, frame := range source {
		frames[i] = Frame{
			Tick:    frame.Tick,
			Time:    frame.Time,
			Key:     frame.Key,
			Payload: append([]byte(nil), frame.Payload...),
		}
	}
	return frames, nil
}

// Verify recomputes the checksum of one block.
func (r *ReplayIndexer) Verify(blockID uint64) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	block, _ := r.blockByIDLocked(blockID)
	if block == nil {
		return ErrBlockNotFound
	}
	if r.isCorruptLocked(block) {
		return ErrBlockCorrupt
	}
	return nil
}

// InjectCorruption marks one block as damaged so Verify reports it.
func (r *ReplayIndexer) InjectCorruption(blockID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	block, _ := r.blockByIDLocked(blockID)
	if block == nil {
		return ErrBlockNotFound
	}
	if len(block.frames) > 0 && len(block.frames[0].Payload) > 0 {
		block.frames[0].Payload[0] ^= 0xFF
		return nil
	}
	// Valid streams always carry payload bytes; keep a fallback so the
	// damage injection never silently does nothing.
	block.info.StartTick ^= 0xFFFFFFFFFFFFFFFF
	return nil
}

// Prune drops the oldest blocks so that at most keep blocks remain.
func (r *ReplayIndexer) Prune(keep int) (int, error) {
	if keep <= 0 {
		return 0, ErrInvalidKeep
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	current := len(r.blocks)
	if keep >= current {
		return 0, nil
	}
	evicted := current - keep
	r.blocks = append([]*chunkBlock(nil), r.blocks[evicted:]...)
	return evicted, nil
}

// snapshotBlock is the serialised form of one block. Damaged records that the
// original checksum no longer matches the frames, allowing corruption to
// survive a restart without laundering it through a recomputed checksum.
type snapshotBlock struct {
	Info    BlockInfo `json:"info"`
	Frames  []Frame   `json:"frames"`
	Damaged bool      `json:"damaged,omitempty"`
}

// snapshotData is the full serialised indexer state.
type snapshotData struct {
	Version     int             `json:"version"`
	Config      ReplayConfig    `json:"config"`
	NextBlockID uint64          `json:"next_block_id"`
	LastTick    uint64          `json:"last_tick"`
	LastTimeNS  int64           `json:"last_time_ns"`
	Blocks      []snapshotBlock `json:"blocks,omitempty"`
	Open        *snapshotBlock  `json:"open,omitempty"`
}

// Snapshot serialises the committed blocks, the open block and the cursor
// bookkeeping.
func (r *ReplayIndexer) Snapshot() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data := snapshotData{
		Version:     snapshotVersion1,
		Config:      r.cfg,
		NextBlockID: r.nextID,
		LastTick:    r.lastTick,
		LastTimeNS:  int64(r.lastTime),
	}

	keyMarks := 0
	payloadBytes := 0
	for _, block := range r.blocks {
		dto := committedBlockToDTO(block, r.isCorruptLocked(block))
		keyMarks += len(dto.Info.KeyTicks)
		payloadBytes += countPayloadBytes(dto.Frames)
		data.Blocks = append(data.Blocks, dto)
	}
	if r.open != nil {
		dto := openBlockToDTO(r.open)
		keyMarks += len(dto.Info.KeyTicks)
		payloadBytes += countPayloadBytes(dto.Frames)
		data.Open = &dto
	}

	if keyMarks > maxKeyMarks {
		return nil, fmt.Errorf("replay snapshot keyframe table exceeds %d marks", maxKeyMarks)
	}
	// Base64 expands payloads by roughly 4/3 plus per-frame overhead.
	if payloadBytes > maxSnapshotBytes {
		return nil, fmt.Errorf("replay snapshot exceeds %d bytes", maxSnapshotBytes)
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode replay snapshot: %w", err)
	}
	if len(encoded) > maxSnapshotBytes {
		return nil, fmt.Errorf("replay snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	return encoded, nil
}

// Restore replaces the whole indexer state from a snapshot, all or nothing.
func (r *ReplayIndexer) Restore(raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty payload", ErrBadSnapshot)
	}
	if len(raw) > maxSnapshotBytes {
		return fmt.Errorf("%w: snapshot exceeds %d bytes", ErrBadSnapshot, maxSnapshotBytes)
	}

	var data snapshotData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("%w: decode snapshot: %v", ErrBadSnapshot, err)
	}

	state, err := decodeSnapshot(&data)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.blocks = state.blocks
	r.open = state.open
	r.nextID = state.nextID
	r.lastTick = state.lastTick
	r.lastTime = state.lastTime
	r.haveLast = state.haveLast
	// Keep the configuration this indexer was constructed with; snapshots
	// produced by this package always carry the same layout anyway.
	r.mu.Unlock()
	return nil
}

// restoredState is the validated replacement state for Restore.
type restoredState struct {
	blocks   []*chunkBlock
	open     *chunkBlock
	nextID   uint64
	lastTick uint64
	lastTime time.Duration
	haveLast bool
}

// decodeSnapshot fully validates the snapshot before any receiver state is
// touched, which gives Restore its all-or-nothing guarantee.
func decodeSnapshot(data *snapshotData) (*restoredState, error) {
	if data.Version != snapshotVersion1 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, data.Version)
	}

	state := &restoredState{}
	var prevTick uint64
	var prevTime time.Duration
	havePrev := false
	keyMarks := 0
	var maxID uint64
	haveID := false

	considerFrame := func(frame Frame) error {
		if len(frame.Payload) == 0 {
			return fmt.Errorf("%w: frame payload is required", ErrBadSnapshot)
		}
		if frame.Tick < 1 {
			return fmt.Errorf("%w: tick numbers start at one", ErrBadSnapshot)
		}
		if havePrev {
			if frame.Tick <= prevTick {
				return fmt.Errorf("%w: tick must increase", ErrBadSnapshot)
			}
			if frame.Time < prevTime {
				return fmt.Errorf("%w: frame time must not regress", ErrBadSnapshot)
			}
		}
		// The first retained frame does not have to be tick 1: pruning
		// evicts the oldest blocks while absolute tick numbering survives.
		prevTick = frame.Tick
		prevTime = frame.Time
		havePrev = true
		return nil
	}

	parseBlock := func(dto snapshotBlock, expectCommitted bool) (*chunkBlock, error) {
		if len(dto.Frames) == 0 {
			return nil, fmt.Errorf("%w: block %d has no frames", ErrBadSnapshot, dto.Info.ID)
		}
		if len(dto.Frames) > 0 && dto.Info.Count != len(dto.Frames) {
			return nil, fmt.Errorf("%w: block %d count mismatch", ErrBadSnapshot, dto.Info.ID)
		}

		frames := make([]Frame, len(dto.Frames))
		for i, frame := range dto.Frames {
			if err := considerFrame(frame); err != nil {
				return nil, err
			}
			frames[i] = Frame{
				Tick:    frame.Tick,
				Time:    frame.Time,
				Key:     frame.Key,
				Payload: append([]byte(nil), frame.Payload...),
			}
		}

		derived := buildBlockInfo(dto.Info.ID, frames)
		if dto.Info.StartTick != derived.StartTick ||
			dto.Info.StartTime != derived.StartTime ||
			dto.Info.EndTime != derived.EndTime ||
			!equalUint64s(dto.Info.KeyTicks, derived.KeyTicks) {
			return nil, fmt.Errorf("%w: block %d metadata mismatch", ErrBadSnapshot, dto.Info.ID)
		}

		if dto.Info.Checksum != derived.Checksum {
			if !dto.Damaged {
				return nil, fmt.Errorf("%w: block %d checksum mismatch", ErrBadSnapshot, dto.Info.ID)
			}
			// Damaged block: keep the original checksum so Verify keeps
			// reporting ErrBlockCorrupt after restart.
		}

		keyMarks += len(derived.KeyTicks)
		if keyMarks > maxKeyMarks {
			return nil, fmt.Errorf("%w: keyframe table exceeds %d marks", ErrBadSnapshot, maxKeyMarks)
		}

		if expectCommitted {
			if haveID && dto.Info.ID <= maxID {
				return nil, fmt.Errorf("%w: block ids must stay ordered", ErrBadSnapshot)
			}
		}
		if !haveID || dto.Info.ID > maxID {
			maxID = dto.Info.ID
			haveID = true
		}

		info := dto.Info
		info.KeyTicks = append([]uint64(nil), dto.Info.KeyTicks...)
		return &chunkBlock{info: info, frames: frames}, nil
	}

	for _, dto := range data.Blocks {
		block, err := parseBlock(dto, true)
		if err != nil {
			return nil, err
		}
		state.blocks = append(state.blocks, block)
	}

	if data.Open != nil {
		open, err := parseBlock(*data.Open, false)
		if err != nil {
			return nil, err
		}
		if len(state.blocks) > 0 && open.info.ID <= state.blocks[len(state.blocks)-1].info.ID {
			return nil, fmt.Errorf("%w: open block id is not after committed blocks", ErrBadSnapshot)
		}
		state.open = open
	}

	if haveID {
		state.nextID = maxID + 1
	} else {
		state.nextID = 0
	}
	// Trust the persisted counter when it is consistent and ahead of every
	// stored id; otherwise the derived max+1 keeps ids monotonic.
	if data.NextBlockID >= state.nextID {
		state.nextID = data.NextBlockID
	}

	state.haveLast = havePrev
	state.lastTick = prevTick
	state.lastTime = prevTime
	return state, nil
}

// blockByIDLocked finds a committed block and its slice index.
func (r *ReplayIndexer) blockByIDLocked(blockID uint64) (*chunkBlock, int) {
	for i, block := range r.blocks {
		if block.info.ID == blockID {
			return block, i
		}
	}
	return nil, -1
}

// isCorruptLocked recomputes the block checksum.
func (r *ReplayIndexer) isCorruptLocked(block *chunkBlock) bool {
	return computeChecksum(block.info.ID, block.frames, block.info.KeyTicks) != block.info.Checksum
}

// buildBlockInfo derives the full metadata (including checksum) for frames.
func buildBlockInfo(id uint64, frames []Frame) BlockInfo {
	info := BlockInfo{
		ID:        id,
		StartTick: frames[0].Tick,
		Count:     len(frames),
		StartTime: frames[0].Time,
		EndTime:   frames[len(frames)-1].Time,
	}
	for _, frame := range frames {
		if frame.Key {
			info.KeyTicks = append(info.KeyTicks, frame.Tick)
		}
	}
	info.Checksum = computeChecksum(id, frames, info.KeyTicks)
	return info
}

// checksumSalt avoids an all-zero checksum for degenerate inputs.
const checksumSalt uint64 = 0x9E3779B97F4A7C15

// computeChecksum hashes the block identity and every frame field.
func computeChecksum(id uint64, frames []Frame, keyTicks []uint64) uint64 {
	h := fnv.New64a()
	var eight [8]byte

	writeUint64 := func(value uint64) {
		binary.LittleEndian.PutUint64(eight[:], value)
		_, _ = h.Write(eight[:])
	}

	writeUint64(checksumSalt)
	writeUint64(id)
	writeUint64(uint64(len(frames)))
	for _, frame := range frames {
		writeUint64(frame.Tick)
		writeUint64(uint64(frame.Time))
		if frame.Key {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{0})
		}
		writeUint64(uint64(len(frame.Payload)))
		_, _ = h.Write(frame.Payload)
	}
	writeUint64(uint64(len(keyTicks)))
	for _, tick := range keyTicks {
		writeUint64(tick)
	}

	sum := h.Sum64()
	if sum == 0 {
		return checksumSalt
	}
	return sum
}

func cloneBlockInfo(info BlockInfo) BlockInfo {
	if info.KeyTicks != nil {
		info.KeyTicks = append([]uint64(nil), info.KeyTicks...)
	}
	return info
}

func committedBlockToDTO(block *chunkBlock, damaged bool) snapshotBlock {
	return snapshotBlock{
		Info:    cloneBlockInfo(block.info),
		Frames:  cloneFrames(block.frames),
		Damaged: damaged,
	}
}

// openBlockToDTO finalises a provisional copy of the open block metadata so
// the serialised open block is internally consistent.
func openBlockToDTO(block *chunkBlock) snapshotBlock {
	frames := cloneFrames(block.frames)
	return snapshotBlock{
		Info:   buildBlockInfo(block.info.ID, frames),
		Frames: frames,
	}
}

func cloneFrames(frames []Frame) []Frame {
	out := make([]Frame, len(frames))
	for i, frame := range frames {
		out[i] = Frame{
			Tick:    frame.Tick,
			Time:    frame.Time,
			Key:     frame.Key,
			Payload: append([]byte(nil), frame.Payload...),
		}
	}
	return out
}

func countPayloadBytes(frames []Frame) int {
	total := 0
	for _, frame := range frames {
		total += len(frame.Payload)
	}
	return total
}

func equalUint64s(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
