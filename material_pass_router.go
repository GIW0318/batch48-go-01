// Package task routes material variants into render passes and keeps one
// pass table with a per pass budget.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// BlendMode is the blend state a material asks for.
type BlendMode string

// Blend modes understood by the router.
const (
	BlendOpaque   BlendMode = "opaque"
	BlendMasked   BlendMode = "masked"
	BlendAlpha    BlendMode = "alpha"
	BlendAdditive BlendMode = "additive"
)

// PassID names one render pass.
type PassID string

// Render passes of the frame graph.
const (
	PassShadow      PassID = "shadow"
	PassOpaque      PassID = "opaque"
	PassTransparent PassID = "transparent"
)

// MaxMaterialsPerPass is the default budget of one pass.
const MaxMaterialsPerPass = 256

const (
	maxVariantCachePerPass = MaxMaterialsPerPass
	maxVariantCacheEntries = MaxMaterialsPerPass * 3
	snapshotVersion        = 1
)

// Errors reported by the router.
var (
	ErrEmptyMaterialID = errors.New("material id is required")
	ErrUnknownBlend    = errors.New("blend mode is unknown")
	ErrUnknownPass     = errors.New("pass is unknown")
	ErrUnknownMaterial = errors.New("material is not routed")
	ErrPassNotRouted   = errors.New("material is not routed to this pass")
	ErrBudgetExceeded  = errors.New("pass budget exceeded")
	ErrBadBudget       = errors.New("pass budget must be positive")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Material is the render state one draw call needs.
type Material struct {
	ID             string    `json:"id"`
	Blend          BlendMode `json:"blend"`
	DoubleSided    bool      `json:"double_sided"`
	DepthWrite     bool      `json:"depth_write"`
	CastShadow     bool      `json:"cast_shadow"`
	ReceivesShadow bool      `json:"receives_shadow"`
	SortKey        int       `json:"sort_key"`
}

type materialRoute struct {
	material Material
	passes   []PassID
}

type passAssignments struct {
	order []string
	set   map[string]struct{}
}

// MaterialPassRouter owns the route table, the pass assignments and the
// variant key cache.
type MaterialPassRouter struct {
	mu          sync.Mutex
	routes      map[string]materialRoute
	assignments map[PassID]*passAssignments
	budgets     map[PassID]int
	cache       map[string]map[PassID]string
}

// NewMaterialPassRouter builds an empty router.
func NewMaterialPassRouter() *MaterialPassRouter {
	return &MaterialPassRouter{
		routes:      make(map[string]materialRoute),
		assignments: make(map[PassID]*passAssignments),
		budgets:     make(map[PassID]int),
		cache:       make(map[string]map[PassID]string),
	}
}

var allPasses = [...]PassID{PassShadow, PassOpaque, PassTransparent}

func isKnownPass(pass PassID) bool {
	switch pass {
	case PassShadow, PassOpaque, PassTransparent:
		return true
	default:
		return false
	}
}

func mainPassFor(mat Material) (PassID, bool) {
	switch mat.Blend {
	case BlendOpaque, BlendMasked:
		if mat.DepthWrite {
			return PassOpaque, true
		}
		return PassTransparent, true
	case BlendAlpha, BlendAdditive:
		return PassTransparent, true
	default:
		return "", false
	}
}

func routeFor(mat Material) ([]PassID, bool) {
	main, ok := mainPassFor(mat)
	if !ok {
		return nil, false
	}
	passes := make([]PassID, 0, 2)
	if mat.CastShadow {
		passes = append(passes, PassShadow)
	}
	passes = append(passes, main)
	return passes, true
}

func containsPass(passes []PassID, pass PassID) bool {
	for _, p := range passes {
		if p == pass {
			return true
		}
	}
	return false
}

func samePasses(a, b []PassID) bool {
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

func boolBit(v bool) int {
	if v {
		return 1
	}
	return 0
}

func variantKeyFor(mat Material, pass PassID) string {
	return fmt.Sprintf("%s|%s|ds=%d|dw=%d|cs=%d",
		pass, mat.Blend, boolBit(mat.DoubleSided), boolBit(mat.DepthWrite), boolBit(mat.CastShadow))
}

func (r *MaterialPassRouter) ensureLocked() {
	if r.routes == nil {
		r.routes = make(map[string]materialRoute)
	}
	if r.assignments == nil {
		r.assignments = make(map[PassID]*passAssignments)
	}
	if r.budgets == nil {
		r.budgets = make(map[PassID]int)
	}
	if r.cache == nil {
		r.cache = make(map[string]map[PassID]string)
	}
}

func (r *MaterialPassRouter) budgetLocked(pass PassID) int {
	if limit, ok := r.budgets[pass]; ok {
		return limit
	}
	return MaxMaterialsPerPass
}

func (r *MaterialPassRouter) cacheCountLocked() int {
	total := 0
	for _, variants := range r.cache {
		total += len(variants)
	}
	return total
}

func (r *MaterialPassRouter) cacheCountForPassLocked(pass PassID) int {
	total := 0
	for _, variants := range r.cache {
		if _, ok := variants[pass]; ok {
			total++
		}
	}
	return total
}

func (r *MaterialPassRouter) pruneAssignmentsLocked(matID string, passes []PassID) {
	for _, pass := range allPasses {
		if containsPass(passes, pass) {
			continue
		}
		assigned, ok := r.assignments[pass]
		if !ok {
			continue
		}
		if _, ok := assigned.set[matID]; !ok {
			continue
		}
		delete(assigned.set, matID)
		for i, id := range assigned.order {
			if id == matID {
				assigned.order = append(assigned.order[:i], assigned.order[i+1:]...)
				break
			}
		}
	}
}

// Route stores the material and reports the passes it must be drawn in.
func (r *MaterialPassRouter) Route(mat Material) ([]PassID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if mat.ID == "" {
		return nil, ErrEmptyMaterialID
	}
	passes, ok := routeFor(mat)
	if !ok {
		return nil, ErrUnknownBlend
	}

	r.ensureLocked()
	if old, exists := r.routes[mat.ID]; exists {
		if old.material != mat || !samePasses(old.passes, passes) {
			delete(r.cache, mat.ID)
			r.pruneAssignmentsLocked(mat.ID, passes)
			old.material = mat
			old.passes = append([]PassID(nil), passes...)
			r.routes[mat.ID] = old
		}
	} else {
		r.routes[mat.ID] = materialRoute{
			material: mat,
			passes:   append([]PassID(nil), passes...),
		}
	}

	return append([]PassID(nil), passes...), nil
}

// VariantKey is the cache key of one material inside one pass.
func (r *MaterialPassRouter) VariantKey(mat Material, pass PassID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !isKnownPass(pass) {
		return "", ErrUnknownPass
	}
	r.ensureLocked()

	route, ok := r.routes[mat.ID]
	if !ok {
		return "", ErrUnknownMaterial
	}
	if !containsPass(route.passes, pass) {
		return "", ErrPassNotRouted
	}

	if variants, ok := r.cache[mat.ID]; ok {
		if key, ok := variants[pass]; ok {
			return key, nil
		}
	}

	if r.cacheCountLocked() >= maxVariantCacheEntries ||
		r.cacheCountForPassLocked(pass) >= maxVariantCachePerPass {
		return "", ErrBudgetExceeded
	}

	key := variantKeyFor(route.material, pass)
	if r.cache[mat.ID] == nil {
		r.cache[mat.ID] = make(map[PassID]string)
	}
	r.cache[mat.ID][pass] = key
	return key, nil
}

// Assign puts a routed material into one pass of its route.
func (r *MaterialPassRouter) Assign(pass PassID, matID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !isKnownPass(pass) {
		return ErrUnknownPass
	}
	r.ensureLocked()

	route, ok := r.routes[matID]
	if !ok {
		return ErrUnknownMaterial
	}
	if !containsPass(route.passes, pass) {
		return ErrPassNotRouted
	}

	assigned := r.assignments[pass]
	if assigned == nil {
		assigned = &passAssignments{set: make(map[string]struct{})}
		r.assignments[pass] = assigned
	}
	if _, ok := assigned.set[matID]; ok {
		return nil
	}
	if len(assigned.order) >= r.budgetLocked(pass) {
		return ErrBudgetExceeded
	}

	assigned.order = append(assigned.order, matID)
	assigned.set[matID] = struct{}{}
	return nil
}

// PassBudget sets how many materials one pass accepts.
func (r *MaterialPassRouter) PassBudget(pass PassID, limit int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !isKnownPass(pass) {
		return ErrUnknownPass
	}
	if limit <= 0 {
		return ErrBadBudget
	}
	r.ensureLocked()
	r.budgets[pass] = limit
	return nil
}

// Invalidate drops the cached variants of one material.
func (r *MaterialPassRouter) Invalidate(matID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ensureLocked()
	if _, ok := r.routes[matID]; !ok {
		return ErrUnknownMaterial
	}
	delete(r.cache, matID)
	return nil
}

// Materials lists the assigned materials of one pass in draw order.
func (r *MaterialPassRouter) Materials(pass PassID) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !isKnownPass(pass) {
		return nil
	}
	r.ensureLocked()

	assigned := r.assignments[pass]
	if assigned == nil || len(assigned.order) == 0 {
		return []string{}
	}

	result := append([]string(nil), assigned.order...)
	sort.Slice(result, func(i, j int) bool {
		left := r.routes[result[i]].material
		right := r.routes[result[j]].material
		if left.SortKey != right.SortKey {
			return left.SortKey < right.SortKey
		}
		return left.ID < right.ID
	})
	return result
}

// CacheEntries is the number of cached variant keys.
func (r *MaterialPassRouter) CacheEntries() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cacheCountLocked()
}

