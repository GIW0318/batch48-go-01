// Package task parses unified diff hunks, aligns them onto drifted branches,
// attributes every hunk to a single function body and fingerprints the checks
// a patch actually adds.
package task

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

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
	mu    sync.RWMutex
	opts  Options
	hunks []Hunk
}

var (
	hunkHeaderPattern = regexp.MustCompile(
		`^@@[ \t]-([0-9]+),([0-9]+)[ \t]+\+([0-9]+),([0-9]+)[ \t]@@(?:[ \t].*)?$`,
	)
	identifierPattern = regexp.MustCompile(`[\p{L}_][\p{L}\p{N}_]*`)
)

// NewPatchDiffAligner returns an aligner configured by opts.
func NewPatchDiffAligner(opts Options) *PatchDiffAligner {
	return &PatchDiffAligner{opts: opts}
}

// Parse splits a unified diff into hunks and keeps them for the next Align.
func (a *PatchDiffAligner) Parse(src []byte) ([]Hunk, error) {
	lines := splitPatchLines(src)

	hunks := make([]Hunk, 0)
	var current Hunk
	active := false

	finish := func() error {
		if !active {
			return nil
		}
		expectedOld := len(current.Context) + len(current.Removed)
		expectedNew := len(current.Context) + len(current.Added)
		if current.OldCount != expectedOld || current.NewCount != expectedNew {
			return fmt.Errorf(
				"%w: hunk %d header counts (-%d,%d +%d,%d) do not match (-%d +%d context %d)",
				ErrCountMismatch,
				current.Index,
				current.OldStart,
				current.OldCount,
				current.NewStart,
				current.NewCount,
				len(current.Removed),
				len(current.Added),
				len(current.Context),
			)
		}
		if len(current.Context) < a.opts.MinContext {
			return fmt.Errorf(
				"%w: hunk %d has %d context lines, need at least %d",
				ErrEmptyContext,
				current.Index,
				len(current.Context),
				a.opts.MinContext,
			)
		}
		hunks = append(hunks, current)
		active = false
		return nil
	}

	for lineIndex, rawLine := range lines {
		lineNo := lineIndex + 1
		line := strings.TrimSuffix(rawLine, "\r")

		if strings.HasPrefix(line, "@@") {
			if err := finish(); err != nil {
				return nil, err
			}
			header, err := parseHunkHeader(line, lineNo)
			if err != nil {
				return nil, err
			}
			header.Index = len(hunks)
			current = header
			active = true
			continue
		}

		if !active {
			continue
		}

		if strings.HasPrefix(line, `\`) {
			continue
		}

		if len(line) == 0 {
			no := current.OldStart + len(current.Context) + len(current.Removed)
			current.Context = append(current.Context, Line{No: no})
			continue
		}

		switch line[0] {
		case ' ':
			no := current.OldStart + len(current.Context) + len(current.Removed)
			current.Context = append(current.Context, Line{No: no, Text: line[1:]})
		case '-':
			no := current.OldStart + len(current.Context) + len(current.Removed)
			current.Removed = append(current.Removed, Line{No: no, Text: line[1:]})
		case '+':
			no := current.NewStart + len(current.Added)
			current.Added = append(current.Added, Line{No: no, Text: line[1:]})
		default:
			if err := finish(); err != nil {
				return nil, err
			}
		}
	}

	if err := finish(); err != nil {
		return nil, err
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("%w: no hunk sections found", ErrBadHeader)
	}

	stored := cloneHunks(hunks)
	returned := cloneHunks(hunks)

	a.mu.Lock()
	a.hunks = stored
	a.mu.Unlock()

	return returned, nil
}

// Align maps the hunks of the most recent Parse onto base and target.
func (a *PatchDiffAligner) Align(base, target []Line) ([]Mapping, error) {
	a.mu.RLock()
	hunks := cloneHunks(a.hunks)
	a.mu.RUnlock()

	sort.SliceStable(hunks, func(i, j int) bool {
		return hunks[i].Index < hunks[j].Index
	})

	baseFunctions := a.Functions(base)
	mappings := make([]Mapping, 0, len(hunks))

	for _, h := range hunks {
		if len(h.Context) == 0 {
			return nil, fmt.Errorf("%w: hunk %d has no context lines", ErrEmptyContext, h.Index)
		}

		baseMatches := findContextMatches(base, h.Context)
		if len(baseMatches) > 1 {
			return nil, fmt.Errorf("%w: hunk %d context appears %d times in base", ErrAmbiguousHunk, h.Index, len(baseMatches))
		}
		if len(baseMatches) == 0 {
			return nil, fmt.Errorf("%w: hunk %d context not found in base", ErrNoMatch, h.Index)
		}
		baseLocation := baseMatches[0]

		expectedBaseNo := h.Context[0].No
		if expectedBaseNo == 0 {
			expectedBaseNo = h.OldStart
		}
		baseDrift := baseLocation.StartNo - expectedBaseNo
		if absInt(baseDrift) > a.opts.MaxShift {
			return nil, fmt.Errorf(
				"%w: hunk %d drifted %d lines from %d to %d (MaxShift %d)",
				ErrNoMatch,
				h.Index,
				baseDrift,
				expectedBaseNo,
				baseLocation.StartNo,
				a.opts.MaxShift,
			)
		}

		targetMatches := findContextMatches(target, h.Context)
		if len(targetMatches) > 1 {
			return nil, fmt.Errorf("%w: hunk %d context appears %d times in target", ErrAmbiguousHunk, h.Index, len(targetMatches))
		}
		if len(targetMatches) == 0 {
			return nil, fmt.Errorf("%w: hunk %d context not found in target", ErrNoMatch, h.Index)
		}
		targetLocation := targetMatches[0]

		fn, err := functionForRange(baseFunctions, baseLocation.StartNo, baseLocation.EndNo)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", h.Index, err)
		}

		mappings = append(mappings, Mapping{
			Hunk:        h.Index,
			Function:    fn.Name,
			BaseStart:   baseLocation.StartNo,
			BaseEnd:     baseLocation.EndNo,
			TargetStart: targetLocation.StartNo,
			Shift:       targetLocation.StartNo - baseLocation.StartNo,
		})
	}

	return mappings, nil
}

// Fingerprint extracts the conditions a hunk adds.
func (a *PatchDiffAligner) Fingerprint(h Hunk) (Semantic, error) {
	checks := make([]string, 0, len(h.Added))
	for _, line := range h.Added {
		if lineContainsCheck(line.Text) {
			checks = append(checks, a.Normalize(line.Text))
		}
	}
	return newSemantic(checks), nil
}

// Classify tells what a hunk changes.
func (a *PatchDiffAligner) Classify(h Hunk) (Kind, error) {
	if len(h.Added) == 0 && len(h.Removed) == 0 {
		return 0, ErrNoChange
	}

	for _, line := range h.Added {
		if lineContainsCheck(line.Text) {
			return KindGuard, nil
		}
	}

	if len(h.Added) == 0 {
		for _, line := range h.Removed {
			if lineContainsCheck(line.Text) {
				return KindGuardRemoval, nil
			}
		}
		return KindRefactor, nil
	}

	for _, line := range h.Added {
		if !lineIsCommentOrBlank(line.Text) {
			return KindUnrelated, nil
		}
	}
	return KindRefactor, nil
}

// Functions finds the function bodies of a file.
func (a *PatchDiffAligner) Functions(lines []Line) []Function {
	startIndexes := make([]int, 0)
	names := make(map[int]string)

	for i, line := range lines {
		if name, ok := functionName(line.Text); ok {
			startIndexes = append(startIndexes, i)
			names[i] = name
		}
	}
	if len(startIndexes) == 0 {
		return nil
	}

	functions := make([]Function, 0, len(startIndexes))
	for order, startIndex := range startIndexes {
		endNo := lines[len(lines)-1].No
		if order+1 < len(startIndexes) {
			nextStartIndex := startIndexes[order+1]
			endNo = lines[nextStartIndex-1].No
		}
		functions = append(functions, Function{
			Name:  names[startIndex],
			Start: lines[startIndex].No,
			End:   endNo,
		})
	}
	return functions
}

// Normalize replaces identifiers in one line with a placeholder.
func (a *PatchDiffAligner) Normalize(text string) string {
	return identifierPattern.ReplaceAllStringFunc(text, func(word string) string {
		if token.Lookup(word).IsKeyword() {
			return word
		}
		return "_"
	})
}

// Digest combines the fingerprints of several hunks.
func (a *PatchDiffAligner) Digest(hunks []Hunk) (Semantic, error) {
	checks := make([]string, 0)
	for _, h := range hunks {
		semantic, err := a.Fingerprint(h)
		if err != nil {
			return Semantic{}, err
		}
		checks = append(checks, semantic.Checks...)
	}
	return newSemantic(checks), nil
}

// Verify checks that a mapping still lands inside one function body.
func (a *PatchDiffAligner) Verify(m Mapping, base []Line) error {
	if m.BaseStart == 0 || m.BaseEnd < m.BaseStart {
		return fmt.Errorf("%w: invalid base range %d-%d", ErrHunkOutOfRange, m.BaseStart, m.BaseEnd)
	}

	startIndex := findLineNumberIndex(base, m.BaseStart)
	endIndex := findLineNumberIndex(base, m.BaseEnd)
	if startIndex < 0 || endIndex < 0 || startIndex > endIndex {
		return fmt.Errorf("%w: base lines %d-%d are not present", ErrHunkOutOfRange, m.BaseStart, m.BaseEnd)
	}

	functions := a.Functions(base)
	insideFunction := false
	for _, fn := range functions {
		if fn.Name != m.Function {
			continue
		}
		if m.BaseStart >= fn.Start && m.BaseEnd <= fn.End {
			insideFunction = true
			break
		}
	}
	if !insideFunction {
		return fmt.Errorf(
			"%w: mapping %d range %d-%d is outside function %q",
			ErrHunkOutOfRange,
			m.Hunk,
			m.BaseStart,
			m.BaseEnd,
			m.Function,
		)
	}

	if m.TargetStart != 0 && m.Shift != m.TargetStart-m.BaseStart {
		return fmt.Errorf("%w: shift %d does not match %d-%d", ErrBadMapping, m.Shift, m.TargetStart, m.BaseStart)
	}
	return nil
}

// Key renders the semantic fingerprint as a stable string.
func (s *Semantic) Key() string {
	if s == nil {
		return ""
	}
	checks := append([]string(nil), s.Checks...)
	sort.Strings(checks)
	return strings.Join(checks, "\n")
}

// Size reports how many lines a hunk touches.
func (h *Hunk) Size() int {
	if h == nil {
		return 0
	}
	return len(h.Added) + len(h.Removed)
}

type contextLocation struct {
	StartIndex int
	EndIndex   int
	StartNo    int
	EndNo      int
}

func splitPatchLines(src []byte) []string {
	if len(src) == 0 {
		return nil
	}
	text := string(src)
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func parseHunkHeader(line string, lineNo int) (Hunk, error) {
	match := hunkHeaderPattern.FindStringSubmatch(line)
	if match == nil {
		return Hunk{}, fmt.Errorf("%w: line %d: %q", ErrBadHeader, lineNo, line)
	}

	values := make([]int, 4)
	for i, raw := range match[1:5] {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return Hunk{}, fmt.Errorf("%w: line %d: %v", ErrBadHeader, lineNo, err)
		}
		values[i] = value
	}

	return Hunk{
		OldStart: values[0],
		OldCount: values[1],
		NewStart: values[2],
		NewCount: values[3],
	}, nil
}

func cloneLines(src []Line) []Line {
	if src == nil {
		return nil
	}
	return append([]Line(nil), src...)
}

func cloneHunks(src []Hunk) []Hunk {
	if src == nil {
		return nil
	}
	out := make([]Hunk, len(src))
	for i, h := range src {
		out[i] = h
		out[i].Context = cloneLines(h.Context)
		out[i].Removed = cloneLines(h.Removed)
		out[i].Added = cloneLines(h.Added)
	}
	return out
}

func findContextMatches(lines, context []Line) []contextLocation {
	if len(context) == 0 || len(lines) < len(context) {
		return nil
	}

	matches := make([]contextLocation, 0)
	if contextNumbersAreOrdered(context) {
		for startIndex := range lines {
			if lines[startIndex].Text != context[0].Text {
				continue
			}
			endIndex, ok := matchSpacedContext(lines, context, startIndex)
			if ok {
				matches = appendMatch(matches, lines, startIndex, endIndex)
			}
		}
		return matches
	}

	lastStart := len(lines) - len(context)
	for startIndex := 0; startIndex <= lastStart; startIndex++ {
		matched := true
		for offset, wanted := range context {
			if lines[startIndex+offset].Text != wanted.Text {
				matched = false
				break
			}
		}
		if matched {
			endIndex := startIndex + len(context) - 1
			matches = appendMatch(matches, lines, startIndex, endIndex)
		}
	}
	return matches
}

func contextNumbersAreOrdered(context []Line) bool {
	if context[0].No <= 0 {
		return false
	}
	for i := 1; i < len(context); i++ {
		if context[i].No <= context[i-1].No {
			return false
		}
	}
	return true
}

func matchSpacedContext(lines, context []Line, startIndex int) (int, bool) {
	endIndex := startIndex
	firstNo := context[0].No
	firstActualNo := lines[startIndex].No

	for offset := 1; offset < len(context); offset++ {
		numberDelta := context[offset].No - firstNo
		wantedNo := firstActualNo + numberDelta

		nextIndex := findLineNumberIndex(lines, wantedNo)
		if nextIndex >= 0 && lines[nextIndex].Text == context[offset].Text {
			endIndex = nextIndex
			continue
		}

		indexByDelta := startIndex + numberDelta
		if indexByDelta < len(lines) && lines[indexByDelta].Text == context[offset].Text {
			nextIndex = indexByDelta
			endIndex = nextIndex
			continue
		}

		return 0, false
	}
	return endIndex, true
}

func appendMatch(matches []contextLocation, lines []Line, startIndex, endIndex int) []contextLocation {
	return append(matches, contextLocation{
		StartIndex: startIndex,
		EndIndex:   endIndex,
		StartNo:    lines[startIndex].No,
		EndNo:      lines[endIndex].No,
	})
}

func functionForRange(functions []Function, startNo, endNo int) (*Function, error) {
	startIndex := -1
	endIndex := -1
	for i := range functions {
		fn := &functions[i]
		if startNo >= fn.Start && startNo <= fn.End {
			startIndex = i
		}
		if endNo >= fn.Start && endNo <= fn.End {
			endIndex = i
		}
	}

	if startIndex < 0 || endIndex < 0 {
		return nil, fmt.Errorf("%w: lines %d-%d", ErrUnknownFunction, startNo, endNo)
	}
	if startIndex != endIndex {
		return nil, fmt.Errorf("%w: lines %d-%d belong to %q and %q",
			ErrSpanningFunction, startNo, endNo, functions[startIndex].Name, functions[endIndex].Name)
	}
	return &functions[startIndex], nil
}

func findLineNumberIndex(lines []Line, no int) int {
	for i, line := range lines {
		if line.No == no {
			return i
		}
	}
	return -1
}

func functionName(line string) (string, bool) {
	hasFuncPrefix := line == "func" ||
		strings.HasPrefix(line, "func ") ||
		strings.HasPrefix(line, "func\t") ||
		strings.HasPrefix(line, "func(")
	if !hasFuncPrefix {
		return "", false
	}

	rest := strings.TrimLeft(line[len("func"):], " \t")
	if strings.HasPrefix(rest, "(") {
		depth := 0
		closeIndex := -1
		for i, r := range rest {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					closeIndex = i
				}
			}
			if closeIndex >= 0 {
				break
			}
		}
		if closeIndex < 0 {
			return "", false
		}
		rest = strings.TrimLeft(rest[closeIndex+1:], " \t")
	}

	name := leadingIdentifier(rest)
	if name == "" {
		return "", false
	}
	return name, true
}

func leadingIdentifier(text string) string {
	for i, r := range text {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return ""
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return text[:i]
		}
	}
	return text
}

func lineScanFacts(text string) (hasCheck bool, hasCode bool) {
	fset := token.NewFileSet()
	file := fset.AddFile("line", fset.Base(), len(text))
	var lineScanner scanner.Scanner
	lineScanner.Init(file, []byte(text), func(token.Position, string) {}, scanner.ScanComments)

	for {
		_, tok, _ := lineScanner.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			continue
		}
		hasCode = true

		switch tok {
		case token.IF, token.FOR, token.SWITCH, token.CASE:
			hasCheck = true
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			hasCheck = true
		}
	}
	return hasCheck, hasCode
}

func lineContainsCheck(text string) bool {
	hasCheck, _ := lineScanFacts(text)
	return hasCheck
}

func lineIsCommentOrBlank(text string) bool {
	if strings.TrimSpace(text) == "" {
		return true
	}
	_, hasCode := lineScanFacts(text)
	return !hasCode
}

func newSemantic(checks []string) Semantic {
	sorted := append([]string(nil), checks...)
	sort.Strings(sorted)

	hash := fnv.New64a()
	for _, check := range sorted {
		_, _ = hash.Write([]byte(check))
		_, _ = hash.Write([]byte{'\n'})
	}
	return Semantic{Checks: sorted, Digest: hash.Sum64()}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
