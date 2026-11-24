// Package task bins visible instances into screen tiles for a software
// rasteriser. Every registered instance has to land in each tile its bounds
// touch, buckets hold a fixed number of handles and anything that does not fit
// moves into a global overflow table.
package task

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"sort"
	"sync"
)

// Handle identifies one registered instance.
type Handle int

// TileKey addresses one tile of the screen grid.
type TileKey struct {
	X int
	Y int
}

// Rect is an axis aligned rectangle in screen pixels.
type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Instance is one drawable registered with the binner.
type Instance struct {
	Bounds Rect
	Layer  int
}

// Viewport is the screen extent used for one build.
type Viewport struct {
	Width  int
	Height int
}

// Bins is the per tile bucket table produced by Build.
type Bins struct {
	TileSize int
	Cols     int
	Rows     int
	Tiles    map[TileKey][]Handle
	Overflow []Handle
}

// BinStats reports the shape of the most recent build.
type BinStats struct {
	Instances  int
	Tiles      int
	Placements int
	Overflow   int
}

// Errors reported by the binner.
var (
	ErrBadTileSize   = errors.New("tile size must be positive")
	ErrBadCapacity   = errors.New("bucket capacity must be positive")
	ErrBadBounds     = errors.New("instance bounds are degenerate")
	ErrUnknownHandle = errors.New("instance handle is unknown")
	ErrLimitReached  = errors.New("instance table is full")
	ErrBadViewport   = errors.New("viewport is empty")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// InstanceTileBinner registers instances and bins them per screen tile.
type InstanceTileBinner struct {
	mu        sync.Mutex
	tileSize  int
	capacity  int
	limit     int
	next      Handle
	instances map[Handle]Instance
	bins      Bins
	stats     BinStats
}

// NewInstanceTileBinner builds a binner with the given tile size and bucket capacity.
func NewInstanceTileBinner(tileSize, capacity int) *InstanceTileBinner {
	return &InstanceTileBinner{
		tileSize:  tileSize,
		capacity:  capacity,
		limit:     1024,
		next:      1,
		instances: make(map[Handle]Instance),
	}
}

// Add registers one instance and returns its handle.
func (g *InstanceTileBinner) Add(inst Instance) (Handle, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !validBounds(inst.Bounds) {
		return 0, ErrBadBounds
	}
	if len(g.instances) >= g.limit {
		return 0, ErrLimitReached
	}

	h := g.next
	g.next++
	g.instances[h] = Instance{Bounds: inst.Bounds, Layer: inst.Layer}
	return h, nil
}

// Remove drops one instance from the table and from every bucket it owns.
func (g *InstanceTileBinner) Remove(h Handle) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.instances[h]; !ok {
		return ErrUnknownHandle
	}
	delete(g.instances, h)
	return nil
}

// Move replaces the bounds of an existing instance.
func (g *InstanceTileBinner) Move(h Handle, bounds Rect) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	inst, ok := g.instances[h]
	if !ok {
		return ErrUnknownHandle
	}
	if !validBounds(bounds) {
		return ErrBadBounds
	}
	inst.Bounds = bounds
	g.instances[h] = inst
	return nil
}

// Build bins every registered instance into the tile grid of one viewport.
func (g *InstanceTileBinner) Build(view Viewport) (Bins, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.tileSize <= 0 {
		return Bins{}, ErrBadTileSize
	}
	if g.capacity <= 0 {
		return Bins{}, ErrBadCapacity
	}
	if view.Width <= 0 || view.Height <= 0 {
		return Bins{}, ErrBadViewport
	}

	cols := 1 + (view.Width-1)/g.tileSize
	rows := 1 + (view.Height-1)/g.tileSize

	handles := make([]Handle, 0, len(g.instances))
	for h := range g.instances {
		handles = append(handles, h)
	}
	sort.Slice(handles, func(i, j int) bool { return handles[i] < handles[j] })

	tiles := make(map[TileKey][]Handle)
	overflowSet := make(map[Handle]struct{})
	placements := 0

	for _, h := range handles {
		inst := g.instances[h]
		x0, x1, okX := tileSpan(inst.Bounds.MinX, inst.Bounds.MaxX, g.tileSize, view.Width)
		y0, y1, okY := tileSpan(inst.Bounds.MinY, inst.Bounds.MaxY, g.tileSize, view.Height)
		if !okX || !okY {
			continue
		}

		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				key := TileKey{X: x, Y: y}
				bucket := tiles[key]
				if len(bucket) >= g.capacity {
					overflowSet[h] = struct{}{}
					continue
				}
				tiles[key] = append(bucket, h)
				placements++
			}
		}
	}

	for key, bucket := range tiles {
		sort.Slice(bucket, func(i, j int) bool { return bucket[i] < bucket[j] })
		tiles[key] = bucket
	}

	overflow := make([]Handle, 0, len(overflowSet))
	for h := range overflowSet {
		overflow = append(overflow, h)
	}
	sort.Slice(overflow, func(i, j int) bool { return overflow[i] < overflow[j] })

	g.bins = Bins{
		TileSize: g.tileSize,
		Cols:     cols,
		Rows:     rows,
		Tiles:    tiles,
		Overflow: overflow,
	}
	g.stats = BinStats{
		Instances:  len(g.instances),
		Tiles:      len(tiles),
		Placements: placements,
		Overflow:   len(overflow),
	}
	return cloneBins(g.bins), nil
}

