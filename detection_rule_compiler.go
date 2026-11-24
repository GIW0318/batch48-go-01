// Package task compiles textual detection rules into an ordered, dependency
// bound rule pack that scanners borrow while it is live and hand back when the
// scan finishes.
package task

import (
	"bytes"
	"container/heap"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Errors surfaced while parsing, validating and publishing rule packs.
var (
	ErrSourceSyntax      = errors.New("detection rule compiler: source syntax")
	ErrDuplicateRule     = errors.New("detection rule compiler: duplicate rule id")
	ErrUnknownRule       = errors.New("detection rule compiler: unknown rule id")
	ErrDependencyCycle   = errors.New("detection rule compiler: dependency cycle")
	ErrUnknownDependency = errors.New("detection rule compiler: unknown dependency")
	ErrMutualExclusion   = errors.New("detection rule compiler: mutual exclusion")
	ErrJumpOutOfRange    = errors.New("detection rule compiler: jump out of range")
	ErrCostBudget        = errors.New("detection rule compiler: cost budget exceeded")
	ErrVersionRollback   = errors.New("detection rule compiler: dependency version rollback")
	ErrPackNotBound      = errors.New("detection rule compiler: pack not bound")
	ErrNoActivePack      = errors.New("detection rule compiler: no active pack")
)

// Diagnostic codes reported by Validate and attached to compiled packs.
const (
	CodeDuplicateRule       = "duplicate-rule"
	CodeDependencyCycle     = "dependency-cycle"
	CodeUnknownRule         = "unknown-rule"
	CodeMutualExclusion     = "mutual-exclusion"
	CodeJumpRange           = "jump-range"
	CodeJumpCaseInsensitive = "jump-case-insensitive"
	CodeCostBudget          = "cost-budget"
)

// Jump is a bounded hexadecimal range jump declared by a rule.
type Jump struct {
	Offset int
	Lo     byte
	Hi     byte
}

// Rule is one unit of detection logic after parsing.
type Rule struct {
	ID        string
	Pattern   string
	Mods      []string
	Deps      []string
	Conflicts []string
	Severity  int
	Jump      *Jump
	Line      int
}

// Dependency is an external package requirement pinned to a minimum version.
type Dependency struct {
	Name       string
	MinVersion uint64
}

// Diagnostic describes one problem or warning found while validating a pack.
type Diagnostic struct {
	Rule    string
	Code    string
	Message string
}

// RulePack is an immutable, ordered set of rules ready to be distributed.
type RulePack struct {
	Name     string
	Version  uint64
	Rules    []Rule
	Order    []string
	Deps     []Dependency
	Resolved map[string]uint64
	Warnings []Diagnostic
}

// Budget is the static cost estimate of a pack.
type Budget struct {
	Rules     int
	Atoms     int
	Backtrack int
	Bytes     int
}

// Options configures a compiler instance.
type Options struct {
	Name         string
	Version      uint64
	MaxRules     int
	MaxAtoms     int
	MaxBacktrack int
}

// CompilerStats counts compiler activity.
type CompilerStats struct {
	Compiles  uint64
	Publishes uint64
	Retired   uint64
	Released  uint64
	Borrowed  uint64
}

// DetectionRuleCompiler turns textual rule sources into published packs.
type DetectionRuleCompiler struct {
	mu                sync.Mutex
	opts              Options
	active            *RulePack
	refs              map[*RulePack]int
	retired           map[*RulePack]struct{}
	publishedVersions map[string]uint64
	stats             CompilerStats
}

// NewDetectionRuleCompiler returns a compiler configured by opts.
func NewDetectionRuleCompiler(opts Options) *DetectionRuleCompiler {
	return &DetectionRuleCompiler{
		opts:              opts,
		refs:              make(map[*RulePack]int),
		retired:           make(map[*RulePack]struct{}),
		publishedVersions: make(map[string]uint64),
	}
}

// Options reports the configuration the compiler was created with.
func (c *DetectionRuleCompiler) Options() Options {
	return c.opts
}

// Compile parses src and turns it into an ordered rule pack.
func (c *DetectionRuleCompiler) Compile(src []byte) (*RulePack, error) {
	c.mu.Lock()
	c.stats.Compiles++
	c.mu.Unlock()

	rules, err := parseRuleSource(src)
	if err != nil {
		return nil, err
	}
	if c.opts.MaxRules > 0 && len(rules) > c.opts.MaxRules {
		return nil, fmt.Errorf("%w: %d rules exceed maximum %d", ErrCostBudget, len(rules), c.opts.MaxRules)
	}

	deps, err := resolveRuleDependencies(rules)
	if err != nil {
		return nil, err
	}
	if left, right, ok := findMutualConflict(rules); ok {
		return nil, fmt.Errorf("%w: rules %q and %q", ErrMutualExclusion, left, right)
	}

	order, err := topologicalOrder(rules)
	if err != nil {
		return nil, err
	}

	pack := &RulePack{
		Name:    c.opts.Name,
		Version: c.opts.Version,
		Rules:   rules,
		Order:   order,
		Deps:    deps,
	}
	for _, rule := range rules {
		if warning, ok := jumpCaseInsensitiveWarning(rule); ok {
			pack.Warnings = append(pack.Warnings, warning)
		}
	}
	if _, err := c.estimateCost(pack, true); err != nil {
		return nil, err
	}
	return pack, nil
}

// Validate reports every diagnostic finding in pack.
func (c *DetectionRuleCompiler) Validate(pack *RulePack) []Diagnostic {
	if pack == nil {
		return nil
	}

	var diagnostics []Diagnostic
	seen := make(map[string]struct{})
	add := func(d Diagnostic) {
		key := d.Code + "\x00" + d.Rule + "\x00" + d.Message
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		diagnostics = append(diagnostics, d)
	}

	for _, warning := range pack.Warnings {
		add(warning)
	}

	first := make(map[string]int, len(pack.Rules))
	for _, rule := range pack.Rules {
		if firstLine, ok := first[rule.ID]; ok {
			add(Diagnostic{
				Rule:    rule.ID,
				Code:    CodeDuplicateRule,
				Message: fmt.Sprintf("duplicate rule %q at line %d (first at line %d)", rule.ID, rule.Line, firstLine),
			})
			continue
		}
		first[rule.ID] = rule.Line
	}

	known := make(map[string]struct{}, len(pack.Rules)+len(pack.Deps)+len(pack.Resolved))
	for _, rule := range pack.Rules {
		known[rule.ID] = struct{}{}
	}
	for _, dep := range pack.Deps {
		known[dep.Name] = struct{}{}
	}
	for name := range pack.Resolved {
		known[name] = struct{}{}
	}

	for _, rule := range pack.Rules {
		for _, dep := range rule.Deps {
			if _, ok := known[dep]; !ok {
				add(Diagnostic{
					Rule:    rule.ID,
					Code:    CodeUnknownRule,
					Message: fmt.Sprintf("rule %q depends on unknown rule or package %q", rule.ID, dep),
				})
			}
		}
		if !jumpInRange(rule.Jump) {
			add(Diagnostic{
				Rule:    rule.ID,
				Code:    CodeJumpRange,
				Message: fmt.Sprintf("rule %q has an invalid jump range", rule.ID),
			})
		}
		if warning, ok := jumpCaseInsensitiveWarning(rule); ok {
			add(warning)
		}
		if c.opts.MaxBacktrack > 0 {
			_, backtrack := ruleCost(rule.Pattern)
			if backtrack > c.opts.MaxBacktrack {
				add(Diagnostic{
					Rule:    rule.ID,
					Code:    CodeCostBudget,
					Message: fmt.Sprintf("rule %q backtrack estimate %d exceeds maximum %d", rule.ID, backtrack, c.opts.MaxBacktrack),
				})
			}
		}
	}

	if cycle := dependencyCyclePath(pack.Rules, internalDependencyList(pack.Rules)); len(cycle) > 0 {
		ids := make([]string, 0, len(cycle))
		seenCycle := make(map[string]struct{}, len(cycle))
		for _, index := range cycle {
			id := pack.Rules[index].ID
			if _, ok := seenCycle[id]; ok {
				continue
			}
			seenCycle[id] = struct{}{}
			ids = append(ids, id)
		}
		for _, id := range ids {
			add(Diagnostic{
				Rule:    id,
				Code:    CodeDependencyCycle,
				Message: fmt.Sprintf("rule %q is part of dependency cycle %s", id, strings.Join(ids, " -> ")),
			})
		}
	}

	ruleByID := make(map[string]Rule, len(pack.Rules))
	for _, rule := range pack.Rules {
		if _, exists := ruleByID[rule.ID]; !exists {
			ruleByID[rule.ID] = rule
		}
	}
	reportedPairs := make(map[string]struct{})
	for _, left := range pack.Rules {
		for _, otherID := range left.Conflicts {
			if otherID == left.ID {
				continue
			}
			right, ok := ruleByID[otherID]
			if !ok || !containsName(right.Conflicts, left.ID) {
				continue
			}
			key := left.ID + "\x00" + right.ID
			if strings.Compare(right.ID, left.ID) < 0 {
				key = right.ID + "\x00" + left.ID
			}
			if _, ok := reportedPairs[key]; ok {
				continue
			}
			reportedPairs[key] = struct{}{}
			add(Diagnostic{
				Rule:    left.ID,
				Code:    CodeMutualExclusion,
				Message: fmt.Sprintf("rule %q is mutually exclusive with %q", left.ID, right.ID),
			})
			add(Diagnostic{
				Rule:    right.ID,
				Code:    CodeMutualExclusion,
				Message: fmt.Sprintf("rule %q is mutually exclusive with %q", right.ID, left.ID),
			})
		}
	}

	return diagnostics
}

// BindDeps pins the external dependencies of pack against known versions.
func (c *DetectionRuleCompiler) BindDeps(pack *RulePack, known map[string]uint64) error {
	if pack == nil {
		return fmt.Errorf("%w: nil pack", ErrSourceSyntax)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	resolved := make(map[string]uint64, len(pack.Deps))
	for _, dep := range pack.Deps {
		name := strings.TrimSpace(dep.Name)
		version, ok := known[name]
		if !ok {
			return fmt.Errorf("%w: missing package %q", ErrUnknownDependency, name)
		}
		if version < dep.MinVersion {
			return fmt.Errorf("%w: package %q version %d is below required minimum %d", ErrVersionRollback, name, version, dep.MinVersion)
		}
		if previous, exists := resolved[name]; !exists || version > previous {
			resolved[name] = version
		}
	}
	pack.Resolved = resolved
	return nil
}

// EstimateCost returns the static cost budget of pack.
func (c *DetectionRuleCompiler) EstimateCost(pack *RulePack) (Budget, error) {
	return c.estimateCost(pack, true)
}

// Publish makes pack the active pack for new scans.
func (c *DetectionRuleCompiler) Publish(pack *RulePack) (*RulePack, error) {
	if pack == nil {
		return nil, fmt.Errorf("%w: nil pack", ErrSourceSyntax)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()

	if err := validatePackStructure(pack); err != nil {
		return nil, err
	}
	if err := c.checkBindingLocked(pack); err != nil {
		return nil, err
	}
	if c.active != nil && packsEqual(c.active, pack) {
		return c.active, nil
	}

	old := c.active
	c.active = pack
	delete(c.retired, pack)

	if old != nil && old != pack {
		c.stats.Retired++
		if c.refs[old] > 0 {
			c.retired[old] = struct{}{}
		} else {
			c.stats.Released++
		}
	}

	c.stats.Publishes++
	for _, dep := range pack.Deps {
		version := pack.Resolved[dep.Name]
		if previous, ok := c.publishedVersions[dep.Name]; !ok || version > previous {
			c.publishedVersions[dep.Name] = version
		}
	}
	return pack, nil
}

// Acquire borrows the active pack and pins it until Release is called.
func (c *DetectionRuleCompiler) Acquire() (*RulePack, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()

	if c.active == nil {
		return nil, ErrNoActivePack
	}
	c.refs[c.active]++
	c.stats.Borrowed++
	return c.active, nil
}

// Release returns a borrowed pack and frees it once the last reference goes.
func (c *DetectionRuleCompiler) Release(pack *RulePack) error {
	if pack == nil {
		return fmt.Errorf("%w: nil pack", ErrUnknownRule)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()

	refs, ok := c.refs[pack]
	if !ok || refs <= 0 {
		return fmt.Errorf("%w: pack is not borrowed", ErrUnknownRule)
	}
	refs--
	if refs > 0 {
		c.refs[pack] = refs
		return nil
	}

	delete(c.refs, pack)
	if _, retired := c.retired[pack]; retired {
		delete(c.retired, pack)
		c.stats.Released++
	}
	return nil
}

// Active reports the pack new scans should use.
func (c *DetectionRuleCompiler) Active() *RulePack {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Reload compiles src and publishes it only when compilation succeeds.
func (c *DetectionRuleCompiler) Reload(src []byte) (*RulePack, error) {
	pack, err := c.Compile(src)
	if err != nil {
		return nil, err
	}

	if len(pack.Deps) > 0 {
		c.mu.Lock()
		known := make(map[string]uint64, len(pack.Deps))
		for _, dep := range pack.Deps {
			if version, ok := c.publishedVersions[dep.Name]; ok {
				known[dep.Name] = version
			} else {
				known[dep.Name] = dep.MinVersion
			}
		}
		c.mu.Unlock()
		if err := c.BindDeps(pack, known); err != nil {
			return nil, err
		}
	}

	return c.Publish(pack)
}

// Stats reports counters since the compiler was created.
func (c *DetectionRuleCompiler) Stats() CompilerStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// OrderedIDs returns the rule identifiers in publish order.
func (p *RulePack) OrderedIDs() []string {
	if p == nil {
		return nil
	}
	if len(p.Order) == 0 {
		ids := make([]string, len(p.Rules))
		for i, rule := range p.Rules {
			ids[i] = rule.ID
		}
		return ids
	}
	return append([]string(nil), p.Order...)
}

// Rule looks a rule up by identifier.
func (p *RulePack) Rule(id string) (Rule, bool) {
	if p == nil {
		return Rule{}, false
	}
	for _, rule := range p.Rules {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// Digest returns a stable fingerprint of the pack contents.
func (p *RulePack) Digest() [16]byte {
	if p == nil {
		return [16]byte{}
	}
	sum := sha256.Sum256(p.Bytes())
	var digest [16]byte
	copy(digest[:], sum[:len(digest)])
	return digest
}

// Bytes returns the canonical serialized form of the pack.
func (p *RulePack) Bytes() []byte {
	if p == nil {
		return nil
	}

	var out strings.Builder
	out.WriteString("rulepack-v1\n")
	fmt.Fprintf(&out, "name %s\n", strconv.Quote(p.Name))
	fmt.Fprintf(&out, "version %d\n", p.Version)

	ruleByID := make(map[string]Rule, len(p.Rules))
	for _, rule := range p.Rules {
		if _, exists := ruleByID[rule.ID]; !exists {
			ruleByID[rule.ID] = rule
		}
	}

	order := p.OrderedIDs()
	fmt.Fprintf(&out, "order %d\n", len(order))
	for _, id := range order {
		rule, ok := ruleByID[id]
		if !ok {
			fmt.Fprintf(&out, "missing %s\n", strconv.Quote(id))
			continue
		}
		writeCanonicalRule(&out, rule)
	}

	deps := append([]Dependency(nil), p.Deps...)
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		return deps[i].MinVersion < deps[j].MinVersion
	})
	fmt.Fprintf(&out, "deps %d\n", len(deps))
	for _, dep := range deps {
		fmt.Fprintf(&out, "dep %s %d\n", strconv.Quote(dep.Name), dep.MinVersion)
	}

	names := make([]string, 0, len(p.Resolved))
	for name := range p.Resolved {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(&out, "resolved %d\n", len(names))
	for _, name := range names {
		fmt.Fprintf(&out, "version %s %d\n", strconv.Quote(name), p.Resolved[name])
	}

	warnings := append([]Diagnostic(nil), p.Warnings...)
	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Rule != warnings[j].Rule {
			return warnings[i].Rule < warnings[j].Rule
		}
		if warnings[i].Code != warnings[j].Code {
			return warnings[i].Code < warnings[j].Code
		}
		return warnings[i].Message < warnings[j].Message
	})
	fmt.Fprintf(&out, "warnings %d\n", len(warnings))
	for _, warning := range warnings {
		fmt.Fprintf(&out, "warning %s %s %s\n",
			strconv.Quote(warning.Rule), strconv.Quote(warning.Code), strconv.Quote(warning.Message))
	}
	return []byte(out.String())
}

func (c *DetectionRuleCompiler) ensureLocked() {
	if c.refs == nil {
		c.refs = make(map[*RulePack]int)
	}
	if c.retired == nil {
		c.retired = make(map[*RulePack]struct{})
	}
	if c.publishedVersions == nil {
		c.publishedVersions = make(map[string]uint64)
	}
}

func (c *DetectionRuleCompiler) checkBindingLocked(pack *RulePack) error {
	for _, dep := range pack.Deps {
		version, ok := pack.Resolved[dep.Name]
		if !ok {
			return fmt.Errorf("%w: dependency %q is unresolved", ErrPackNotBound, dep.Name)
		}
		if version < dep.MinVersion {
			return fmt.Errorf("%w: dependency %q version %d is below required minimum %d", ErrVersionRollback, dep.Name, version, dep.MinVersion)
		}
		if previous, existed := c.publishedVersions[dep.Name]; existed && version < previous {
			return fmt.Errorf("%w: dependency %q version %d is below published version %d", ErrVersionRollback, dep.Name, version, previous)
		}
	}
	return nil
}

func (c *DetectionRuleCompiler) estimateCost(pack *RulePack, enforce bool) (Budget, error) {
	if pack == nil {
		return Budget{}, fmt.Errorf("%w: nil pack", ErrSourceSyntax)
	}

	budget := Budget{Rules: len(pack.Rules)}
	for _, rule := range pack.Rules {
		atoms, backtrack := ruleCost(rule.Pattern)
		budget.Atoms += atoms
		budget.Backtrack += backtrack
		budget.Bytes += len(rule.Pattern)
		if enforce && c.opts.MaxBacktrack > 0 && backtrack > c.opts.MaxBacktrack {
			return budget, fmt.Errorf("%w: rule %q line %d backtrack estimate %d exceeds maximum %d",
				ErrCostBudget, rule.ID, rule.Line, backtrack, c.opts.MaxBacktrack)
		}
	}
	if enforce {
		if c.opts.MaxRules > 0 && budget.Rules > c.opts.MaxRules {
			return budget, fmt.Errorf("%w: %d rules exceed maximum %d", ErrCostBudget, budget.Rules, c.opts.MaxRules)
		}
		if c.opts.MaxAtoms > 0 && budget.Atoms > c.opts.MaxAtoms {
			return budget, fmt.Errorf("%w: %d atoms exceed maximum %d", ErrCostBudget, budget.Atoms, c.opts.MaxAtoms)
		}
	}
	return budget, nil
}

func parseRuleSource(src []byte) ([]Rule, error) {
	lines := bytes.Split(src, []byte{'\n'})
	rules := make([]Rule, 0, len(lines))
	firstLine := make(map[string]int, len(lines))

	for index, raw := range lines {
		lineNumber := index + 1
		line := strings.TrimSuffix(string(raw), "\r")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields, err := splitRuleFields(line, lineNumber)
		if err != nil {
			return nil, err
		}
		id := fields[0]
		if id == "" {
			return nil, fmt.Errorf("%w: line %d has an empty rule id", ErrSourceSyntax, lineNumber)
		}
		if previous, ok := firstLine[id]; ok {
			return nil, fmt.Errorf("%w: rule %q at line %d (first at line %d)",
				ErrDuplicateRule, id, lineNumber, previous)
		}
		firstLine[id] = lineNumber

		jump, err := parseJump(fields[2], id, lineNumber)
		if err != nil {
			return nil, err
		}
		mods, err := parseMods(fields[3], lineNumber)
		if err != nil {
			return nil, err
		}
		deps, err := parseNameList(fields[4], "deps", lineNumber)
		if err != nil {
			return nil, err
		}
		conflicts, err := parseNameList(fields[5], "conflicts", lineNumber)
		if err != nil {
			return nil, err
		}
		severity, err := strconv.Atoi(fields[6])
		if err != nil {
			return nil, fmt.Errorf("%w: line %d has invalid severity %q", ErrSourceSyntax, lineNumber, fields[6])
		}

		rules = append(rules, Rule{
			ID:        id,
			Pattern:   fields[1],
			Mods:      mods,
			Deps:      deps,
			Conflicts: conflicts,
			Severity:  severity,
			Jump:      jump,
			Line:      lineNumber,
		})
	}
	return rules, nil
}

func splitRuleFields(line string, lineNumber int) ([7]string, error) {
	var fields [7]string
	parts := strings.Split(line, "|")
	if len(parts) < len(fields) {
		return fields, fmt.Errorf("%w: line %d has %d fields, want 7", ErrSourceSyntax, lineNumber, len(parts))
	}

	if len(parts) == len(fields) {
		copy(fields[:], parts)
	} else {
		fields[0] = parts[0]
		fields[1] = strings.Join(parts[1:len(parts)-5], "|")
		copy(fields[2:], parts[len(parts)-5:])
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields, nil
}

func parseMods(raw string, lineNumber int) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	mods := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		mod := strings.TrimSpace(part)
		if mod != "i" && mod != "s" && mod != "m" {
			return nil, fmt.Errorf("%w: line %d has invalid modifier %q", ErrSourceSyntax, lineNumber, mod)
		}
		if _, ok := seen[mod]; ok {
			continue
		}
		seen[mod] = struct{}{}
		mods = append(mods, mod)
	}
	return mods, nil
}

func parseNameList(raw, field string, lineNumber int) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || name == "-" {
			return nil, fmt.Errorf("%w: line %d has an empty %s entry", ErrSourceSyntax, lineNumber, field)
		}
		names = append(names, name)
	}
	return names, nil
}

func parseJump(raw, id string, lineNumber int) (*Jump, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil, nil
	}

	at := strings.LastIndexByte(raw, '@')
	if at < 0 {
		return nil, fmt.Errorf("%w: rule %q line %d has malformed jump %q", ErrSourceSyntax, id, lineNumber, raw)
	}
	rangeText := strings.TrimSpace(raw[:at])
	offsetText := strings.TrimSpace(raw[at+1:])
	bounds := strings.Split(rangeText, "-")
	if len(bounds) != 2 || offsetText == "" {
		return nil, fmt.Errorf("%w: rule %q line %d has malformed jump %q", ErrSourceSyntax, id, lineNumber, raw)
	}

	lo, err := parseHexValue(bounds[0])
	if err != nil {
		return nil, fmt.Errorf("%w: rule %q line %d has malformed jump %q", ErrSourceSyntax, id, lineNumber, raw)
	}
	hi, err := parseHexValue(bounds[1])
	if err != nil {
		return nil, fmt.Errorf("%w: rule %q line %d has malformed jump %q", ErrSourceSyntax, id, lineNumber, raw)
	}
	offset, err := parseUintValue(offsetText)
	if err != nil {
		return nil, fmt.Errorf("%w: rule %q line %d has malformed jump %q", ErrSourceSyntax, id, lineNumber, raw)
	}

	if offset > 255 || lo > 0xff || hi > 0xff || lo > hi {
		return nil, fmt.Errorf("%w: rule %q line %d jump %q", ErrJumpOutOfRange, id, lineNumber, raw)
	}
	return &Jump{Offset: int(offset), Lo: byte(lo), Hi: byte(hi)}, nil
}

