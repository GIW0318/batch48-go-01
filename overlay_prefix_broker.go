// Package task keeps the inner prefix registrations of tenant overlay
// tunnels, rejects unauthorised overlaps and answers data plane lookups with
// the longest matching prefix.
package task

import (
	"errors"
	"net/netip"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the broker.
var (
	ErrBlankTenant        = errors.New("blank tenant")
	ErrInvalidPrefix      = errors.New("invalid prefix")
	ErrNotCanonical       = errors.New("prefix has host bits set")
	ErrDuplicatePrefix    = errors.New("duplicate prefix")
	ErrSameTenantOverlap  = errors.New("overlaps prefix of the same tenant")
	ErrCrossTenantOverlap = errors.New("overlaps prefix of another tenant without transit")
	ErrPrefixLimit        = errors.New("per tenant prefix limit reached")
	ErrUnknownTenant      = errors.New("unknown tenant")
	ErrUnknownPrefix      = errors.New("unknown prefix")
	ErrFiltered           = errors.New("prefix not permitted by filter")
	ErrBadFilter          = errors.New("invalid filter")
	ErrBadSnapshot        = errors.New("snapshot rejected")
)

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// Attrs carries the data plane attributes announced with a prefix.
type Attrs struct {
	Metric uint32
	Weight int
	Label  string
}

// Filter limits which prefixes a tenant may announce. Deny wins over Allow,
// and an empty Allow list means every canonical prefix is allowed.
type Filter struct {
	Allow []netip.Prefix
	Deny  []netip.Prefix
}

// Permits reports whether p may be announced under this filter.
func (f Filter) Permits(p netip.Prefix) bool {
	return false
}

// PrefixState is one registered prefix as seen from outside the broker.
type PrefixState struct {
	Tenant string
	Prefix netip.Prefix
	Attrs  Attrs
	Usable bool
}

// TransitPair is one unordered transit authorisation.
type TransitPair struct {
	A string
	B string
}

// Snapshot is the serialisable form of a broker table.
type Snapshot struct {
	Version  int
	TakenAt  time.Time
	Limit    int
	Tenants  []string
	Prefixes []PrefixState
	Filters  map[string]Filter
	Transit  []TransitPair
}

// entry is the internal per prefix bookkeeping.
type entry struct {
	attrs  Attrs
	usable bool
}

// PrefixBroker owns the inner prefix table of every tenant tunnel.
type PrefixBroker struct {
	mu      sync.RWMutex
	limit   int
	table   map[string]map[netip.Prefix]*entry
	filters map[string]Filter
	transit map[string]bool
}

// NewPrefixBroker builds a broker with the given per tenant prefix limit.
func NewPrefixBroker(limit int) *PrefixBroker {
	return &PrefixBroker{
		limit:   limit,
		table:   make(map[string]map[netip.Prefix]*entry),
		filters: make(map[string]Filter),
		transit: make(map[string]bool),
	}
}

// Register installs one inner prefix for the tenant.
func (b *PrefixBroker) Register(tenant string, prefix netip.Prefix, attrs Attrs) error {
	return errNotImplemented
}

// Withdraw removes one inner prefix of the tenant.
func (b *PrefixBroker) Withdraw(tenant string, prefix netip.Prefix) error {
	return errNotImplemented
}

// Lookup returns the tenant owning the longest prefix that covers dst.
func (b *PrefixBroker) Lookup(dst netip.Addr) (string, bool) {
	return "", false
}

// SetFilter replaces the announce filter of the tenant.
func (b *PrefixBroker) SetFilter(tenant string, f Filter) error {
	return errNotImplemented
}

// FilterOf returns the filter currently installed for the tenant.
func (b *PrefixBroker) FilterOf(tenant string) (Filter, bool) {
	return Filter{}, false
}

// SetTransit grants or revokes the transit authorisation between two tenants.
func (pb *PrefixBroker) SetTransit(a, b string, allowed bool) {
}

// TransitAllowed reports whether two tenants may announce overlapping prefixes.
func (pb *PrefixBroker) TransitAllowed(a, b string) bool {
	return false
}

// Count returns the number of prefixes registered by the tenant.
func (b *PrefixBroker) Count(tenant string) int {
	return 0
}

// Total returns the number of prefixes registered by every tenant.
func (b *PrefixBroker) Total() int {
	return 0
}

// Tenants returns the tenants that currently own at least one prefix.
func (b *PrefixBroker) Tenants() []string {
	return nil
}

// Prefixes returns the current state of every prefix of one tenant.
func (b *PrefixBroker) Prefixes(tenant string) []PrefixState {
	return nil
}

// Suspended returns the prefixes that are registered but not usable.
func (b *PrefixBroker) Suspended() []PrefixState {
	return nil
}

// SetLimit changes the per tenant prefix limit.
func (b *PrefixBroker) SetLimit(limit int) error {
	return errNotImplemented
}

// Snapshot captures the whole table for a later restart.
func (b *PrefixBroker) Snapshot() Snapshot {
	return Snapshot{}
}

// Restore rebuilds the table from a snapshot after a restart.
func (b *PrefixBroker) Restore(s Snapshot) error {
	return errNotImplemented
}
