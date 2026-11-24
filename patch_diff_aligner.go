// Package task parses unified diff hunks, aligns them onto drifted branches,
// attributes every hunk to a single function body and fingerprints the checks
// a patch actually adds.
package task

import "errors"

var errNotImplemented = errors.New("patch diff aligner: not implemented")

// Errors surfaced while parsing and aligning hunks.
var (
	ErrBadHeader        = errors.New("patch diff aligner: bad header")
	ErrCountMismatch    = errors.New("patch diff aligner: count mismatch")
	ErrEmptyContext     = errors.New("patch diff aligner: empty context")
	ErrHunkOutOfRange   = errors.New("patch diff aligner: hunk out of range")
	ErrNoMatch          = errors.New("patch diff aligner: no match")
	ErrAmbiguousHunk    = errors.New("patch diff aligner: ambiguous hunk")
	ErrSpanningFunction = errors.New("patch diff aligner: hunk spans functions")
	ErrUnknownFunction  = errors.New("patch diff aligner: unknown function")
	ErrNoChange         = errors.New("patch diff aligner: no change")
	ErrBadMapping       = errors.New("patch diff aligner: bad mapping")
)

// Line is one source line with its original line number.
type Line struct {
	No   int
	Text string
}

// Hunk is one parsed section of a patch.
type Hunk struct {
	Index    int
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Context  []Line
	Removed  []Line
	Added    []Line
}

// Function is one function body found in a file.
type Function struct {
	Name  string
	Start int
	End   int
}

// Mapping ties one hunk to a function body and to both branches.
type Mapping struct {
	Hunk        int
	Function    string
	BaseStart   int
	BaseEnd     int
	TargetStart int
	Shift       int
}

// Semantic is the change fingerprint of a hunk.
type Semantic struct {
	Checks []string
	Digest uint64
}

// Kind classifies what a hunk actually changes.
type Kind int

// Supported hunk kinds.
const (
	KindGuard Kind = iota
	KindRefactor
	KindUnrelated
	KindGuardRemoval
)

// Options configures an aligner instance.
type Options struct {
	MaxShift   int
	MinContext int
}

// PatchDiffAligner parses and aligns security patches.
type PatchDiffAligner struct {
	opts  Options
	hunks []Hunk
}

// NewPatchDiffAligner returns an aligner configured by opts.
func NewPatchDiffAligner(opts Options) *PatchDiffAligner {
	return &PatchDiffAligner{opts: opts}
}

// Parse splits a unified diff into hunks and keeps them for the next Align.
func (a *PatchDiffAligner) Parse(src []byte) ([]Hunk, error) {
	return nil, errNotImplemented
}

// Align maps the hunks of the most recent Parse onto base and target.
func (a *PatchDiffAligner) Align(base, target []Line) ([]Mapping, error) {
	return nil, errNotImplemented
}

// Fingerprint extracts the conditions a hunk adds.
func (a *PatchDiffAligner) Fingerprint(h Hunk) (Semantic, error) {
	return Semantic{}, errNotImplemented
}

// Classify tells what a hunk changes.
func (a *PatchDiffAligner) Classify(h Hunk) (Kind, error) {
	return 0, errNotImplemented
}

// Functions finds the function bodies of a file.
func (a *PatchDiffAligner) Functions(lines []Line) []Function {
	return nil
}

// Normalize replaces identifiers in one line with a placeholder.
func (a *PatchDiffAligner) Normalize(text string) string {
	return ""
}

// Digest combines the fingerprints of several hunks.
func (a *PatchDiffAligner) Digest(hunks []Hunk) (Semantic, error) {
	return Semantic{}, errNotImplemented
}

// Verify checks that a mapping still lands inside one function body.
func (a *PatchDiffAligner) Verify(m Mapping, base []Line) error {
	return errNotImplemented
}

// Key renders the semantic fingerprint as a stable string.
func (s *Semantic) Key() string {
	return ""
}

// Size reports how many lines a hunk touches.
func (h *Hunk) Size() int {
	return 0
}