// SetTileSize changes the tile edge in pixels.
func (g *InstanceTileBinner) SetTileSize(px int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if px <= 0 {
		return ErrBadTileSize
	}
	g.tileSize = px
	return nil
}

// SetCapacity changes how many handles one bucket may hold.
func (g *InstanceTileBinner) SetCapacity(n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if n <= 0 {
		return ErrBadCapacity
	}
	g.capacity = n
	return nil
}

// SetInstanceLimit changes how many instances may be registered at once.
func (g *InstanceTileBinner) SetInstanceLimit(n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if n < 0 || n < len(g.instances) {
		return ErrLimitReached
	}
	g.limit = n
	return nil
}

// TileSize reports the configured tile edge in pixels.
func (g *InstanceTileBinner) TileSize() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tileSize
}

// Capacity reports the configured bucket capacity.
func (g *InstanceTileBinner) Capacity() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.capacity
}

// HandleCount reports how many instances are registered.
func (g *InstanceTileBinner) HandleCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.instances)
}

// BoundsOf reports the bounds recorded for one handle.
func (g *InstanceTileBinner) BoundsOf(h Handle) (Rect, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	inst, ok := g.instances[h]
	if !ok {
		return Rect{}, false
	}
	return inst.Bounds, true
}

// TileHandles reports the handles of one tile from the most recent build.
func (g *InstanceTileBinner) TileHandles(key TileKey) []Handle {
	g.mu.Lock()
	defer g.mu.Unlock()

	bucket, ok := g.bins.Tiles[key]
	if !ok {
		return nil
	}
	return append([]Handle(nil), bucket...)
}

// OverflowHandles reports the overflow table of the most recent build.
func (g *InstanceTileBinner) OverflowHandles() []Handle {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Handle(nil), g.bins.Overflow...)
}

// Stats reports the counters of the most recent build.
func (g *InstanceTileBinner) Stats() BinStats {
	g.mu.Lock()
	defer g.mu.Unlock()

	stats := g.stats
	stats.Instances = len(g.instances)
	stats.Tiles = len(g.bins.Tiles)
	stats.Overflow = len(g.bins.Overflow)
	return stats
}

// Snapshot serialises the instance table and the grid settings.
func (g *InstanceTileBinner) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	handles := make([]Handle, 0, len(g.instances))
	for h := range g.instances {
		handles = append(handles, h)
	}
	sort.Slice(handles, func(i, j int) bool { return handles[i] < handles[j] })

	payload := make([]byte, 0, snapshotHeaderSize+len(handles)*snapshotEntrySize+snapshotCRCSize)
	payload = append(payload, snapshotMagic...)
	payload = appendInt64(payload, int64(g.tileSize))
	payload = appendInt64(payload, int64(g.capacity))
	payload = appendInt64(payload, int64(g.limit))
	payload = appendInt64(payload, int64(g.next))
	payload = appendInt64(payload, int64(len(handles)))
	for _, h := range handles {
		inst := g.instances[h]
		payload = appendInt64(payload, int64(h))
		payload = appendInt64(payload, int64(inst.Layer))
		payload = appendFloat64(payload, inst.Bounds.MinX)
		payload = appendFloat64(payload, inst.Bounds.MinY)
		payload = appendFloat64(payload, inst.Bounds.MaxX)
		payload = appendFloat64(payload, inst.Bounds.MaxY)
	}
	var checksum [4]byte
	binary.BigEndian.PutUint32(checksum[:], crc32.ChecksumIEEE(payload))
	payload = append(payload, checksum[:]...)
	return payload, nil
}

