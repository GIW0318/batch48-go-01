// Package task negotiates TLS certificate compression algorithms per
// connection, enforces an output and compression ratio budget while
// decompressing and keeps the shared dictionary cache under an LRU bound.
package task

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"container/list"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
)

// Errors returned by the cache.
var (
	ErrBlankConn         = errors.New("blank connection id")
	ErrConnNotFound      = errors.New("connection not found")
	ErrAlreadyNegotiated = errors.New("algorithm already negotiated")
	ErrConnLimit         = errors.New("connection budget exhausted")
	ErrNotNegotiated     = errors.New("algorithm not negotiated")
	ErrNoCompression     = errors.New("connection uses no compression")
	ErrOutputTooLarge    = errors.New("decompressed output exceeds limit")
	ErrRatioExceeded     = errors.New("decompression ratio exceeds limit")
	ErrCorruptCompressed = errors.New("corrupt compressed payload")
	ErrBadLimits         = errors.New("invalid decompression limits")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Algorithm is one certificate compression algorithm name.
type Algorithm string

const (
	// AlgorithmNone means the peer falls back to an uncompressed certificate.
	AlgorithmNone Algorithm = "none"
	// AlgorithmZlib is a supported RFC 1950 zlib stream.
	AlgorithmZlib Algorithm = "zlib"
	// AlgorithmDeflate is a supported raw DEFLATE stream.
	AlgorithmDeflate Algorithm = "deflate"
	// AlgorithmBrotli is recognised during negotiation but not implemented.
	AlgorithmBrotli Algorithm = "brotli"
)

// CacheStats reports aggregate counters of the cache.
type CacheStats struct {
	Connections    int
	Dictionaries   int
	Decompressions int
	Evictions      int
}

type connState struct {
	Algorithm Algorithm
	DictID    string
}

type dictEntry struct {
	ID       string
	Refs     int
	LRUEntry *list.Element
}

type lruItem struct {
	algo Algorithm
}

const (
	defaultMaxOut   = 1 << 20
	defaultMaxRatio = 64
	inflateChunk    = 4096
)

var implementedAlgorithms = map[Algorithm]bool{
	AlgorithmZlib:    true,
	AlgorithmDeflate: true,
}

// CertCompressionCache keeps the negotiated algorithm of every connection and
// the shared dictionary cache.
type CertCompressionCache struct {
	mu       sync.Mutex
	maxConns int
	maxDicts int
	maxOut   int
	maxRatio int
	conns    map[string]*connState
	dicts    map[Algorithm]*dictEntry
	lru      *list.List
	stats    CacheStats
}

// NewCertCompressionCache builds a cache with the supplied connection and
// dictionary bounds. The decompression limits start at their defaults.
func NewCertCompressionCache(maxConns, maxDicts int) *CertCompressionCache {
	return &CertCompressionCache{
		maxConns: maxConns,
		maxDicts: maxDicts,
		maxOut:   defaultMaxOut,
		maxRatio: defaultMaxRatio,
		conns:    make(map[string]*connState),
		dicts:    make(map[Algorithm]*dictEntry),
		lru:      list.New(),
	}
}

// Negotiate picks the first offered algorithm the server implements. When no
// offered algorithm is implemented the connection falls back to
// AlgorithmNone and does not occupy the connection budget.
func (c *CertCompressionCache) Negotiate(connID string, offered []Algorithm) (Algorithm, bool, error) {
	if strings.TrimSpace(connID) == "" {
		return AlgorithmNone, false, ErrBlankConn
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.conns[connID]; exists {
		return AlgorithmNone, false, ErrAlreadyNegotiated
	}

	chosen := AlgorithmNone
	for _, algo := range offered {
		if implementedAlgorithms[algo] {
			chosen = algo
			break
		}
	}

	if chosen == AlgorithmNone {
		c.conns[connID] = &connState{Algorithm: AlgorithmNone}
		return AlgorithmNone, false, nil
	}

	if c.stats.Connections >= c.maxConns {
		return AlgorithmNone, false, ErrConnLimit
	}

	dictID := c.acquireDict(chosen)
	c.conns[connID] = &connState{Algorithm: chosen, DictID: dictID}
	c.stats.Connections++
	return chosen, true, nil
}

// acquireDict returns the shared dictionary of an algorithm, creating it once
// and keeping the cache within the LRU bound. Callers must hold c.mu.
func (c *CertCompressionCache) acquireDict(algo Algorithm) string {
	if entry, ok := c.dicts[algo]; ok {
		entry.Refs++
		c.touchDict(entry)
		return entry.ID
	}

	for len(c.dicts) >= c.maxDicts {
		c.evictLRUDict()
	}

	entry := &dictEntry{
		ID:   "dict-" + string(algo),
		Refs: 1,
	}
	entry.LRUEntry = c.lru.PushBack(&lruItem{algo: algo})
	c.dicts[algo] = entry
	return entry.ID
}

// evictLRUDict removes the least recently used dictionary. Callers must hold
// c.mu.
func (c *CertCompressionCache) evictLRUDict() {
	front := c.lru.Front()
	if front == nil {
		return
	}
	item := front.Value.(*lruItem)
	c.lru.Remove(front)
	delete(c.dicts, item.algo)
	c.stats.Evictions++
}

// touchDict moves a dictionary to the most recently used position. Callers
// must hold c.mu.
func (c *CertCompressionCache) touchDict(entry *dictEntry) {
	if entry.LRUEntry != nil {
		c.lru.MoveToBack(entry.LRUEntry)
	}
}

// Negotiated reports the algorithm bound to one connection.
func (c *CertCompressionCache) Negotiated(connID string) (Algorithm, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.conns[connID]
	if !ok {
		return AlgorithmNone, false
	}
	return state.Algorithm, true
}

// Decompress inflates one payload under the negotiated algorithm while
// enforcing the output size and compression ratio budget.
func (c *CertCompressionCache) Decompress(connID string, compressed []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.conns[connID]
	if !ok {
		return nil, ErrNotNegotiated
	}
	if state.Algorithm == AlgorithmNone {
		return nil, ErrNoCompression
	}

	if entry, ok := c.dicts[state.Algorithm]; ok {
		c.touchDict(entry)
	}

	var reader io.ReadCloser
	var err error
	switch state.Algorithm {
	case AlgorithmZlib:
		reader, err = zlib.NewReader(bytes.NewReader(compressed))
	case AlgorithmDeflate:
		reader = flate.NewReader(bytes.NewReader(compressed))
	default:
		return nil, ErrNotNegotiated
	}
	if err != nil {
		return nil, ErrCorruptCompressed
	}
	defer reader.Close()

	plain, err := c.inflate(reader, int64(len(compressed)))
	if err != nil {
		return nil, err
	}
	c.stats.Decompressions++
	return plain, nil
}

// inflate streams the decompressed data in fixed chunks and aborts as soon as
// the output or ratio budget is exceeded. The plaintext is never allocated as
// one whole block before the budgets are checked. Callers must hold c.mu.
func (c *CertCompressionCache) inflate(reader io.Reader, compressedLen int64) ([]byte, error) {
	plain := make([]byte, 0, inflateChunk)
	buf := make([]byte, inflateChunk)
	var produced int64

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			produced += int64(n)
			if produced > int64(c.maxOut) {
				return nil, ErrOutputTooLarge
			}
			if compressedLen > 0 && produced > int64(c.maxRatio)*compressedLen {
				return nil, ErrRatioExceeded
			}
			plain = append(plain, buf[:n]...)
		}
		if err == io.EOF {
			return plain, nil
		}
		if err != nil {
			return nil, ErrCorruptCompressed
		}
	}
}

