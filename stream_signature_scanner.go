// Package task scans large files and network streams in fixed chunks for exact
// and gapped signatures, reporting every hit once with absolute offsets.
package task

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
)

// Errors surfaced while building indexes and feeding chunks.
var (
	ErrNoIndex            = errors.New("stream signature scanner: no index installed")
	ErrPatternInvalid     = errors.New("stream signature scanner: pattern invalid")
	ErrAtomTooShort       = errors.New("stream signature scanner: atom too short")
	ErrTooManyAtoms       = errors.New("stream signature scanner: too many atoms")
	ErrBadWindow          = errors.New("stream signature scanner: bad window")
	ErrChunkTooLarge      = errors.New("stream signature scanner: chunk too large")
	ErrOffsetGap          = errors.New("stream signature scanner: offset gap")
	ErrCandidatesExceeded = errors.New("stream signature scanner: candidates exceeded")
)

const (
	atomLen   = 3
	maxWindow = 1 << 20
	maxGap    = 4096
)

// PatternKind selects how a pattern is matched.
type PatternKind int

// Supported pattern kinds.
const (
	Exact PatternKind = iota
	Gapped
)

// Pattern is one signature to look for in the stream.
type Pattern struct {
	ID     string
	Kind   PatternKind
	Prefix []byte
	Suffix []byte
	Gap    int
}

// Hit is one confirmed match with absolute stream offsets; End is exclusive.
type Hit struct {
	Pattern string
	Start   int64
	End     int64
}

type compiledPattern struct {
	id      string
	kind    PatternKind
	prefix  []byte
	suffix  []byte
	gap     int
	maxSpan int
}

// AtomIndex is one complete, immutable build of the pattern bucketing.
type AtomIndex struct {
	version     int
	fingerprint uint64
	patterns    []*compiledPattern
	buckets     map[string][]*compiledPattern
	size        int
	maxSpan     int
}

// Options configures a scanner instance.
type Options struct {
	Window        int
	MaxChunk      int
	MaxAtoms      int
	MaxCandidates int
}

// ScanStats counts scanning activity.
type ScanStats struct {
	Chunks       uint64
	Bytes        int64
	Hits         uint64
	Candidates   uint64
	IndexVersion int
}

// StreamSignatureScanner matches patterns against a stream fed in chunks.
type StreamSignatureScanner struct {
	mu sync.Mutex

	opts   Options
	window int

	index       *AtomIndex
	nextVersion int

	nextOff    int64
	carry      []byte
	carryStart int64
	pending    []Hit
	stats      ScanStats
}

// NewStreamSignatureScanner returns a scanner configured by opts.
func NewStreamSignatureScanner(opts Options) *StreamSignatureScanner {
	if opts.Window < 0 || opts.Window > maxWindow {
		opts.Window = 0
	}
	if opts.MaxChunk < 0 {
		opts.MaxChunk = 0
	}
	if opts.MaxAtoms < 0 {
		opts.MaxAtoms = 0
	}
	if opts.MaxCandidates < 0 {
		opts.MaxCandidates = 0
	}
	return &StreamSignatureScanner{
		opts:   opts,
		window: opts.Window,
	}
}

// BuildAtoms validates patterns, builds an atom index and installs it.
func (s *StreamSignatureScanner) BuildAtoms(patterns []Pattern) (*AtomIndex, error) {
	idx, err := buildAtomIndex(patterns, s.opts.MaxAtoms)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.nextVersion++
	idx.version = s.nextVersion
	s.index = idx
	s.stats.IndexVersion = idx.version
	s.mu.Unlock()
	return idx, nil
}

