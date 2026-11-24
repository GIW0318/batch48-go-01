// Package task tracks the connection IDs a QUIC server hands out per
// connection together with the DCID routing table that maps every issued or
// retiring CID back to its connection.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned by the ledger.
var (
	ErrBlankConn          = errors.New("blank connection id")
	ErrConnExists         = errors.New("connection already exists")
	ErrConnNotFound       = errors.New("connection not found")
	ErrConnReleased       = errors.New("connection released")
	ErrBadCount           = errors.New("invalid cid count")
	ErrZeroLengthCID      = errors.New("zero length cid connection")
	ErrActiveLimit        = errors.New("active cid limit reached")
	ErrCIDInUse           = errors.New("cid already in use")
	ErrRetireBackwards    = errors.New("retire_prior_to moved backwards")
	ErrRetireBeyondIssued = errors.New("retire_prior_to beyond issued sequence")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// CID is a server side connection id tag. Zero is reserved for connections
// that negotiated the zero length CID.
type CID uint64

// ZeroLengthCID is the tag reserved for connections that do not use CIDs.
const ZeroLengthCID CID = 0

// CIDState reports whether a CID is still active or already retiring.
type CIDState string

const (
	// CIDActive marks a CID that is still part of the active set.
	CIDActive CIDState = "active"
	// CIDRetired marks a CID that is only kept for in flight packets.
	CIDRetired CIDState = "retired"
)

// CIDLimits bounds one ledger.
type CIDLimits struct {
	MaxActivePerConn int
	MaxConnections   int
	GracePeriod      time.Duration
}

// CIDInfo is one entry of a connection CID table.
type CIDInfo struct {
	Value CID
	Seq   uint64
	State CIDState
}

// LedgerStats reports aggregate counters.
type LedgerStats struct {
	Connections int
	ActiveCIDs  int
	RetiredCIDs int
	RoutedCIDs  int
	ZeroLength  int
}

type cidEntry struct {
	Value      CID
	Seq        uint64
	State      CIDState
	GraceUntil time.Time
}

type connState struct {
	ID       string
	Zero     bool
	Released bool
	Entries  []cidEntry
	MaxSeq   uint64
	PriorTo  uint64
}

type routeEntry struct {
	ConnID     string
	Retired    bool
	GraceUntil time.Time
}

// CIDRotationLedger keeps per connection CID tables and the DCID route table.
type CIDRotationLedger struct {
	mu     sync.RWMutex
	limits CIDLimits
	conns  map[string]*connState
	routes map[CID]routeEntry
	next   CID
}

// NewCIDRotationLedger builds an empty ledger with the supplied limits.
func NewCIDRotationLedger(lim CIDLimits) *CIDRotationLedger {
	return &CIDRotationLedger{
		limits: lim,
		conns:  make(map[string]*connState),
		routes: make(map[CID]routeEntry),
		next:   1,
	}
}

// Open registers a connection. A zeroLength connection is not routable by CID
// and lives in the default bucket only.
func (l *CIDRotationLedger) Open(connID string, zeroLength bool) error {
	if strings.TrimSpace(connID) == "" {
		return ErrBlankConn
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.conns[connID]; ok {
		return ErrConnExists
	}
	l.conns[connID] = &connState{ID: connID, Zero: zeroLength}
	return nil
}

// Issue mints n fresh CIDs for one connection and binds every one of them in
// the route table.
func (l *CIDRotationLedger) Issue(connID string, n int) ([]CID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	conn, ok := l.conns[connID]
	if !ok {
		return nil, ErrConnNotFound
	}
	if conn.Released {
		return nil, ErrConnReleased
	}
	if conn.Zero {
		return nil, ErrZeroLengthCID
	}
	if n <= 0 {
		return nil, ErrBadCount
	}
	active := 0
	for _, e := range conn.Entries {
		if e.State == CIDActive {
			active++
		}
	}
	if l.limits.MaxActivePerConn > 0 && active+n > l.limits.MaxActivePerConn {
		return nil, ErrActiveLimit
	}
	out := make([]CID, 0, n)
	for len(out) < n {
		v := l.next
		l.next++
		if v == ZeroLengthCID {
			continue
		}
		if _, taken := l.routes[v]; taken {
			continue
		}
		conn.MaxSeq++
		conn.Entries = append(conn.Entries, cidEntry{
			Value: v,
			Seq:   conn.MaxSeq,
			State: CIDActive,
		})
		l.routes[v] = routeEntry{ConnID: connID}
		out = append(out, v)
	}
	return out, nil
}

// Claim binds an externally supplied CID to one connection.
func (l *CIDRotationLedger) Claim(connID string, dcid CID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	conn, ok := l.conns[connID]
	if !ok {
		return ErrConnNotFound
	}
	if conn.Released {
		return ErrConnReleased
	}
	if conn.Zero || dcid == ZeroLengthCID {
		return ErrZeroLengthCID
	}
	if route, taken := l.routes[dcid]; taken {
		if route.ConnID == connID {
			return nil
		}
		return ErrCIDInUse
	}
	conn.MaxSeq++
	conn.Entries = append(conn.Entries, cidEntry{
		Value: dcid,
		Seq:   conn.MaxSeq,
		State: CIDActive,
	})
	l.routes[dcid] = routeEntry{ConnID: connID}
	return nil
}

// Retire moves every CID whose sequence is below priorTo into the grace window.
func (l *CIDRotationLedger) Retire(connID string, priorTo uint64, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	conn, ok := l.conns[connID]
	if !ok {
		return ErrConnNotFound
	}
	if priorTo < conn.PriorTo {
		return ErrRetireBackwards
	}
	if priorTo > conn.MaxSeq {
		return ErrRetireBeyondIssued
	}
	graceUntil := now.Add(l.limits.GracePeriod)
	for i := range conn.Entries {
		e := &conn.Entries[i]
		if e.State != CIDActive || e.Seq >= priorTo {
			continue
		}
		e.State = CIDRetired
		e.GraceUntil = graceUntil
		l.routes[e.Value] = routeEntry{
			ConnID:     connID,
			Retired:    true,
			GraceUntil: graceUntil,
		}
	}
	conn.PriorTo = priorTo
	return nil
}

// Route resolves a DCID to its connection while the CID is still in the table.
func (l *CIDRotationLedger) Route(dcid CID) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	route, ok := l.routes[dcid]
	if !ok {
		return "", false
	}
	return route.ConnID, true
}

// Release closes a connection and starts the grace window of its CIDs.
func (l *CIDRotationLedger) Release(connID string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	conn, ok := l.conns[connID]
	if !ok {
		return ErrConnNotFound
	}
	if conn.Released {
		return ErrConnReleased
	}
	conn.Released = true
	graceUntil := now.Add(l.limits.GracePeriod)
	for i := range conn.Entries {
		e := &conn.Entries[i]
		if e.State != CIDActive {
			continue
		}
		e.State = CIDRetired
		e.GraceUntil = graceUntil
		l.routes[e.Value] = routeEntry{
			ConnID:     connID,
			Retired:    true,
			GraceUntil: graceUntil,
		}
	}
	return nil
}

// Sweep reclaims every retired route whose grace window elapsed and reports
// how many routes were reclaimed.
func (l *CIDRotationLedger) Sweep(now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	reclaimed := 0
	for value, route := range l.routes {
		if !route.Retired || now.Before(route.GraceUntil) {
			continue
		}
		delete(l.routes, value)
		reclaimed++
		if conn, ok := l.conns[route.ConnID]; ok {
			kept := conn.Entries[:0]
			for _, e := range conn.Entries {
				if e.Value != value {
					kept = append(kept, e)
				}
			}
			conn.Entries = kept
		}
	}
	return reclaimed
}

// ActiveCIDs lists the active CID values of a connection in ascending order.
func (l *CIDRotationLedger) ActiveCIDs(connID string) []CID {
	return l.listCIDs(connID, CIDActive)
}

// RetiredCIDs lists the retiring CID values of a connection in ascending order.
func (l *CIDRotationLedger) RetiredCIDs(connID string) []CID {
	return l.listCIDs(connID, CIDRetired)
}

func (l *CIDRotationLedger) listCIDs(connID string, state CIDState) []CID {
	l.mu.RLock()
	defer l.mu.RUnlock()
	conn, ok := l.conns[connID]
	if !ok {
		return nil
	}
	var out []CID
	for _, e := range conn.Entries {
		if e.State == state {
			out = append(out, e.Value)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// DefaultConnections lists the zero length CID connections in ascending order.
func (l *CIDRotationLedger) DefaultConnections() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []string
	for id, conn := range l.conns {
		if conn.Zero {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Stats reports the current table sizes.
func (l *CIDRotationLedger) Stats() LedgerStats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	stats := LedgerStats{RoutedCIDs: len(l.routes)}
	for _, conn := range l.conns {
		stats.Connections++
		if conn.Zero {
			stats.ZeroLength++
		}
		for _, e := range conn.Entries {
			switch e.State {
			case CIDActive:
				stats.ActiveCIDs++
			case CIDRetired:
				stats.RetiredCIDs++
			}
		}
	}
	return stats
}

type snapshotEntry struct {
	Value      CID
	Seq        uint64
	State      CIDState
	GraceUntil time.Time
}

type snapshotConn struct {
	ID       string
	Zero     bool
	Released bool
	MaxSeq   uint64
	PriorTo  uint64
	Entries  []snapshotEntry
}

type snapshot struct {
	Next  CID
	Conns []snapshotConn
}

// Snapshot serialises the connection tables and the route table.
func (l *CIDRotationLedger) Snapshot() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snap := snapshot{Next: l.next}
	for _, conn := range l.conns {
		sc := snapshotConn{
			ID:       conn.ID,
			Zero:     conn.Zero,
			Released: conn.Released,
			MaxSeq:   conn.MaxSeq,
			PriorTo:  conn.PriorTo,
		}
		for _, e := range conn.Entries {
			sc.Entries = append(sc.Entries, snapshotEntry{
				Value:      e.Value,
				Seq:        e.Seq,
				State:      e.State,
				GraceUntil: e.GraceUntil,
			})
		}
		snap.Conns = append(snap.Conns, sc)
	}
	sort.Slice(snap.Conns, func(i, j int) bool { return snap.Conns[i].ID < snap.Conns[j].ID })
	return json.Marshal(snap)
}

// Restore replaces every table with a previously taken snapshot.
func (l *CIDRotationLedger) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	conns := make(map[string]*connState, len(snap.Conns))
	routes := make(map[CID]routeEntry)
	next := snap.Next
	for _, sc := range snap.Conns {
		if strings.TrimSpace(sc.ID) == "" {
			return ErrBadSnapshot
		}
		if _, dup := conns[sc.ID]; dup {
			return ErrBadSnapshot
		}
		conn := &connState{
			ID:       sc.ID,
			Zero:     sc.Zero,
			Released: sc.Released,
			MaxSeq:   sc.MaxSeq,
			PriorTo:  sc.PriorTo,
		}
		for _, se := range sc.Entries {
			if se.Value == ZeroLengthCID {
				return ErrBadSnapshot
			}
			if se.State != CIDActive && se.State != CIDRetired {
				return ErrBadSnapshot
			}
			if _, dup := routes[se.Value]; dup {
				return ErrBadSnapshot
			}
			conn.Entries = append(conn.Entries, cidEntry{
				Value:      se.Value,
				Seq:        se.Seq,
				State:      se.State,
				GraceUntil: se.GraceUntil,
			})
			routes[se.Value] = routeEntry{
				ConnID:     sc.ID,
				Retired:    se.State == CIDRetired,
				GraceUntil: se.GraceUntil,
			}
			if se.Value >= next {
				next = se.Value + 1
			}
		}
		conns[sc.ID] = conn
	}
	if next == 0 {
		next = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns = conns
	l.routes = routes
	l.next = next
	return nil
}