func parseHexValue(raw string) (uint64, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		value = value[2:]
	}
	if value == "" {
		return 0, errors.New("empty hexadecimal value")
	}
	return strconv.ParseUint(value, 16, 64)
}

func parseUintValue(raw string) (uint64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("empty numeric value")
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return strconv.ParseUint(value[2:], 16, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}

func resolveRuleDependencies(rules []Rule) ([]Dependency, error) {
	ids := make(map[string]int, len(rules))
	for _, rule := range rules {
		ids[rule.ID] = 0
	}

	deps := make([]Dependency, 0)
	depIndex := make(map[string]int)
	for i := range rules {
		cleaned := make([]string, 0, len(rules[i].Deps))
		for _, token := range rules[i].Deps {
			at := strings.LastIndexByte(token, '@')
			if at < 0 {
				if _, ok := ids[token]; !ok {
					return nil, fmt.Errorf("%w: rule %q line %d depends on missing %q",
						ErrUnknownDependency, rules[i].ID, rules[i].Line, token)
				}
				cleaned = append(cleaned, token)
				continue
			}

			name := strings.TrimSpace(token[:at])
			versionText := strings.TrimSpace(token[at+1:])
			if name == "" || strings.Contains(name, "@") || versionText == "" {
				return nil, fmt.Errorf("%w: rule %q line %d has malformed dependency %q",
					ErrSourceSyntax, rules[i].ID, rules[i].Line, token)
			}
			minimum, err := parseUintValue(versionText)
			if err != nil {
				return nil, fmt.Errorf("%w: rule %q line %d has malformed dependency %q",
					ErrSourceSyntax, rules[i].ID, rules[i].Line, token)
			}
			cleaned = append(cleaned, name)
			if index, ok := depIndex[name]; ok {
				if minimum > deps[index].MinVersion {
					deps[index].MinVersion = minimum
				}
			} else {
				depIndex[name] = len(deps)
				deps = append(deps, Dependency{Name: name, MinVersion: minimum})
			}
		}
		rules[i].Deps = cleaned
	}
	return deps, nil
}

type ruleIndexHeap []int

func (h ruleIndexHeap) Len() int           { return len(h) }
func (h ruleIndexHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h ruleIndexHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *ruleIndexHeap) Push(value any)    { *h = append(*h, value.(int)) }
func (h *ruleIndexHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func topologicalOrder(rules []Rule) ([]string, error) {
	indexByID := make(map[string]int, len(rules))
	for i, rule := range rules {
		indexByID[rule.ID] = i
	}

	dependencies := make([][]int, len(rules))
	dependents := make([][]int, len(rules))
	indegree := make([]int, len(rules))
	for i, rule := range rules {
		seen := make(map[int]struct{}, len(rule.Deps))
		for _, name := range rule.Deps {
			dep, ok := indexByID[name]
			if !ok {
				continue
			}
			if _, duplicate := seen[dep]; duplicate {
				continue
			}
			seen[dep] = struct{}{}
			dependencies[i] = append(dependencies[i], dep)
			dependents[dep] = append(dependents[dep], i)
			indegree[i]++
		}
	}

	ready := &ruleIndexHeap{}
	heap.Init(ready)
	for i, degree := range indegree {
		if degree == 0 {
			heap.Push(ready, i)
		}
	}

	order := make([]string, 0, len(rules))
	for ready.Len() > 0 {
		index := heap.Pop(ready).(int)
		order = append(order, rules[index].ID)
		for _, dependent := range dependents[index] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				heap.Push(ready, dependent)
			}
		}
	}

	if len(order) != len(rules) {
		return nil, dependencyCycleError(rules, dependencies)
	}
	return order, nil
}

