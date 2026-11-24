// Package task loads a scene's GPU resources as an all-or-nothing
// transaction: every resource is staged, materialised once per content key and
// released in reverse creation order when the transaction is abandoned.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TxID identifies one load transaction.
type TxID = string

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// Errors reported by the loader.
var (
	ErrBlankScene        = errors.New("scene id is blank")
	ErrNoUploader        = errors.New("uploader is missing")
	ErrBadLimits         = errors.New("limits are invalid")
	ErrSceneBusy         = errors.New("scene already has an open transaction")
	ErrTooManyTx         = errors.New("too many open transactions")
	ErrUnknownTx         = errors.New("transaction is unknown")
	ErrTxClosed          = errors.New("transaction is closed")
	ErrTxFailed          = errors.New("transaction has a failed resource")
	ErrBadResource       = errors.New("resource is invalid")
	ErrKeyConflict       = errors.New("content key already staged with different content")
	ErrTxLimit           = errors.New("transaction resource limit reached")
	ErrKeyLimit          = errors.New("global content key limit reached")
	ErrEmptyTx           = errors.New("transaction has no staged resources")
	ErrAlreadyCommitted  = errors.New("transaction is already committed")
	ErrAlreadyRolledBack = errors.New("transaction is already rolled back")
	ErrStageFailed       = errors.New("a staged resource failed to upload")
	ErrBadLease          = errors.New("lease is invalid")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// TxState is the lifecycle stage of one transaction.
type TxState string

// Lifecycle states of a transaction.
const (
	TxOpen       TxState = "open"
	TxCommitted  TxState = "committed"
	TxRolledBack TxState = "rolled_back"
	TxCanceled   TxState = "canceled"
)

// Resource is one GPU asset addressed by its content key.
type Resource struct {
	Key   string
	Kind  string
	Bytes int
}

// Limits caps a loader.
type Limits struct {
	MaxTx    int
	MaxPerTx int
	MaxKeys  int
	Lease    time.Duration
}

// Uploader materialises and releases resources on the device.
type Uploader interface {
	Upload(Resource) error
	Release(Resource) error
}

// TxInfo is a read-only view of one transaction.
type TxInfo struct {
	ID      TxID
	Scene   string
	State   TxState
	Staged  int
	Created int
}

// ResourceLoadTx owns the transactions and the device-wide content key table.
type ResourceLoadTx struct {
	mu     sync.Mutex
	up     Uploader
	lim    Limits
	seq    uint64
	txs    map[TxID]*loadTx
	scenes map[string]TxID
	refs   map[string]*keyRef
}

type keyRef struct {
	res  Resource
	refs int
}

type loadTx struct {
	id         TxID
	scene      string
	state      TxState
	order      []string
	owned      map[string]Resource
	created    []string
	createdSet map[string]bool
	failed     bool
	begun      time.Time
}

// NewResourceLoadTx builds a loader on top of one uploader.
func NewResourceLoadTx(up Uploader, lim Limits) *ResourceLoadTx {
	return &ResourceLoadTx{
		up:     up,
		lim:    lim,
		txs:    make(map[TxID]*loadTx),
		scenes: make(map[string]TxID),
		refs:   make(map[string]*keyRef),
	}
}

// Begin opens a transaction for one scene.
func (g *ResourceLoadTx) Begin(sceneID string) (TxID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if strings.TrimSpace(sceneID) == "" {
		return "", ErrBlankScene
	}
	if g.up == nil {
		return "", ErrNoUploader
	}
	if err := g.validateConfigLocked(); err != nil {
		return "", err
	}
	if _, ok := g.scenes[sceneID]; ok {
		return "", ErrSceneBusy
	}
	if g.activeLocked() >= g.lim.MaxTx {
		return "", ErrTooManyTx
	}

	g.seq++
	id := TxID(fmt.Sprintf("tx-%d", g.seq))
	tx := &loadTx{
		id:         id,
		scene:      sceneID,
		state:      TxOpen,
		owned:      make(map[string]Resource),
		createdSet: make(map[string]bool),
		begun:      time.Now(),
	}
	g.txs[id] = tx
	g.scenes[sceneID] = id
	return id, nil
}

// Stage materialises one resource inside an open transaction.
func (g *ResourceLoadTx) Stage(txID string, r Resource) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return ErrUnknownTx
	}
	if tx.state != TxOpen {
		return ErrTxClosed
	}
	if tx.failed {
		return ErrTxFailed
	}
	if strings.TrimSpace(r.Key) == "" || r.Bytes <= 0 {
		return ErrBadResource
	}

	if prev, ok := tx.owned[r.Key]; ok {
		if prev != r {
			return ErrKeyConflict
		}
		return nil
	}
	if len(tx.owned) >= g.lim.MaxPerTx {
		return ErrTxLimit
	}

	if kr, ok := g.refs[r.Key]; ok {
		if kr.res != r {
			return ErrKeyConflict
		}
		kr.refs++
		tx.owned[r.Key] = r
		tx.order = append(tx.order, r.Key)
		return nil
	}

	if len(g.refs) >= g.lim.MaxKeys {
		return ErrKeyLimit
	}
	if err := g.up.Upload(r); err != nil {
		tx.failed = true
		return err
	}

	g.refs[r.Key] = &keyRef{res: r, refs: 1}
	tx.owned[r.Key] = r
	tx.order = append(tx.order, r.Key)
	tx.created = append(tx.created, r.Key)
	tx.createdSet[r.Key] = true
	return nil
}

