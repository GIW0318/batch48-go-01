// Package task routes material variants into render passes and keeps one
// pass table with a per pass budget.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// BlendMode is the blend state a material asks for.
type BlendMode string

// Blend modes understood by the router.
const (
	BlendOpaque     BlendMode = "opaque"
	BlendMasked     BlendMode = "masked"
	BlendAlpha      BlendMode = "alpha"
	BlendAdditive   BlendMode = "additive"
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

// MaterialPassRouter owns the route table, the pass assignments and the
// variant key cache.
type MaterialPassRouter struct {
	mu sync.Mutex
}

// NewMaterialPassRouter builds an empty router.
func NewMaterialPassRouter() *MaterialPassRouter {
	return &MaterialPassRouter{}
}

// Route stores the material and reports the passes it must be drawn in.
func (r *MaterialPassRouter) Route(mat Material) ([]PassID, error) {
	return nil, errNotImplemented
}

// VariantKey is the cache key of one material inside one pass.
func (r *MaterialPassRouter) VariantKey(mat Material, pass PassID) (string, error) {
	return "", errNotImplemented
}

// Assign puts a routed material into one pass of its route.
func (r *MaterialPassRouter) Assign(pass PassID, matID string) error {
	return errNotImplemented
}

// PassBudget sets how many materials one pass accepts.
func (r *MaterialPassRouter) PassBudget(pass PassID, limit int) error {
	return errNotImplemented
}

// Invalidate drops the cached variants of one material.
func (r *MaterialPassRouter) Invalidate(matID string) error {
	return errNotImplemented
}

// Materials lists the assigned materials of one pass in draw order.
func (r *MaterialPassRouter) Materials(pass PassID) []string {
	return nil
}

// CacheEntries is the number of cached variant keys.
func (r *MaterialPassRouter) CacheEntries() int { return 0 }

// Snapshot serialises the routes, assignments, budgets and cache keys.
func (r *MaterialPassRouter) Snapshot() ([]byte, error) { return nil, errNotImplemented }

// Restore replaces the router state with a snapshot, all or nothing.
func (r *MaterialPassRouter) Restore(data []byte) error { return errNotImplemented }

// Reset drops every route, assignment and cached variant.
func (r *MaterialPassRouter) Reset() error { return errNotImplemented }
