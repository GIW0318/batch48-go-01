// Package task compiles textual detection rules into an ordered, dependency
// bound rule pack that scanners borrow while it is live and hand back when the
// scan finishes.
package task

import "errors"

var errNotImplemented = errors.New("detection rule compiler: not implemented")

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
	opts Options
}

// NewDetectionRuleCompiler returns a compiler configured by opts.
func NewDetectionRuleCompiler(opts Options) *DetectionRuleCompiler {
	return &DetectionRuleCompiler{opts: opts}
}

// Options reports the configuration the compiler was created with.
func (c *DetectionRuleCompiler) Options() Options {
	return Options{}
}

// Compile parses src and turns it into an ordered rule pack.
func (c *DetectionRuleCompiler) Compile(src []byte) (*RulePack, error) {
	return nil, errNotImplemented
}

// Validate reports every diagnostic finding in pack.
func (c *DetectionRuleCompiler) Validate(pack *RulePack) []Diagnostic {
	return nil
}

// BindDeps pins the external dependencies of pack against known versions.
func (c *DetectionRuleCompiler) BindDeps(pack *RulePack, known map[string]uint64) error {
	return errNotImplemented
}

// EstimateCost returns the static cost budget of pack.
func (c *DetectionRuleCompiler) EstimateCost(pack *RulePack) (Budget, error) {
	return Budget{}, errNotImplemented
}

// Publish makes pack the active pack for new scans.
func (c *DetectionRuleCompiler) Publish(pack *RulePack) (*RulePack, error) {
	return nil, errNotImplemented
}

// Acquire borrows the active pack and pins it until Release is called.
func (c *DetectionRuleCompiler) Acquire() (*RulePack, error) {
	return nil, errNotImplemented
}

// Release returns a borrowed pack and frees it once the last reference goes.
func (c *DetectionRuleCompiler) Release(pack *RulePack) error {
	return errNotImplemented
}

// Active reports the pack new scans should use.
func (c *DetectionRuleCompiler) Active() *RulePack {
	return nil
}

// Reload compiles src and publishes it only when compilation succeeds.
func (c *DetectionRuleCompiler) Reload(src []byte) (*RulePack, error) {
	return nil, errNotImplemented
}

// Stats reports counters since the compiler was created.
func (c *DetectionRuleCompiler) Stats() CompilerStats {
	return CompilerStats{}
}

// OrderedIDs returns the rule identifiers in publish order.
func (p *RulePack) OrderedIDs() []string {
	return nil
}

// Rule looks a rule up by identifier.
func (p *RulePack) Rule(id string) (Rule, bool) {
	return Rule{}, false
}

// Digest returns a stable fingerprint of the pack contents.
func (p *RulePack) Digest() [16]byte {
	return [16]byte{}
}

// Bytes returns the canonical serialized form of the pack.
func (p *RulePack) Bytes() []byte {
	return nil
}