// Commit publishes an open transaction.
func (g *ResourceLoadTx) Commit(txID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return ErrUnknownTx
	}
	switch tx.state {
	case TxCommitted:
		return ErrAlreadyCommitted
	case TxRolledBack:
		return ErrAlreadyRolledBack
	case TxCanceled:
		return ErrTxClosed
	}
	if tx.failed {
		return ErrStageFailed
	}
	if len(tx.owned) == 0 {
		return ErrEmptyTx
	}

	tx.state = TxCommitted
	g.releaseSceneLocked(tx)
	return nil
}

// Rollback releases every resource of a transaction in reverse creation order.
func (g *ResourceLoadTx) Rollback(txID string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return 0, ErrUnknownTx
	}
	if tx.state == TxRolledBack || tx.state == TxCanceled {
		return 0, ErrAlreadyRolledBack
	}

	n, err := g.releaseTxLocked(tx)
	tx.state = TxRolledBack
	g.releaseSceneLocked(tx)
	return n, err
}

// Cancel abandons an open transaction.
func (g *ResourceLoadTx) Cancel(txID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return ErrUnknownTx
	}
	switch tx.state {
	case TxCanceled:
		return nil
	case TxCommitted:
		return ErrAlreadyCommitted
	case TxRolledBack:
		return ErrAlreadyRolledBack
	}

	return g.cancelTxLocked(tx)
}

// Progress reports the committed fraction of a transaction.
func (g *ResourceLoadTx) Progress(txID string) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return -1
	}
	switch tx.state {
	case TxCommitted:
		return 1
	case TxOpen:
		return 0
	default:
		return -1
	}
}

// State reports the lifecycle state of a transaction.
func (g *ResourceLoadTx) State(txID string) (TxState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	tx, ok := g.txs[txID]
	if !ok {
		return "", ErrUnknownTx
	}
	return tx.state, nil
}

// ActiveTx reports how many transactions are still open.
func (g *ResourceLoadTx) ActiveTx() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeLocked()
}

// RefCount reports how many transactions hold one content key.
func (g *ResourceLoadTx) RefCount(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if kr, ok := g.refs[key]; ok {
		return kr.refs
	}
	return 0
}

// KeyCount reports how many distinct content keys are resident.
func (g *ResourceLoadTx) KeyCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.refs)
}

// SetLease changes the inactivity window used by Reclaim.
func (g *ResourceLoadTx) SetLease(d time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d <= 0 {
		return ErrBadLease
	}
	g.lim.Lease = d
	return nil
}

// Reclaim abandons open transactions whose lease has expired.
func (g *ResourceLoadTx) Reclaim(now time.Time) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.validateConfigLocked(); err != nil {
		return 0, err
	}

	var errs error
	reclaimed := 0
	for _, tx := range g.txs {
		if tx.state != TxOpen {
			continue
		}
		if now.Before(tx.begun.Add(g.lim.Lease)) {
			continue
		}
		if err := g.cancelTxLocked(tx); err != nil {
			errs = errors.Join(errs, err)
		}
		reclaimed++
	}
	return reclaimed, errs
}