func internalDependencyList(rules []Rule) [][]int {
	indexByID := make(map[string]int, len(rules))
	for i, rule := range rules {
		indexByID[rule.ID] = i
	}

	dependencies := make([][]int, len(rules))
	for i, rule := range rules {
		seen := make(map[int]struct{}, len(rule.Deps))
		for _, name := range rule.Deps {
			dep, ok := indexByID[name]
			if !ok {
				continue
			}
			if _, duplicate := seen[dep]; duplicate {
				continue
			}
			seen[dep] = struct{}{}
			dependencies[i] = append(dependencies[i], dep)
		}
	}
	return dependencies
}

func dependencyCyclePath(rules []Rule, dependencies [][]int) []int {
	colors := make([]int, len(rules))
	positions := make([]int, len(rules))
	stack := make([]int, 0, len(rules))
	cycle := make([]int, 0)

	var visit func(int) bool
	visit = func(node int) bool {
		colors[node] = 1
		positions[node] = len(stack)
		stack = append(stack, node)

		for _, dependency := range dependencies[node] {
			switch colors[dependency] {
			case 0:
				if visit(dependency) {
					return true
				}
			case 1:
				start := positions[dependency]
				cycle = append(cycle, stack[start:]...)
				cycle = append(cycle, dependency)
				return true
			}
		}

		stack = stack[:len(stack)-1]
		colors[node] = 2
		return false
	}

	for i := range rules {
		if colors[i] == 0 && visit(i) {
			break
		}
	}
	return cycle
}

