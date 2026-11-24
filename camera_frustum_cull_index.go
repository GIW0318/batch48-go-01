// Package task answers frustum visibility and spatial candidates queries over
// a grid bucketed item index without rebuilding it when the camera moves.
package task

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Errors reported by the cull index.
var (
	ErrBadCellSize  = errors.New("cell size must be positive")
	ErrBadID        = errors.New("item id must be positive")
	ErrDuplicateID  = errors.New("item id appears more than once")
	ErrBadBox       = errors.New("box bounds are inverted")
	ErrBadSphere    = errors.New("sphere radius must not be negative")
	ErrTooManyItems = errors.New("item count exceeds the configured limit")
	ErrBadBounds    = errors.New("orthographic bounds must have a positive span")
	ErrBadMatrix    = errors.New("camera matrix is degenerate")
	ErrNoCamera     = errors.New("no camera matrix has been installed")
	ErrResultLimit  = errors.New("result count exceeds the configured limit")
)

// Vec3 is a point or direction in world space.
type Vec3 struct {
	X, Y, Z float64
}

// Box is an axis aligned bounding box.
type Box struct {
	Min Vec3
	Max Vec3
}

// Sphere is a bounding sphere.
type Sphere struct {
	Center Vec3
	Radius float64
}

// Mat4 is a row major 4x4 matrix applied to column vectors.
type Mat4 [16]float64

// Plane is a normalized frustum plane; points with N dot p plus D >= 0 are inside.
type Plane struct {
	N Vec3
	D float64
}

// Item is one cullable object with its bounds and layer bits.
type Item struct {
	ID     int
	Box    Box
	Sphere Sphere
	Layer  uint32
}

// CullStats summarises index and query counters.
type CullStats struct {
	Items      int
	Cells      int
	Queries    int
	Visible    int
	Culled     int
	Generation uint64
}

type cellKey struct {
	x, y, z int64
}

// FrustumCullIndex is a grid bucketed index queried by a frustum.
type FrustumCullIndex struct {
	mu         sync.RWMutex
	cellSize   float64
	buildSize  float64
	maxItems   int
	maxResults int
	generation uint64

	items map[int]Item
	cells map[cellKey][]int

	hasCamera bool
	camera    Mat4
	invCamera Mat4
	planes    []Plane

	queries int
	visible int
	culled  int
}

// NewFrustumCullIndex builds an index with the given grid cell size.
func NewFrustumCullIndex(cellSize float64) *FrustumCullIndex {
	return &FrustumCullIndex{
		cellSize:   cellSize,
		maxItems:   1 << 16,
		maxResults: 4096,
		items:      make(map[int]Item),
		cells:      make(map[cellKey][]int),
	}
}

// SetCellSize changes the grid cell size before the next build.
func (x *FrustumCullIndex) SetCellSize(size float64) error {
	if !validCellSize(size) {
		return ErrBadCellSize
	}
	x.mu.Lock()
	x.cellSize = size
	x.mu.Unlock()
	return nil
}

// SetMaxResults changes the hard cap of returned results.
func (x *FrustumCullIndex) SetMaxResults(n int) error {
	if n <= 0 {
		return ErrResultLimit
	}
	x.mu.Lock()
	x.maxResults = n
	x.mu.Unlock()
	return nil
}

// SetMaxItems changes the hard cap of indexed items.
func (x *FrustumCullIndex) SetMaxItems(n int) error {
	if n <= 0 {
		return ErrTooManyItems
	}
	x.mu.Lock()
	x.maxItems = n
	x.mu.Unlock()
	return nil
}

