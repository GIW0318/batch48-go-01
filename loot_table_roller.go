package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the loot roller.
var (
	ErrInvalidTable   = errors.New("table id rejected")
	ErrEmptyTable     = errors.New("table has no entries")
	ErrInvalidEntry   = errors.New("entry definition rejected")
	ErrDuplicateEntry = errors.New("entry id repeated in a table")
	ErrUnknownTable   = errors.New("table does not exist")
	ErrInvalidRequest = errors.New("roll request rejected")
	ErrBadSnapshot    = errors.New("snapshot payload rejected")
)

// Entry is one line of a loot table.
type Entry struct {
	ID      string
	Weight  int
	Min     int
	Max     int
	HardMax int
	Group   string
	Pity    bool
}

// Item is one produced stack.
type Item struct {
	ID    string
	Count int
}

// RollRequest is one kill or one chest open.
type RollRequest struct {
	Key    string
	Player string
	Table  string
	Luck   int
}

// Drop is the result of one request.
type Drop struct {
	Key   string
	Items []Item
}

// Options tunes the roller.
type Options struct {
	LuckGroup string
	PityLimit int
}

// LootTableRoller turns tables and requests into reproducible drops.
type LootTableRoller struct {
	mu     sync.Mutex
	opts   Options
	tables map[string][]Entry
	pity   map[string]int
	order  []string
}

// NewLootTableRoller builds a roller with the given tuning.
func NewLootTableRoller(opts Options) *LootTableRoller {
	return &LootTableRoller{opts: opts, tables: make(map[string][]Entry), pity: make(map[string]int)}
}

// LoadTable validates and installs one loot table.
func (g *LootTableRoller) LoadTable(id string, entries []Entry) error {
	return errNotImplemented
}

// Roll produces the drop of one request.
func (g *LootTableRoller) Roll(req RollRequest) (Drop, error) {
	return Drop{}, errNotImplemented
}

// Batch produces the drops of a batch of requests in order.
func (g *LootTableRoller) Batch(reqs []RollRequest) ([]Drop, error) {
	return nil, errNotImplemented
}

// Pity reports the consecutive rolls without a pity entry.
func (g *LootTableRoller) Pity(player, table string) (int, error) {
	return 0, errNotImplemented
}

// ResetPity clears the pity counter of one player and table.
func (g *LootTableRoller) ResetPity(player, table string) error {
	return errNotImplemented
}

// EffectiveWeights reports the weights used after the luck bonus.
func (g *LootTableRoller) EffectiveWeights(table string, luck int) (map[string]int, error) {
	return nil, errNotImplemented
}

// Table returns a copy of the loaded entries of one table.
func (g *LootTableRoller) Table(id string) []Entry {
	return nil
}

// Tables returns the loaded table ids in load order.
func (g *LootTableRoller) Tables() []string {
	return nil
}

// Snapshot serialises tables, pity counters and options.
func (g *LootTableRoller) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the roller state from a snapshot.
func (g *LootTableRoller) Restore(data []byte) error {
	return errNotImplemented
}