type snapshotRoute struct {
	Material Material `json:"material"`
	Passes   []PassID `json:"passes"`
}

type snapshotPass struct {
	Pass      PassID   `json:"pass"`
	Materials []string `json:"materials"`
}

type snapshotBudget struct {
	Pass  PassID `json:"pass"`
	Limit int    `json:"limit"`
}

type snapshotCacheEntry struct {
	Material string `json:"material"`
	Pass     PassID `json:"pass"`
	Key      string `json:"key"`
}

type routerSnapshot struct {
	Version     int                  `json:"version"`
	Routes      []snapshotRoute      `json:"routes"`
	Assignments []snapshotPass       `json:"assignments"`
	Budgets     []snapshotBudget     `json:"budgets"`
	Cache       []snapshotCacheEntry `json:"cache"`
}

// Snapshot serialises the routes, assignments, budgets and cache keys.
func (r *MaterialPassRouter) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLocked()

	snapshot := routerSnapshot{Version: snapshotVersion}

	routeIDs := make([]string, 0, len(r.routes))
	for id := range r.routes {
		routeIDs = append(routeIDs, id)
	}
	sort.Strings(routeIDs)
	for _, id := range routeIDs {
		route := r.routes[id]
		snapshot.Routes = append(snapshot.Routes, snapshotRoute{
			Material: route.material,
			Passes:   append([]PassID(nil), route.passes...),
		})
	}

	for _, pass := range allPasses {
		assigned := r.assignments[pass]
		if assigned == nil || len(assigned.order) == 0 {
			continue
		}
		snapshot.Assignments = append(snapshot.Assignments, snapshotPass{
			Pass:      pass,
			Materials: append([]string(nil), assigned.order...),
		})
	}

	for _, pass := range allPasses {
		if limit, ok := r.budgets[pass]; ok {
			snapshot.Budgets = append(snapshot.Budgets, snapshotBudget{Pass: pass, Limit: limit})
		}
	}

	cacheMaterials := make([]string, 0, len(r.cache))
	for id := range r.cache {
		cacheMaterials = append(cacheMaterials, id)
	}
	sort.Strings(cacheMaterials)
	for _, id := range cacheMaterials {
		for _, pass := range allPasses {
			if key, ok := r.cache[id][pass]; ok {
				snapshot.Cache = append(snapshot.Cache, snapshotCacheEntry{
					Material: id,
					Pass:     pass,
					Key:      key,
				})
			}
		}
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the router state with a snapshot, all or nothing.
func (r *MaterialPassRouter) Restore(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var snapshot routerSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	routes := make(map[string]materialRoute)
	for _, saved := range snapshot.Routes {
		id := saved.Material.ID
		if id == "" {
			return ErrBadSnapshot
		}
		if _, exists := routes[id]; exists {
			return ErrBadSnapshot
		}
		passes, ok := routeFor(saved.Material)
		if !ok || !samePasses(passes, saved.Passes) {
			return ErrBadSnapshot
		}
		routes[id] = materialRoute{
			material: saved.Material,
			passes:   append([]PassID(nil), saved.Passes...),
		}
	}

	assignments := make(map[PassID]*passAssignments)
	seenPasses := make(map[PassID]struct{})
	for _, saved := range snapshot.Assignments {
		if !isKnownPass(saved.Pass) {
			return ErrBadSnapshot
		}
		if _, exists := seenPasses[saved.Pass]; exists {
			return ErrBadSnapshot
		}
		seenPasses[saved.Pass] = struct{}{}

		assigned := &passAssignments{set: make(map[string]struct{})}
		for _, matID := range saved.Materials {
			if _, exists := assigned.set[matID]; exists {
				return ErrBadSnapshot
			}
			route, ok := routes[matID]
			if !ok || !containsPass(route.passes, saved.Pass) {
				return ErrBadSnapshot
			}
			assigned.order = append(assigned.order, matID)
			assigned.set[matID] = struct{}{}
		}
		assignments[saved.Pass] = assigned
	}

	budgets := make(map[PassID]int)
	seenBudgets := make(map[PassID]struct{})
	for _, saved := range snapshot.Budgets {
		if !isKnownPass(saved.Pass) || saved.Limit <= 0 {
			return ErrBadSnapshot
		}
		if _, exists := seenBudgets[saved.Pass]; exists {
			return ErrBadSnapshot
		}
		seenBudgets[saved.Pass] = struct{}{}
		budgets[saved.Pass] = saved.Limit
	}

	cache := make(map[string]map[PassID]string)
	perPassCache := make(map[PassID]int)
	cacheEntries := 0
	for _, saved := range snapshot.Cache {
		if !isKnownPass(saved.Pass) {
			return ErrBadSnapshot
		}
		route, ok := routes[saved.Material]
		if !ok || !containsPass(route.passes, saved.Pass) {
			return ErrBadSnapshot
		}
		if saved.Key != variantKeyFor(route.material, saved.Pass) {
			return ErrBadSnapshot
		}
		if cache[saved.Material] == nil {
			cache[saved.Material] = make(map[PassID]string)
		}
		if _, exists := cache[saved.Material][saved.Pass]; exists {
			return ErrBadSnapshot
		}
		cache[saved.Material][saved.Pass] = saved.Key
		perPassCache[saved.Pass]++
		cacheEntries++
		if cacheEntries > maxVariantCacheEntries ||
			perPassCache[saved.Pass] > maxVariantCachePerPass {
			return ErrBadSnapshot
		}
	}

	r.routes = routes
	r.assignments = assignments
	r.budgets = budgets
	r.cache = cache
	return nil
}

// Reset drops every route, assignment and cached variant.
func (r *MaterialPassRouter) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.routes = make(map[string]materialRoute)
	r.assignments = make(map[PassID]*passAssignments)
	r.cache = make(map[string]map[PassID]string)
	if r.budgets == nil {
		r.budgets = make(map[PassID]int)
	}
	return nil
}