// Build validates the items and buckets them into grid cells.
func (x *FrustumCullIndex) Build(items []Item) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	size := x.cellSize
	if !validCellSize(size) {
		return ErrBadCellSize
	}
	if len(items) > x.maxItems {
		return ErrTooManyItems
	}

	newItems := make(map[int]Item, len(items))
	newCells := make(map[cellKey][]int)
	for _, item := range items {
		if item.ID <= 0 {
			return ErrBadID
		}
		if _, exists := newItems[item.ID]; exists {
			return ErrDuplicateID
		}
		if !validBox(item.Box) {
			return ErrBadBox
		}
		if !finite(item.Sphere.Radius) || item.Sphere.Radius < 0 {
			return ErrBadSphere
		}
		if !finiteVec(item.Sphere.Center) {
			return ErrBadSphere
		}

		bucketBox := item.Box
		if zeroBox(item.Box) && !zeroSphere(item.Sphere) {
			bucketBox = sphereBounds(item.Sphere)
		}
		minCell, maxCell, err := cellRangeForBox(bucketBox, size)
		if err != nil {
			return err
		}
		newItems[item.ID] = item
		forEachCell(minCell, maxCell, func(key cellKey) {
			newCells[key] = append(newCells[key], item.ID)
		})
	}

	x.items = newItems
	x.cells = newCells
	x.buildSize = size
	x.queries = 0
	x.visible = 0
	x.culled = 0
	return nil
}

// SetCamera installs the view projection matrix used by Query.
func (x *FrustumCullIndex) SetCamera(m Mat4) error {
	if !finiteMat4(m) {
		return ErrBadMatrix
	}
	planes, ok := planesFromMatrix(m)
	if !ok {
		return ErrBadMatrix
	}
	inv, ok := invertMat4(m)
	if !ok {
		return ErrBadMatrix
	}

	x.mu.Lock()
	x.camera = m
	x.invCamera = inv
	x.planes = planes
	x.hasCamera = true
	x.generation++
	x.mu.Unlock()
	return nil
}

// Query returns the visible item ids for the layer mask in ascending order.
func (x *FrustumCullIndex) Query(layerMask uint32) ([]int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.hasCamera {
		return nil, ErrNoCamera
	}

	out := make([]int, 0)
	culled := 0
	limitHit := false

	visit := func(id int) bool {
		item := x.items[id]
		if item.Layer&layerMask == 0 || !itemVisible(item, x.planes) {
			culled++
			return true
		}
		out = append(out, id)
		if len(out) > x.maxResults {
			limitHit = true
			return false
		}
		return true
	}

	x.queries++
	bounds, ok := x.frustumBoundsLocked()
	if ok {
		size := x.buildSize
		if size <= 0 {
			size = x.cellSize
		}
		minCell, maxCell, err := cellRangeForBox(bounds, size)
		if err != nil {
			return nil, err
		}
		x.forEachIDInCellRangeLocked(minCell, maxCell, visit)
	} else {
		for id := range x.items {
			if !visit(id) {
				break
			}
		}
	}

	if limitHit {
		return nil, ErrResultLimit
	}
	sort.Ints(out)
	x.visible += len(out)
	x.culled += culled
	return out, nil
}

// Candidates returns the item ids of the cells overlapping the box.
func (x *FrustumCullIndex) Candidates(b Box) ([]int, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()

	size := x.buildSize
	if size <= 0 {
		size = x.cellSize
	}
	minCell, maxCell, err := cellRangeForBox(b, size)
	if err != nil {
		return nil, err
	}

	out := make([]int, 0)
	limitHit := false
	x.forEachIDInCellRangeLocked(minCell, maxCell, func(id int) bool {
		out = append(out, id)
		if len(out) > x.maxResults {
			limitHit = true
			return false
		}
		return true
	})
	if limitHit {
		return nil, ErrResultLimit
	}
	sort.Ints(out)
	return out, nil
}

// OrthoMatrix builds a row major orthographic projection matrix.
func (x *FrustumCullIndex) OrthoMatrix(l, r, btm, top, near, far float64) (Mat4, error) {
	if !finite(l) || !finite(r) || !finite(btm) || !finite(top) ||
		!finite(near) || !finite(far) ||
		r <= l || top <= btm || far <= near {
		return Mat4{}, ErrBadBounds
	}

	var m Mat4
	m[0] = 2 / (r - l)
	m[5] = 2 / (top - btm)
	m[10] = -2 / (far - near)
	m[3] = -(r + l) / (r - l)
	m[7] = -(top + btm) / (top - btm)
	m[11] = -(far + near) / (far - near)
	m[15] = 1
	if !finiteMat4(m) {
		return Mat4{}, ErrBadBounds
	}
	return m, nil
}