func dependencyCycleError(rules []Rule, dependencies [][]int) error {
	cycle := dependencyCyclePath(rules, dependencies)
	if len(cycle) == 0 {
		return ErrDependencyCycle
	}

	ids := make([]string, len(cycle))
	for i, index := range cycle {
		ids[i] = rules[index].ID
	}
	return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(ids, " -> "))
}

func findMutualConflict(rules []Rule) (string, string, bool) {
	byID := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		if _, exists := byID[rule.ID]; !exists {
			byID[rule.ID] = rule
		}
	}
	for _, left := range rules {
		for _, otherID := range left.Conflicts {
			if otherID == left.ID {
				continue
			}
			right, ok := byID[otherID]
			if ok && containsName(right.Conflicts, left.ID) {
				return left.ID, right.ID, true
			}
		}
	}
	return "", "", false
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func jumpCaseInsensitiveWarning(rule Rule) (Diagnostic, bool) {
	if rule.Jump == nil || !containsName(rule.Mods, "i") {
		return Diagnostic{}, false
	}
	return Diagnostic{
		Rule:    rule.ID,
		Code:    CodeJumpCaseInsensitive,
		Message: fmt.Sprintf("rule %q combines a jump with a case-insensitive modifier", rule.ID),
	}, true
}

func jumpInRange(jump *Jump) bool {
	if jump == nil {
		return true
	}
	return jump.Offset >= 0 && jump.Offset <= 255 && jump.Lo <= jump.Hi
}