// Reset drops the negotiation state of one connection and releases its budget.
func (c *CertCompressionCache) Reset(connID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.conns[connID]
	if !ok {
		return ErrConnNotFound
	}

	if state.Algorithm != AlgorithmNone {
		if entry, ok := c.dicts[state.Algorithm]; ok {
			if entry.Refs > 0 {
				entry.Refs--
			}
		}
		c.stats.Connections--
	}
	delete(c.conns, connID)
	return nil
}

// SetLimits replaces the decompression output and ratio limits.
func (c *CertCompressionCache) SetLimits(maxOut, maxRatio int) error {
	if maxOut <= 0 || maxRatio < 1 {
		return ErrBadLimits
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxOut = maxOut
	c.maxRatio = maxRatio
	return nil
}

// DictionaryID reports the identity of the dictionary a connection uses.
func (c *CertCompressionCache) DictionaryID(connID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.conns[connID]
	if !ok || state.Algorithm == AlgorithmNone {
		return ""
	}
	return state.DictID
}

// Stats reports connection, dictionary and decompression counters.
func (c *CertCompressionCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	stats.Dictionaries = len(c.dicts)
	return stats
}

// Snapshot serialises the active connections and their dictionary identities.
func (c *CertCompressionCache) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := snapshotFile{Version: 1}
	for connID, state := range c.conns {
		if state.Algorithm == AlgorithmNone {
			continue
		}
		snap.Connections = append(snap.Connections, snapshotConn{
			ConnID:    connID,
			Algorithm: state.Algorithm,
			DictID:    state.DictID,
		})
	}
	for algo, entry := range c.dicts {
		snap.Dictionaries = append(snap.Dictionaries, snapshotDict{
			Algorithm: algo,
			DictID:    entry.ID,
			Refs:      entry.Refs,
		})
	}

	data, err := json.Marshal(&snap)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return data, nil
}