// Planes extracts and normalizes the six frustum planes of the camera.
func (x *FrustumCullIndex) Planes() ([]Plane, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if !x.hasCamera {
		return nil, ErrNoCamera
	}
	out := make([]Plane, len(x.planes))
	copy(out, x.planes)
	return out, nil
}

// Generation reports how many camera matrices have been installed.
func (x *FrustumCullIndex) Generation() uint64 {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.generation
}

// ItemCount reports how many items the index holds.
func (x *FrustumCullIndex) ItemCount() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.items)
}

// CellCount reports how many grid cells are occupied.
func (x *FrustumCullIndex) CellCount() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.cells)
}

// Resolve returns the item behind an id.
func (x *FrustumCullIndex) Resolve(id int) (Item, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	item, ok := x.items[id]
	return item, ok
}

// Stats returns a snapshot of the index counters.
func (x *FrustumCullIndex) Stats() CullStats {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return CullStats{
		Items:      len(x.items),
		Cells:      len(x.cells),
		Queries:    x.queries,
		Visible:    x.visible,
		Culled:     x.culled,
		Generation: x.generation,
	}
}

func validCellSize(size float64) bool {
	return finite(size) && size > 0
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func finiteVec(v Vec3) bool {
	return finite(v.X) && finite(v.Y) && finite(v.Z)
}

func validBox(b Box) bool {
	if !finiteVec(b.Min) || !finiteVec(b.Max) {
		return false
	}
	return b.Min.X <= b.Max.X && b.Min.Y <= b.Max.Y && b.Min.Z <= b.Max.Z
}

func zeroBox(b Box) bool {
	return b.Min == (Vec3{}) && b.Max == (Vec3{})
}

func zeroSphere(s Sphere) bool {
	return s.Center == (Vec3{}) && s.Radius == 0
}

func sphereBounds(s Sphere) Box {
	r := s.Radius
	return Box{
		Min: Vec3{X: s.Center.X - r, Y: s.Center.Y - r, Z: s.Center.Z - r},
		Max: Vec3{X: s.Center.X + r, Y: s.Center.Y + r, Z: s.Center.Z + r},
	}
}

func finiteMat4(m Mat4) bool {
	for _, v := range m {
		if !finite(v) {
			return false
		}
	}
	return true
}

func floorCell(v, size float64) (int64, bool) {
	q := math.Floor(v / size)
	if math.IsNaN(q) || math.IsInf(q, 0) || q < math.MinInt64 || q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}

func cellRangeForBox(b Box, size float64) (cellKey, cellKey, error) {
	if !validCellSize(size) {
		return cellKey{}, cellKey{}, ErrBadCellSize
	}
	if !validBox(b) {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	minX, ok := floorCell(b.Min.X, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	minY, ok := floorCell(b.Min.Y, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	minZ, ok := floorCell(b.Min.Z, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	maxX, ok := floorCell(b.Max.X, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	maxY, ok := floorCell(b.Max.Y, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	maxZ, ok := floorCell(b.Max.Z, size)
	if !ok {
		return cellKey{}, cellKey{}, ErrBadBox
	}
	return cellKey{x: minX, y: minY, z: minZ}, cellKey{x: maxX, y: maxY, z: maxZ}, nil
}

func forEachCell(minCell, maxCell cellKey, fn func(cellKey)) {
	if minCell.x > maxCell.x || minCell.y > maxCell.y || minCell.z > maxCell.z {
		return
	}
	for cx := minCell.x; ; {
		for cy := minCell.y; ; {
			for cz := minCell.z; ; {
				fn(cellKey{x: cx, y: cy, z: cz})
				if cz == maxCell.z {
					break
				}
				cz++
			}
			if cy == maxCell.y {
				break
			}
			cy++
		}
		if cx == maxCell.x {
			break
		}
		cx++
	}
}

func cellRangeVolume(minCell, maxCell cellKey) float64 {
	dx := float64(maxCell.x) - float64(minCell.x) + 1
	dy := float64(maxCell.y) - float64(minCell.y) + 1
	dz := float64(maxCell.z) - float64(minCell.z) + 1
	if dx <= 0 || dy <= 0 || dz <= 0 {
		return 0
	}
	return dx * dy * dz
}

func (x *FrustumCullIndex) forEachIDInCellRangeLocked(minCell, maxCell cellKey, fn func(int) bool) {
	if minCell.x > maxCell.x || minCell.y > maxCell.y || minCell.z > maxCell.z {
		return
	}
	seen := make(map[int]struct{})
	process := func(ids []int) bool {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if !fn(id) {
				return false
			}
		}
		return true
	}

	volume := cellRangeVolume(minCell, maxCell)
	if volume <= 4096 || volume <= float64(len(x.cells))*8 {
		stopped := false
		forEachCell(minCell, maxCell, func(key cellKey) {
			if stopped {
				return
			}
			if !process(x.cells[key]) {
				stopped = true
			}
		})
		return
	}

	for key, ids := range x.cells {
		if key.x < minCell.x || key.x > maxCell.x ||
			key.y < minCell.y || key.y > maxCell.y ||
			key.z < minCell.z || key.z > maxCell.z {
			continue
		}
		if !process(ids) {
			return
		}
	}
}

func planesFromMatrix(m Mat4) ([]Plane, bool) {
	row := func(i int) [4]float64 {
		return [4]float64{m[i*4], m[i*4+1], m[i*4+2], m[i*4+3]}
	}
	add := func(a, b [4]float64) [4]float64 {
		return [4]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2], a[3] + b[3]}
	}
	sub := func(a, b [4]float64) [4]float64 {
		return [4]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2], a[3] - b[3]}
	}
	makePlane := func(v [4]float64) (Plane, bool) {
		length := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		if !finite(length) || length == 0 {
			return Plane{}, false
		}
		p := Plane{
			N: Vec3{X: v[0] / length, Y: v[1] / length, Z: v[2] / length},
			D: v[3] / length,
		}
		if !finiteVec(p.N) || !finite(p.D) {
			return Plane{}, false
		}
		return p, true
	}

	r0, r1, r2, r3 := row(0), row(1), row(2), row(3)
	raw := [6][4]float64{
		add(r3, r0),
		sub(r3, r0),
		add(r3, r1),
		sub(r3, r1),
		add(r3, r2),
		sub(r3, r2),
	}
	planes := make([]Plane, 0, 6)
	for _, v := range raw {
		p, ok := makePlane(v)
		if !ok {
			return nil, false
		}
		planes = append(planes, p)
	}
	return planes, true
}

func invertMat4(m Mat4) (Mat4, bool) {
	var a [4][8]float64
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			a[r][c] = m[r*4+c]
		}
		a[r][4+r] = 1
	}

	for col := 0; col < 4; col++ {
		pivot := col
		best := math.Abs(a[col][col])
		for r := col + 1; r < 4; r++ {
			if v := math.Abs(a[r][col]); v > best {
				best = v
				pivot = r
			}
		}
		if best == 0 || !finite(best) {
			return Mat4{}, false
		}
		if pivot != col {
			a[col], a[pivot] = a[pivot], a[col]
		}

		divisor := a[col][col]
		for c := 0; c < 8; c++ {
			a[col][c] /= divisor
		}
		for r := 0; r < 4; r++ {
			if r == col {
				continue
			}
			factor := a[r][col]
			if factor == 0 {
				continue
			}
			for c := 0; c < 8; c++ {
				a[r][c] -= factor * a[col][c]
			}
		}
	}

	var inv Mat4
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			inv[r*4+c] = a[r][4+c]
			if !finite(inv[r*4+c]) {
				return Mat4{}, false
			}
		}
	}
	return inv, true
}