// TxInfos lists every transaction this loader still remembers.
func (g *ResourceLoadTx) TxInfos() []TxInfo {
	g.mu.Lock()
	defer g.mu.Unlock()

	infos := make([]TxInfo, 0, len(g.txs))
	for _, tx := range g.txs {
		infos = append(infos, TxInfo{
			ID:      tx.id,
			Scene:   tx.scene,
			State:   tx.state,
			Staged:  len(tx.owned),
			Created: len(tx.created),
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		ni, iok := numericTxSeq(infos[i].ID)
		nj, jok := numericTxSeq(infos[j].ID)
		if iok && jok && ni != nj {
			return ni < nj
		}
		return infos[i].ID < infos[j].ID
	})
	return infos
}

// Keys lists the resident resources ordered by content key.
func (g *ResourceLoadTx) Keys() []Resource {
	g.mu.Lock()
	defer g.mu.Unlock()

	keys := make([]string, 0, len(g.refs))
	for key := range g.refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Resource, 0, len(keys))
	for _, key := range keys {
		out = append(out, g.refs[key].res)
	}
	return out
}

type snapshotPayload struct {
	Version int           `json:"version"`
	Seq     uint64        `json:"seq"`
	Txs     []snapshotTx  `json:"txs"`
	Keys    []snapshotKey `json:"keys"`
}

type snapshotTx struct {
	ID        TxID       `json:"id"`
	Scene     string     `json:"scene"`
	State     TxState    `json:"state"`
	Begun     int64      `json:"begun"`
	Failed    bool       `json:"failed"`
	Resources []Resource `json:"resources"`
	Created   []string   `json:"created"`
}

type snapshotKey struct {
	Resource Resource `json:"resource"`
	Refs     int      `json:"refs"`
}

// Snapshot serialises the live transactions and the content key table.
func (g *ResourceLoadTx) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ids := make([]string, 0, len(g.txs))
	for id := range g.txs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, iok := numericTxSeq(ids[i])
		nj, jok := numericTxSeq(ids[j])
		if iok && jok && ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})

	p := snapshotPayload{
		Version: snapshotVersion,
		Seq:     g.seq,
		Txs:     make([]snapshotTx, 0, len(g.txs)),
		Keys:    make([]snapshotKey, 0, len(g.refs)),
	}
	for _, id := range ids {
		tx := g.txs[id]
		resources := make([]Resource, 0, len(tx.order))
		for _, key := range tx.order {
			if r, ok := tx.owned[key]; ok {
				resources = append(resources, r)
			}
		}
		created := make([]string, 0, len(tx.created))
		created = append(created, tx.created...)
		p.Txs = append(p.Txs, snapshotTx{
			ID:        tx.id,
			Scene:     tx.scene,
			State:     tx.state,
			Begun:     tx.begun.UnixNano(),
			Failed:    tx.failed,
			Resources: resources,
			Created:   created,
		})
	}

	keys := make([]string, 0, len(g.refs))
	for key := range g.refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		kr := g.refs[key]
		p.Keys = append(p.Keys, snapshotKey{Resource: kr.res, Refs: kr.refs})
	}
	return json.Marshal(p)
}