// Restore replaces the connection and dictionary tables with a snapshot.
func (c *CertCompressionCache) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}

	var snap snapshotFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&snap); err != nil {
		return ErrBadSnapshot
	}
	if decoder.More() {
		return ErrBadSnapshot
	}
	if snap.Version != 1 {
		return ErrBadSnapshot
	}

	conns := make(map[string]*connState, len(snap.Connections))
	dicts := make(map[Algorithm]*dictEntry, len(snap.Dictionaries))
	lru := list.New()

	for _, dict := range snap.Dictionaries {
		if strings.TrimSpace(string(dict.Algorithm)) == "" ||
			!implementedAlgorithms[dict.Algorithm] ||
			strings.TrimSpace(dict.DictID) == "" {
			return ErrBadSnapshot
		}
		if _, dup := dicts[dict.Algorithm]; dup {
			return ErrBadSnapshot
		}
		dictID := dict.DictID
		entry := &dictEntry{ID: dictID}
		entry.LRUEntry = lru.PushBack(&lruItem{algo: dict.Algorithm})
		dicts[dict.Algorithm] = entry
	}

	active := 0
	for _, conn := range snap.Connections {
		if strings.TrimSpace(conn.ConnID) == "" ||
			!implementedAlgorithms[conn.Algorithm] {
			return ErrBadSnapshot
		}
		if _, dup := conns[conn.ConnID]; dup {
			return ErrBadSnapshot
		}
		entry, ok := dicts[conn.Algorithm]
		if !ok || entry.ID != conn.DictID {
			return ErrBadSnapshot
		}
		conns[conn.ConnID] = &connState{
			Algorithm: conn.Algorithm,
			DictID:    conn.DictID,
		}
		entry.Refs++
		active++
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.conns = conns
	c.dicts = dicts
	c.lru = lru
	c.stats = CacheStats{
		Connections:    active,
		Dictionaries:   len(dicts),
		Decompressions: 0,
		Evictions:      0,
	}
	return nil
}

type snapshotFile struct {
	Version      int            `json:"version"`
	Connections  []snapshotConn `json:"connections"`
	Dictionaries []snapshotDict `json:"dictionaries"`
}

type snapshotConn struct {
	ConnID    string    `json:"conn_id"`
	Algorithm Algorithm `json:"algorithm"`
	DictID    string    `json:"dict_id"`
}

type snapshotDict struct {
	Algorithm Algorithm `json:"algorithm"`
	DictID    string    `json:"dict_id"`
	Refs      int       `json:"refs"`
}