func ruleCost(pattern string) (atoms, backtrack int) {
	for _, character := range pattern {
		if !isRegexSpecial(character) {
			atoms++
		}
	}
	if atoms < 1 {
		atoms = 1
	}

	quantifiers := strings.Count(pattern, "*") + strings.Count(pattern, "+")
	alternations := strings.Count(pattern, "|")
	backtrack = (quantifiers + 1) * (alternations + 1)
	if backtrack < atoms {
		backtrack = atoms
	}
	return atoms, backtrack
}

func isRegexSpecial(character rune) bool {
	switch character {
	case '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '\\':
		return true
	default:
		return false
	}
}

func validatePackStructure(pack *RulePack) error {
	if pack == nil {
		return fmt.Errorf("%w: nil pack", ErrSourceSyntax)
	}

	firstLine := make(map[string]int, len(pack.Rules))
	known := make(map[string]struct{}, len(pack.Rules)+len(pack.Deps)+len(pack.Resolved))
	for _, rule := range pack.Rules {
		if rule.ID == "" {
			return fmt.Errorf("%w: line %d has an empty rule id", ErrSourceSyntax, rule.Line)
		}
		if previous, ok := firstLine[rule.ID]; ok {
			return fmt.Errorf("%w: rule %q at line %d (first at line %d)",
				ErrDuplicateRule, rule.ID, rule.Line, previous)
		}
		firstLine[rule.ID] = rule.Line
		known[rule.ID] = struct{}{}
	}
	for _, dep := range pack.Deps {
		known[dep.Name] = struct{}{}
	}
	for name := range pack.Resolved {
		known[name] = struct{}{}
	}
	for _, rule := range pack.Rules {
		for _, dep := range rule.Deps {
			if _, ok := known[dep]; !ok {
				return fmt.Errorf("%w: rule %q line %d depends on missing %q",
					ErrUnknownDependency, rule.ID, rule.Line, dep)
			}
		}
		if !jumpInRange(rule.Jump) {
			return fmt.Errorf("%w: rule %q line %d", ErrJumpOutOfRange, rule.ID, rule.Line)
		}
	}
	if cycle := dependencyCyclePath(pack.Rules, internalDependencyList(pack.Rules)); len(cycle) > 0 {
		ids := make([]string, len(cycle))
		for i, index := range cycle {
			ids[i] = pack.Rules[index].ID
		}
		return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(ids, " -> "))
	}
	if left, right, ok := findMutualConflict(pack.Rules); ok {
		return fmt.Errorf("%w: rules %q and %q", ErrMutualExclusion, left, right)
	}
	return nil
}

func packsEqual(left, right *RulePack) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return bytes.Equal(left.Bytes(), right.Bytes())
}

func writeCanonicalRule(out *strings.Builder, rule Rule) {
	jump := "-"
	if rule.Jump != nil {
		jump = fmt.Sprintf("%d:%d:%d", rule.Jump.Offset, rule.Jump.Lo, rule.Jump.Hi)
	}
	fmt.Fprintf(out, "rule %s %s %s %s %s %s %d\n",
		strconv.Quote(rule.ID),
		strconv.Quote(rule.Pattern),
		jump,
		joinQuoted(rule.Mods),
		joinQuoted(rule.Deps),
		joinQuoted(rule.Conflicts),
		rule.Severity,
	)
}

func joinQuoted(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	var out strings.Builder
	for i, value := range values {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(value))
	}
	return out.String()
}
