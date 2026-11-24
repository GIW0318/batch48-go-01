package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the layer router.
var (
	ErrBlankSubscriber   = errors.New("subscriber id is blank")
	ErrSubscriberLimit   = errors.New("subscriber limit reached")
	ErrBudgetInvalid     = errors.New("budget must be positive")
	ErrBudgetExceeded    = errors.New("budget cannot cover the requested layer closure")
	ErrMixedSwitch       = errors.New("layer switch mixes upgrade and downgrade dimensions")
	ErrLayerInvalid      = errors.New("layer components must not be negative")
	ErrLayerLimit        = errors.New("active layer cap reached")
	ErrUnknownSubscriber = errors.New("subscriber is not attached")
	ErrRouterSnapshot    = errors.New("layer router snapshot is not readable")
)

// Defaults applied when a router config field is left at zero.
const (
	DefaultMaxSubscribers = 64
	DefaultMaxLayers      = 128
	LayerUnitCost         = 100
)

// LayerID names one temporal, spatial and quality layer combination.
// Every component starts at zero, which is the lowest layer of that dimension.
type LayerID struct {
	Temporal int
	Spatial  int
	Quality  int
}

// RouterConfig bounds the number of subscribers and forwarded layers.
type RouterConfig struct {
	MaxSubscribers int
	MaxLayers      int
}

// RouterStats reports the counters kept by the router.
type RouterStats struct {
	Subscribers   int
	Layers        int
	Upgrades      uint64
	Downgrades    uint64
	Keyframes     uint64
	BudgetRejects uint64
	Detached      uint64
	Restored      uint64
}

// LayerRouter selects the SVC layers forwarded to each subscriber.
type LayerRouter struct {
	mu    sync.Mutex
	cfg   RouterConfig
	stats RouterStats
}

// NewLayerRouter builds a router with the given caps.
func NewLayerRouter(cfg RouterConfig) *LayerRouter {
	return &LayerRouter{cfg: withRouterDefaults(cfg)}
}

func withRouterDefaults(cfg RouterConfig) RouterConfig {
	if cfg.MaxSubscribers <= 0 {
		cfg.MaxSubscribers = DefaultMaxSubscribers
	}
	if cfg.MaxLayers <= 0 {
		cfg.MaxLayers = DefaultMaxLayers
	}
	return cfg
}

// Subscribe attaches a subscriber with a bandwidth budget in cost units.
func (r *LayerRouter) Subscribe(id string, budget int) error {
	return errNotImplemented
}

// SelectLayers records the layer a subscriber wants and returns the layer
// currently forwarded to it.
func (r *LayerRouter) SelectLayers(id string, want LayerID) (LayerID, error) {
	return LayerID{}, errNotImplemented
}

// OnKeyframe applies the pending upgrade of one subscriber at the given
// keyframe time and returns the layer now forwarded.
func (r *LayerRouter) OnKeyframe(id string, at uint64) (LayerID, error) {
	return LayerID{}, errNotImplemented
}

// SetBudget replaces the bandwidth budget of one subscriber and returns the
// layer that is forwarded after the change.
func (r *LayerRouter) SetBudget(id string, budget int) (LayerID, error) {
	return LayerID{}, errNotImplemented
}

// Detach removes a subscriber and releases the layers nobody references.
func (r *LayerRouter) Detach(id string) error {
	return errNotImplemented
}

// Forwarded reports the layer currently sent to a subscriber.
func (r *LayerRouter) Forwarded(id string) LayerID {
	return LayerID{}
}

// Pending reports the upgrade target of a subscriber, if one is waiting for
// a keyframe.
func (r *LayerRouter) Pending(id string) (LayerID, bool) {
	return LayerID{}, false
}

// Coverage lists the dependency closure of a layer in dependency order.
func (r *LayerRouter) Coverage(want LayerID) []LayerID {
	return nil
}

// DropPlan lists the layers removed when moving from one layer to another,
// dependents before their dependencies.
func (r *LayerRouter) DropPlan(from, to LayerID) []LayerID {
	return nil
}

// Cost reports the bandwidth cost of a layer closure.
func (r *LayerRouter) Cost(want LayerID) int {
	return 0
}

// ActiveLayers lists every layer currently referenced by a subscriber.
func (r *LayerRouter) ActiveLayers() []LayerID {
	return nil
}

// Snapshot serialises the router state.
func (r *LayerRouter) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the router state from a snapshot.
func (r *LayerRouter) Restore(data []byte) error {
	return errNotImplemented
}

// Stats reports the counters kept by the router.
func (r *LayerRouter) Stats() RouterStats {
	return RouterStats{}
}
