package task

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"math/rand"
	"sync"
)

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
	if id == "" {
		return ErrInvalidTable
	}
	if len(entries) == 0 {
		return ErrEmptyTable
	}

	installed := make([]Entry, len(entries))
	seen := make(map[string]struct{}, len(entries))
	totalWeight := 0
	for i, entry := range entries {
		if entry.ID == "" || entry.Weight < 0 || entry.Min < 1 || entry.Max < entry.Min {
			return ErrInvalidEntry
		}
		if _, ok := seen[entry.ID]; ok {
			return ErrDuplicateEntry
		}
		seen[entry.ID] = struct{}{}
		installed[i] = entry
		totalWeight += entry.Weight
	}
	if totalWeight == 0 {
		return ErrInvalidEntry
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.tables[id]; ok {
		return ErrInvalidTable
	}
	g.tables[id] = installed
	g.order = append(g.order, id)
	return nil
}

// Roll produces the drop of one request.
func (g *LootTableRoller) Roll(req RollRequest) (Drop, error) {
	if req.Key == "" || req.Player == "" {
		return Drop{}, ErrInvalidRequest
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	entries, ok := g.tables[req.Table]
	if !ok {
		return Drop{}, ErrUnknownTable
	}
	return g.rollLocked(req, entries), nil
}

// Batch produces the drops of a batch of requests in order.
func (g *LootTableRoller) Batch(reqs []RollRequest) ([]Drop, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, req := range reqs {
		if req.Key == "" || req.Player == "" {
			return nil, ErrInvalidRequest
		}
		if _, ok := g.tables[req.Table]; !ok {
			return nil, ErrUnknownTable
		}
	}

	drops := make([]Drop, len(reqs))
	produced := make(map[string]Drop, len(reqs))
	for i, req := range reqs {
		if drop, ok := produced[req.Key]; ok {
			drops[i] = drop
			continue
		}
		drop := g.rollLocked(req, g.tables[req.Table])
		produced[req.Key] = drop
		drops[i] = drop
	}
	return drops, nil
}

// Pity reports the consecutive rolls without a pity entry.
func (g *LootTableRoller) Pity(player, table string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.tables[table]; !ok {
		return 0, ErrUnknownTable
	}
	return g.pity[pityKey(player, table)], nil
}

// ResetPity clears the pity counter of one player and table.
func (g *LootTableRoller) ResetPity(player, table string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.tables[table]; !ok {
		return ErrUnknownTable
	}
	delete(g.pity, pityKey(player, table))
	return nil
}

// EffectiveWeights reports the weights used after the luck bonus.
func (g *LootTableRoller) EffectiveWeights(table string, luck int) (map[string]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entries, ok := g.tables[table]
	if !ok {
		return nil, ErrUnknownTable
	}

	bonus := luck
	if bonus < 0 {
		bonus = 0
	}
	weights := make(map[string]int, len(entries))
	for _, entry := range entries {
		weights[entry.ID] = g.effectiveWeight(entry, bonus)
	}
	return weights, nil
}

// Table returns a copy of the loaded entries of one table.
func (g *LootTableRoller) Table(id string) []Entry {
	g.mu.Lock()
	defer g.mu.Unlock()
	entries, ok := g.tables[id]
	if !ok {
		return nil
	}
	return append([]Entry(nil), entries...)
}

// Tables returns the loaded table ids in load order.
func (g *LootTableRoller) Tables() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.order...)
}

type snapshotData struct {
	Options Options            `json:"options"`
	Order   []string           `json:"order"`
	Tables  map[string][]Entry `json:"tables"`
	Pity    map[string]int     `json:"pity"`
}

// Snapshot serialises tables, pity counters and options.
func (g *LootTableRoller) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	tables := make(map[string][]Entry, len(g.tables))
	for id, entries := range g.tables {
		tables[id] = append([]Entry(nil), entries...)
	}
	pity := make(map[string]int, len(g.pity))
	for key, count := range g.pity {
		pity[key] = count
	}
	data := snapshotData{
		Options: g.opts,
		Order:   append([]string(nil), g.order...),
		Tables:  tables,
		Pity:    pity,
	}
	return json.Marshal(&data)
}