// Restore rebuilds the loader from a snapshot, either completely or not at all.
func (g *ResourceLoadTx) Restore(payload []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.up == nil {
		return ErrNoUploader
	}
	if err := g.validateConfigLocked(); err != nil {
		return err
	}
	if len(payload) == 0 {
		return ErrBadSnapshot
	}

	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var p snapshotPayload
	if err := dec.Decode(&p); err != nil {
		return ErrBadSnapshot
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return ErrBadSnapshot
	}
	if p.Version != snapshotVersion || p.Txs == nil || p.Keys == nil {
		return ErrBadSnapshot
	}

	refs := make(map[string]*keyRef, len(p.Keys))
	for _, k := range p.Keys {
		r := k.Resource
		if strings.TrimSpace(r.Key) == "" || r.Bytes <= 0 || k.Refs <= 0 {
			return ErrBadSnapshot
		}
		if _, dup := refs[r.Key]; dup {
			return ErrBadSnapshot
		}
		refs[r.Key] = &keyRef{res: r, refs: k.Refs}
	}
	if len(refs) > g.lim.MaxKeys {
		return ErrBadSnapshot
	}

	txs := make(map[TxID]*loadTx, len(p.Txs))
	scenes := make(map[string]TxID)
	counts := make(map[string]int, len(refs))
	active := 0
	var maxSeq uint64

	for _, st := range p.Txs {
		if st.ID == "" || strings.TrimSpace(st.Scene) == "" {
			return ErrBadSnapshot
		}
		if _, dup := txs[st.ID]; dup {
			return ErrBadSnapshot
		}
		if !validTxState(st.State) || st.Resources == nil || st.Created == nil {
			return ErrBadSnapshot
		}
		if st.State == TxCommitted && st.Failed {
			return ErrBadSnapshot
		}
		if st.State == TxRolledBack || st.State == TxCanceled {
			if len(st.Resources) != 0 || len(st.Created) != 0 {
				return ErrBadSnapshot
			}
		}
		if st.State == TxCommitted && len(st.Resources) == 0 {
			return ErrBadSnapshot
		}
		if len(st.Resources) > g.lim.MaxPerTx {
			return ErrBadSnapshot
		}

		tx := &loadTx{
			id:         st.ID,
			scene:      st.Scene,
			state:      st.State,
			owned:      make(map[string]Resource, len(st.Resources)),
			createdSet: make(map[string]bool, len(st.Created)),
			failed:     st.Failed,
			begun:      time.Unix(0, st.Begun),
		}
		if st.State == TxOpen {
			active++
			if _, dup := scenes[st.Scene]; dup {
				return ErrBadSnapshot
			}
			scenes[st.Scene] = st.ID
		}

		for _, r := range st.Resources {
			if strings.TrimSpace(r.Key) == "" || r.Bytes <= 0 {
				return ErrBadSnapshot
			}
			if _, dup := tx.owned[r.Key]; dup {
				return ErrBadSnapshot
			}
			kr, ok := refs[r.Key]
			if !ok || kr.res != r {
				return ErrBadSnapshot
			}
			tx.owned[r.Key] = r
			tx.order = append(tx.order, r.Key)
			counts[r.Key]++
		}
		for _, key := range st.Created {
			if key == "" || tx.createdSet[key] {
				return ErrBadSnapshot
			}
			if _, ok := tx.owned[key]; !ok {
				return ErrBadSnapshot
			}
			tx.createdSet[key] = true
			tx.created = append(tx.created, key)
		}

		txs[tx.id] = tx
		if n, ok := numericTxSeq(tx.id); ok && n > maxSeq {
			maxSeq = n
		}
	}

	if active > g.lim.MaxTx {
		return ErrBadSnapshot
	}
	for key, kr := range refs {
		if counts[key] != kr.refs {
			return ErrBadSnapshot
		}
	}
	for key := range counts {
		if _, ok := refs[key]; !ok {
			return ErrBadSnapshot
		}
	}

	seq := p.Seq
	if seq < maxSeq {
		seq = maxSeq
	}
	g.txs = txs
	g.scenes = scenes
	g.refs = refs
	g.seq = seq
	return nil
}

func (g *ResourceLoadTx) validateConfigLocked() error {
	if g.lim.MaxTx <= 0 || g.lim.MaxPerTx <= 0 || g.lim.MaxKeys <= 0 {
		return ErrBadLimits
	}
	if g.lim.Lease <= 0 {
		return ErrBadLease
	}
	return nil
}

func (g *ResourceLoadTx) activeLocked() int {
	active := 0
	for _, tx := range g.txs {
		if tx.state == TxOpen {
			active++
		}
	}
	return active
}

func (g *ResourceLoadTx) releaseSceneLocked(tx *loadTx) {
	if id, ok := g.scenes[tx.scene]; ok && id == tx.id {
		delete(g.scenes, tx.scene)
	}
}

func (g *ResourceLoadTx) cancelTxLocked(tx *loadTx) error {
	_, err := g.releaseTxLocked(tx)
	tx.state = TxCanceled
	g.releaseSceneLocked(tx)
	return err
}

func (g *ResourceLoadTx) releaseTxLocked(tx *loadTx) (int, error) {
	var errs error
	released := 0
	for i := len(tx.order) - 1; i >= 0; i-- {
		key := tx.order[i]
		if _, ok := tx.owned[key]; !ok {
			continue
		}
		released++

		kr, ok := g.refs[key]
		if !ok {
			continue
		}
		if kr.refs <= 1 {
			delete(g.refs, key)
			if g.up != nil {
				if err := g.up.Release(kr.res); err != nil {
					errs = errors.Join(errs, err)
				}
			}
			continue
		}
		kr.refs--
	}

	tx.order = nil
	tx.owned = make(map[string]Resource)
	tx.created = nil
	tx.createdSet = make(map[string]bool)
	tx.failed = false
	return released, errs
}

func validTxState(state TxState) bool {
	switch state {
	case TxOpen, TxCommitted, TxRolledBack, TxCanceled:
		return true
	default:
		return false
	}
}

func numericTxSeq(id string) (uint64, bool) {
	if !strings.HasPrefix(id, "tx-") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(id, "tx-"), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