func buildAtomIndex(patterns []Pattern, maxAtoms int) (*AtomIndex, error) {
	ids := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
		if p.ID == "" {
			return nil, fmt.Errorf("%w: pattern %q has an empty id", ErrPatternInvalid, p.ID)
		}
		if _, ok := ids[p.ID]; ok {
			return nil, fmt.Errorf("%w: duplicate pattern id %q", ErrPatternInvalid, p.ID)
		}
		ids[p.ID] = struct{}{}
	}

	compiled := make([]*compiledPattern, 0, len(patterns))
	atoms := make(map[string]struct{}, len(patterns))
	maxSpan := 0
	for _, p := range patterns {
		switch p.Kind {
		case Exact:
			if len(p.Prefix) < atomLen {
				return nil, fmt.Errorf("%w: pattern %q prefix is shorter than %d bytes", ErrAtomTooShort, p.ID, atomLen)
			}
		case Gapped:
			if len(p.Prefix) < atomLen {
				return nil, fmt.Errorf("%w: pattern %q prefix is shorter than %d bytes", ErrAtomTooShort, p.ID, atomLen)
			}
			if len(p.Suffix) == 0 {
				return nil, fmt.Errorf("%w: pattern %q has an empty suffix", ErrPatternInvalid, p.ID)
			}
			if p.Gap < 1 || p.Gap > maxGap {
				return nil, fmt.Errorf("%w: pattern %q gap %d is outside 1..%d", ErrPatternInvalid, p.ID, p.Gap, maxGap)
			}
		default:
			return nil, fmt.Errorf("%w: pattern %q has unknown kind %d", ErrPatternInvalid, p.ID, p.Kind)
		}

		atom := string(p.Prefix[:atomLen])
		atoms[atom] = struct{}{}
		if maxAtoms > 0 && len(atoms) > maxAtoms {
			return nil, fmt.Errorf("%w: %d distinct atoms exceed limit %d", ErrTooManyAtoms, len(atoms), maxAtoms)
		}

		span := len(p.Prefix)
		if p.Kind == Gapped {
			span += p.Gap + len(p.Suffix)
		}
		if span > maxSpan {
			maxSpan = span
		}
		compiled = append(compiled, &compiledPattern{
			id:      p.ID,
			kind:    p.Kind,
			prefix:  append([]byte(nil), p.Prefix...),
			suffix:  append([]byte(nil), p.Suffix...),
			gap:     p.Gap,
			maxSpan: span,
		})
	}

	sort.Slice(compiled, func(i, j int) bool { return compiled[i].id < compiled[j].id })
	buckets := make(map[string][]*compiledPattern, len(atoms))
	for _, p := range compiled {
		atom := string(p.prefix[:atomLen])
		buckets[atom] = append(buckets[atom], p)
	}

	return &AtomIndex{
		fingerprint: fingerprintPatterns(compiled),
		patterns:    compiled,
		buckets:     buckets,
		size:        len(atoms),
		maxSpan:     maxSpan,
	}, nil
}

func fingerprintPatterns(patterns []*compiledPattern) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	writeUint64 := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		_, _ = h.Write(buf[:])
	}
	writeBytes := func(b []byte) {
		writeUint64(uint64(len(b)))
		_, _ = h.Write(b)
	}

	for _, p := range patterns {
		writeBytes([]byte(p.id))
		writeUint64(uint64(p.kind))
		writeBytes(p.prefix)
		writeBytes(p.suffix)
		writeUint64(uint64(p.gap))
	}
	return h.Sum64()
}

// Install switches the scanner to a previously built index.
func (s *StreamSignatureScanner) Install(idx *AtomIndex) error {
	if idx == nil {
		return ErrNoIndex
	}
	s.mu.Lock()
	s.index = idx
	s.stats.IndexVersion = idx.version
	s.mu.Unlock()
	return nil
}

// Feed scans one chunk that starts at absolute offset off.
func (s *StreamSignatureScanner) Feed(chunk []byte, off int64) ([]Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.index
	if idx == nil {
		return nil, ErrNoIndex
	}
	if s.opts.MaxChunk > 0 && len(chunk) > s.opts.MaxChunk {
		return nil, ErrChunkTooLarge
	}
	if off < 0 || off > s.nextOff {
		return nil, ErrOffsetGap
	}

	// Data that has already been consumed may be re-delivered by the upstream.
	// Ignore the duplicate prefix and only process bytes beyond nextOff.
	if off < s.nextOff {
		overlap := s.nextOff - off
		if overlap >= int64(len(chunk)) {
			return nil, nil
		}
		chunk = chunk[overlap:]
		off = s.nextOff
	}
	if off > math.MaxInt64-int64(len(chunk)) {
		return nil, ErrOffsetGap
	}
	previousEnd := s.nextOff
	newEnd := off + int64(len(chunk))

	buf := make([]byte, 0, len(s.carry)+len(chunk))
	buf = append(buf, s.carry...)
	buf = append(buf, chunk...)
	bufStart := s.carryStart

	discovered, unresolved := scanBuffer(idx, buf, bufStart, previousEnd)

	var released, remaining []Hit
	for _, h := range s.pending {
		if s.settled(h, newEnd) {
			released = append(released, h)
		} else {
			remaining = append(remaining, h)
		}
	}
	for _, h := range discovered {
		if s.settled(h, newEnd) {
			released = append(released, h)
		} else {
			remaining = append(remaining, h)
		}
	}

	if s.opts.MaxCandidates > 0 {
		candidates := len(remaining) + unresolved
		if candidates > s.opts.MaxCandidates {
			return nil, fmt.Errorf("%w: %d pending, limit %d", ErrCandidatesExceeded, candidates, s.opts.MaxCandidates)
		}
	}

	s.pending = remaining
	s.nextOff = newEnd
	s.stats.Chunks++
	s.stats.Bytes += int64(len(chunk))
	s.stats.Hits += uint64(len(released))
	s.stats.Candidates = uint64(len(remaining) + unresolved)

	keep := idx.maxSpan - 1
	if keep < 0 {
		keep = 0
	}
	if keep > len(buf) {
		keep = len(buf)
	}
	if keep == 0 {
		s.carry = nil
		s.carryStart = newEnd
	} else {
		s.carry = append([]byte(nil), buf[len(buf)-keep:]...)
		s.carryStart = newEnd - int64(keep)
	}

	sortHits(released)
	return released, nil
}

