package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Rule set failures.
var (
	ErrEmptyRuleList     = errors.New("rule list is empty")
	ErrPathNotAbsolute   = errors.New("rule path is not absolute")
	ErrBadPermission     = errors.New("permission set is empty or contains unknown letters")
	ErrBadField          = errors.New("unknown audit field")
	ErrBadOperator       = errors.New("operator is not allowed for the field")
	ErrRuleAlreadyActive = errors.New("an equivalent watch is already loaded")
	ErrUnknownRuleID     = errors.New("rule id is not active")
	ErrBadSnapshot       = errors.New("snapshot payload is corrupt")
)

// Field is one filter clause of a rule.
type Field struct {
	Name  string
	Op    string
	Value string
}

// Rule is one declarative audit rule.
type Rule struct {
	Path     string
	Perm     string
	Fields   []Field
	Priority int
	Watch    bool
}

// Watch is one compiled kernel watch with all equivalent filters merged.
type Watch struct {
	Path    string
	Perm    string
	Filters []string
	Indexes []int
}

// Program is the compiled load sequence.
type Program struct {
	Rules   []Rule
	Watches []Watch
}

// Stats counts rule set activity.
type Stats struct {
	Compiled int
	Loaded   int
	Unloaded int
	Rejected int
}

// AuditRuleSet owns the active audit watches of one host.
type AuditRuleSet struct {
	nextID  int
	entries map[int]Watch
	stats   Stats
}

// NewAuditRuleSet builds an empty rule set.
func NewAuditRuleSet() *AuditRuleSet {
	return &AuditRuleSet{}
}

// NormalizePath canonicalises an absolute audit path.
func NormalizePath(path string) (string, error) {
	return "", errNotImplemented
}

// ValidateRule checks one rule before compilation.
func ValidateRule(r Rule) error {
	return errNotImplemented
}

// MergeWatches folds equivalent rules into shared watches.
func MergeWatches(rules []Rule) []Watch {
	return nil
}

// Compile turns declarative rules into the kernel load sequence.
func Compile(rules []Rule) (Program, error) {
	return Program{}, errNotImplemented
}

// Render returns the canonical byte representation of a program.
func Render(p Program) string {
	return ""
}

// Load installs a whole program or nothing at all.
func (s *AuditRuleSet) Load(p Program) ([]int, error) {
	return nil, errNotImplemented
}

// Unload removes a whole batch of rule ids or nothing at all.
func (s *AuditRuleSet) Unload(ids []int) error {
	return errNotImplemented
}

// Active returns the loaded rule ids in ascending order.
func (s *AuditRuleSet) Active() []int {
	return nil
}

// Rules returns the loaded rules in id order.
func (s *AuditRuleSet) Rules() []Rule {
	return nil
}

// Snapshot serialises the rule set state.
func (s *AuditRuleSet) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot and keeps the id watermark.
func (s *AuditRuleSet) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (s *AuditRuleSet) Stats() Stats {
	return Stats{}
}