// Restore rebuilds the binner from a snapshot, either completely or not at all.
func (g *InstanceTileBinner) Restore(payload []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(payload) < snapshotHeaderSize+snapshotCRCSize {
		return ErrBadSnapshot
	}
	body := payload[:len(payload)-snapshotCRCSize]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(payload[len(payload)-snapshotCRCSize:]) {
		return ErrBadSnapshot
	}
	if len(body) < snapshotHeaderSize || !bytes.Equal(body[:len(snapshotMagic)], []byte(snapshotMagic)) {
		return ErrBadSnapshot
	}

	tileSize := int64(binary.BigEndian.Uint64(body[8:16]))
	capacity := int64(binary.BigEndian.Uint64(body[16:24]))
	limit := int64(binary.BigEndian.Uint64(body[24:32]))
	next := int64(binary.BigEndian.Uint64(body[32:40]))
	count := int64(binary.BigEndian.Uint64(body[40:48]))

	if tileSize <= 0 || capacity <= 0 || limit < 0 || next <= 0 || count < 0 || count > limit {
		return ErrBadSnapshot
	}
	tileSizeInt, ok := checkedInt(tileSize)
	if !ok {
		return ErrBadSnapshot
	}
	capacityInt, ok := checkedInt(capacity)
	if !ok {
		return ErrBadSnapshot
	}
	limitInt, ok := checkedInt(limit)
	if !ok {
		return ErrBadSnapshot
	}
	nextHandle, ok := checkedInt(next)
	if !ok || nextHandle <= 0 {
		return ErrBadSnapshot
	}

	remaining := len(body) - snapshotHeaderSize
	if count > int64(remaining/snapshotEntrySize) || int64(remaining) != count*int64(snapshotEntrySize) {
		return ErrBadSnapshot
	}

	restored := make(map[Handle]Instance, int(count))
	var maxHandle Handle
	offset := snapshotHeaderSize
	for i := int64(0); i < count; i++ {
		h := int64(binary.BigEndian.Uint64(body[offset : offset+8]))
		layer := int64(binary.BigEndian.Uint64(body[offset+8 : offset+16]))
		bounds := Rect{
			MinX: math.Float64frombits(binary.BigEndian.Uint64(body[offset+16 : offset+24])),
			MinY: math.Float64frombits(binary.BigEndian.Uint64(body[offset+24 : offset+32])),
			MaxX: math.Float64frombits(binary.BigEndian.Uint64(body[offset+32 : offset+40])),
			MaxY: math.Float64frombits(binary.BigEndian.Uint64(body[offset+40 : offset+48])),
		}
		offset += snapshotEntrySize

		handleInt, ok := checkedInt(h)
		if !ok || handleInt <= 0 || h >= next {
			return ErrBadSnapshot
		}
		layerInt, ok := checkedInt(layer)
		if !ok || !validBounds(bounds) {
			return ErrBadSnapshot
		}
		handle := Handle(handleInt)
		if _, exists := restored[handle]; exists {
			return ErrBadSnapshot
		}
		restored[handle] = Instance{Bounds: bounds, Layer: layerInt}
		if handle > maxHandle {
			maxHandle = handle
		}
	}
	if count > 0 && Handle(nextHandle) <= maxHandle {
		return ErrBadSnapshot
	}

	g.tileSize = tileSizeInt
	g.capacity = capacityInt
	g.limit = limitInt
	g.next = Handle(nextHandle)
	g.instances = restored
	g.bins = Bins{}
	g.stats = BinStats{Instances: len(restored)}
	return nil
}

const (
	snapshotMagic      = "ITBNSNP1"
	snapshotHeaderSize = 8 + 5*8
	snapshotEntrySize  = 6 * 8
	snapshotCRCSize    = 4
)

func validBounds(r Rect) bool {
	if math.IsNaN(r.MinX) || math.IsNaN(r.MinY) || math.IsNaN(r.MaxX) || math.IsNaN(r.MaxY) {
		return false
	}
	if math.IsInf(r.MinX, 0) || math.IsInf(r.MinY, 0) || math.IsInf(r.MaxX, 0) || math.IsInf(r.MaxY, 0) {
		return false
	}
	return r.MinX < r.MaxX && r.MinY < r.MaxY
}

func tileSpan(minValue, maxValue float64, tileSize, extent int) (int, int, bool) {
	lo := math.Max(minValue, 0)
	hi := math.Min(maxValue, float64(extent))
	if !(lo < hi) {
		return 0, 0, false
	}

	first := int(math.Floor(lo / float64(tileSize)))
	last := int(math.Ceil(hi/float64(tileSize))) - 1
	tileCount := 1 + (extent-1)/tileSize
	if first < 0 {
		first = 0
	}
	if last >= tileCount {
		last = tileCount - 1
	}
	if first > last {
		return 0, 0, false
	}
	return first, last, true
}

func cloneBins(src Bins) Bins {
	dst := Bins{
		TileSize: src.TileSize,
		Cols:     src.Cols,
		Rows:     src.Rows,
		Tiles:    make(map[TileKey][]Handle, len(src.Tiles)),
		Overflow: append([]Handle(nil), src.Overflow...),
	}
	for key, bucket := range src.Tiles {
		dst.Tiles[key] = append([]Handle(nil), bucket...)
	}
	return dst
}

func checkedInt(v int64) (int, bool) {
	i := int(v)
	return i, int64(i) == v
}

func appendInt64(dst []byte, v int64) []byte {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(v))
	return append(dst, raw[:]...)
}

func appendFloat64(dst []byte, v float64) []byte {
	return appendInt64(dst, int64(math.Float64bits(v)))
}