func (s *StreamSignatureScanner) settled(h Hit, now int64) bool {
	return h.End+int64(s.window) <= now
}

func scanBuffer(idx *AtomIndex, buf []byte, base, emitAfter int64) ([]Hit, int) {
	var hits []Hit
	unresolved := 0

	for p := 0; p+atomLen <= len(buf); p++ {
		bucket := idx.buckets[string(buf[p:p+atomLen])]
		if len(bucket) == 0 {
			continue
		}

		for _, cp := range bucket {
			avail := len(buf) - p
			prefixLen := len(cp.prefix)

			if avail < prefixLen {
				if bytes.Equal(buf[p:], cp.prefix[:avail]) {
					unresolved++
				}
				continue
			}
			if !bytes.Equal(buf[p:p+prefixLen], cp.prefix) {
				continue
			}

			if cp.kind == Exact {
				start := base + int64(p)
				end := start + int64(prefixLen)
				if end > emitAfter {
					hits = append(hits, Hit{Pattern: cp.id, Start: start, End: end})
				}
				continue
			}

			suffixLen := len(cp.suffix)
			lastSuffixStart := p + prefixLen + cp.gap
			found := false
			for q := p + prefixLen + 1; q <= lastSuffixStart && q+suffixLen <= len(buf); q++ {
				if bytes.Equal(buf[q:q+suffixLen], cp.suffix) {
					start := base + int64(p)
					end := base + int64(q+suffixLen)
					if end > emitAfter {
						hits = append(hits, Hit{Pattern: cp.id, Start: start, End: end})
					}
					found = true
					break
				}
			}
			if !found && lastSuffixStart+suffixLen > len(buf) {
				unresolved++
			}
		}
	}
	return hits, unresolved
}

func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Start != hits[j].Start {
			return hits[i].Start < hits[j].Start
		}
		if hits[i].End != hits[j].End {
			return hits[i].End < hits[j].End
		}
		return hits[i].Pattern < hits[j].Pattern
	})
}

// Flush confirms hits still held in the trailing window.
func (s *StreamSignatureScanner) Flush() []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		s.stats.Candidates = 0
		return nil
	}
	hits := append([]Hit(nil), s.pending...)
	s.pending = nil
	s.stats.Hits += uint64(len(hits))
	s.stats.Candidates = 0
	sortHits(hits)
	return hits
}

// SetWindow sets how many trailing bytes are held back before reporting.
func (s *StreamSignatureScanner) SetWindow(n int) error {
	if n < 0 || n > maxWindow {
		return ErrBadWindow
	}
	s.mu.Lock()
	s.window = n
	s.opts.Window = n
	s.mu.Unlock()
	return nil
}

// Window reports the current trailing window size.
func (s *StreamSignatureScanner) Window() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.window
}

// Stats reports counters since the last reset.
func (s *StreamSignatureScanner) Stats() ScanStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.stats
	if s.index != nil {
		st.IndexVersion = s.index.version
	} else {
		st.IndexVersion = 0
	}
	return st
}

// Reset drops the installed index, pending candidates and counters.
func (s *StreamSignatureScanner) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.index = nil
	s.nextOff = 0
	s.carry = nil
	s.carryStart = 0
	s.pending = nil
	s.stats = ScanStats{}
	return nil
}

// Size reports how many atom buckets the index holds.
func (idx *AtomIndex) Size() int {
	if idx == nil {
		return 0
	}
	return idx.size
}

// Patterns reports the pattern identifiers in lexical order.
func (idx *AtomIndex) Patterns() []string {
	if idx == nil {
		return nil
	}
	out := make([]string, len(idx.patterns))
	for i, p := range idx.patterns {
		out[i] = p.id
	}
	return out
}

// Version reports the build generation of the index.
func (idx *AtomIndex) Version() int {
	if idx == nil {
		return 0
	}
	return idx.version
}

// Fingerprint returns a stable digest of the indexed patterns.
func (idx *AtomIndex) Fingerprint() uint64 {
	if idx == nil {
		return 0
	}
	return idx.fingerprint
}