func transformPoint(m Mat4, p Vec3) (Vec3, float64) {
	x := m[0]*p.X + m[1]*p.Y + m[2]*p.Z + m[3]
	y := m[4]*p.X + m[5]*p.Y + m[6]*p.Z + m[7]
	z := m[8]*p.X + m[9]*p.Y + m[10]*p.Z + m[11]
	w := m[12]*p.X + m[13]*p.Y + m[14]*p.Z + m[15]
	return Vec3{X: x, Y: y, Z: z}, w
}

func (x *FrustumCullIndex) frustumBoundsLocked() (Box, bool) {
	min := Vec3{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	max := Vec3{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, nx := range []float64{-1, 1} {
		for _, ny := range []float64{-1, 1} {
			for _, nz := range []float64{-1, 1} {
				clip, w := transformPoint(x.invCamera, Vec3{X: nx, Y: ny, Z: nz})
				if !finite(w) || math.Abs(w) < 1e-15 {
					return Box{}, false
				}
				p := Vec3{X: clip.X / w, Y: clip.Y / w, Z: clip.Z / w}
				if !finiteVec(p) {
					return Box{}, false
				}
				min.X = math.Min(min.X, p.X)
				min.Y = math.Min(min.Y, p.Y)
				min.Z = math.Min(min.Z, p.Z)
				max.X = math.Max(max.X, p.X)
				max.Y = math.Max(max.Y, p.Y)
				max.Z = math.Max(max.Z, p.Z)
			}
		}
	}

	ex := (max.X-min.X)*1e-9 + 1e-9
	ey := (max.Y-min.Y)*1e-9 + 1e-9
	ez := (max.Z-min.Z)*1e-9 + 1e-9
	return Box{
		Min: Vec3{X: min.X - ex, Y: min.Y - ey, Z: min.Z - ez},
		Max: Vec3{X: max.X + ex, Y: max.Y + ey, Z: max.Z + ez},
	}, true
}

func itemVisible(item Item, planes []Plane) bool {
	hasBox := !zeroBox(item.Box)
	hasSphere := !zeroSphere(item.Sphere)
	if !hasBox && !hasSphere {
		return pointVisible(Vec3{}, planes)
	}
	if hasBox && !boxVisible(item.Box, planes) {
		return false
	}
	if hasSphere && !sphereVisible(item.Sphere, planes) {
		return false
	}
	return true
}

func pointVisible(p Vec3, planes []Plane) bool {
	for _, plane := range planes {
		if plane.N.X*p.X+plane.N.Y*p.Y+plane.N.Z*p.Z+plane.D < -1e-9 {
			return false
		}
	}
	return true
}

func boxVisible(b Box, planes []Plane) bool {
	center := Vec3{
		X: (b.Min.X + b.Max.X) * 0.5,
		Y: (b.Min.Y + b.Max.Y) * 0.5,
		Z: (b.Min.Z + b.Max.Z) * 0.5,
	}
	extent := Vec3{
		X: (b.Max.X - b.Min.X) * 0.5,
		Y: (b.Max.Y - b.Min.Y) * 0.5,
		Z: (b.Max.Z - b.Min.Z) * 0.5,
	}
	for _, p := range planes {
		distance := p.N.X*center.X + p.N.Y*center.Y + p.N.Z*center.Z + p.D
		radius := math.Abs(p.N.X)*extent.X + math.Abs(p.N.Y)*extent.Y + math.Abs(p.N.Z)*extent.Z
		if distance+radius < -1e-9 {
			return false
		}
	}
	return true
}

func sphereVisible(s Sphere, planes []Plane) bool {
	for _, p := range planes {
		distance := p.N.X*s.Center.X + p.N.Y*s.Center.Y + p.N.Z*s.Center.Z + p.D
		if distance+s.Radius < -1e-9 {
			return false
		}
	}
	return true
}