// Restore replaces the roller state from a snapshot.
func (g *LootTableRoller) Restore(data []byte) error {
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.Tables == nil || len(snap.Tables) == 0 {
		return ErrBadSnapshot
	}

	tables := make(map[string][]Entry, len(snap.Tables))
	order := make([]string, 0, len(snap.Order))
	seen := make(map[string]struct{}, len(snap.Tables))
	for _, id := range snap.Order {
		entries, ok := snap.Tables[id]
		if !ok || id == "" || len(entries) == 0 {
			return ErrBadSnapshot
		}
		entryIDs := make(map[string]struct{}, len(entries))
		totalWeight := 0
		for _, entry := range entries {
			if entry.ID == "" || entry.Weight < 0 || entry.Min < 1 || entry.Max < entry.Min {
				return ErrBadSnapshot
			}
			if _, dup := entryIDs[entry.ID]; dup {
				return ErrBadSnapshot
			}
			entryIDs[entry.ID] = struct{}{}
			totalWeight += entry.Weight
		}
		if totalWeight == 0 {
			return ErrBadSnapshot
		}
		tables[id] = append([]Entry(nil), entries...)
		order = append(order, id)
		seen[id] = struct{}{}
	}
	for id := range snap.Tables {
		if _, ok := seen[id]; !ok {
			return ErrBadSnapshot
		}
	}

	pity := make(map[string]int, len(snap.Pity))
	for key, count := range snap.Pity {
		if count < 0 {
			return ErrBadSnapshot
		}
		pity[key] = count
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.opts = snap.Options
	g.tables = tables
	g.order = order
	g.pity = pity
	return nil
}

// pityKey is the composite key of one player/table counter.
func pityKey(player, table string) string {
	return player + "\x00" + table
}

// keyRand derives an independent, integer-only random stream from the request key.
func keyRand(key string) *rand.Rand {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(key))
	return rand.New(rand.NewSource(int64(hash.Sum64())))
}

// effectiveWeight applies the luck bonus to one entry. Luck is a percentage
// bonus (100 doubles the base weight) and only touches the configured group.
// Negative luck counts as zero, and the integer formula never accumulates
// floating-point drift.
func (g *LootTableRoller) effectiveWeight(entry Entry, luck int) int {
	if g.opts.LuckGroup != "" && entry.Group == g.opts.LuckGroup && luck > 0 {
		return entry.Weight + entry.Weight*luck/100
	}
	return entry.Weight
}

// rollLocked performs one draw. Callers must hold g.mu.
func (g *LootTableRoller) rollLocked(req RollRequest, entries []Entry) Drop {
	rng := keyRand(req.Key)
	counterKey := pityKey(req.Player, req.Table)

	pityIndex := -1
	for i, entry := range entries {
		if entry.Pity {
			pityIndex = i
			break
		}
	}

	chosenIndex := -1
	if pityIndex >= 0 && g.opts.PityLimit > 0 && g.pity[counterKey] >= g.opts.PityLimit {
		chosenIndex = pityIndex
	} else {
		totalWeight := 0
		for _, entry := range entries {
			totalWeight += g.effectiveWeight(entry, req.Luck)
		}
		ticket := rng.Intn(totalWeight)
		cumulative := 0
		for i, entry := range entries {
			cumulative += g.effectiveWeight(entry, req.Luck)
			if ticket < cumulative {
				chosenIndex = i
				break
			}
		}
	}

	entry := entries[chosenIndex]

	if pityIndex >= 0 && chosenIndex == pityIndex {
		g.pity[counterKey] = 0
	} else {
		g.pity[counterKey]++
	}

	span := entry.Max - entry.Min
	count := entry.Min
	if span > 0 {
		count += rng.Intn(span + 1)
	}
	if entry.HardMax > 0 && count > entry.HardMax {
		count = entry.HardMax
	}

	return Drop{
		Key:   req.Key,
		Items: []Item{{ID: entry.ID, Count: count}},
	}
}
