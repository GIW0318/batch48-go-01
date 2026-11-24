package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the patch presence checker.
var (
	ErrInvalidFix      = errors.New("patch_presence_checker: invalid fix")
	ErrDuplicateFix    = errors.New("patch_presence_checker: duplicate fix")
	ErrUnknownFix      = errors.New("patch_presence_checker: unknown fix")
	ErrInvalidVersion  = errors.New("patch_presence_checker: invalid version")
	ErrInvalidRef      = errors.New("patch_presence_checker: invalid artifact reference")
	ErrBatchTooLarge   = errors.New("patch_presence_checker: batch over limit")
	ErrUnknownArtifact = errors.New("patch_presence_checker: unknown artifact")
	ErrBadSnapshot     = errors.New("patch_presence_checker: bad snapshot")
	ErrInvalidLimit    = errors.New("patch_presence_checker: invalid limit")
)

// Fix is one upstream fix with its version range and function fingerprint.
type Fix struct {
	ID           string
	Component    string
	IntroducedIn string
	FixedIn      string
	Function     string
	Digest       string
}

// Function is one function of a built artifact as a normalised instruction list.
type Function struct {
	Name         string
	Instructions []string
}

// ArtifactRef identifies one built artifact and carries its functions.
type ArtifactRef struct {
	ID        string
	Component string
	Version   string
	Functions []Function
}

// Presence is the verdict for one artifact.
type Presence struct {
	Status     string
	Confidence int
	Evidence   []string
}

// Explanation replays the evidence steps behind one verdict.
type Explanation struct {
	FixID string
	Steps []string
}

// CheckerOptions bounds the batch size and the verdict cache.
type CheckerOptions struct {
	MaxBatch  int
	CacheSize int
}

// CheckerStats is the accumulated counter view of the checker.
type CheckerStats struct {
	Registered int
	Checked    int
	CacheHits  int
	Uncertain  int
}

// PatchPresenceChecker decides whether an artifact already contains a fix.
type PatchPresenceChecker struct {
	opts CheckerOptions
}

// NewPatchPresenceChecker builds a checker with the given limits.
func NewPatchPresenceChecker(opts CheckerOptions) *PatchPresenceChecker {
	return &PatchPresenceChecker{opts: opts}
}

// Register adds one fix to the checker.
func (c *PatchPresenceChecker) Register(f Fix) error {
	return errNotImplemented
}

// Check decides the presence of the component fix in one artifact.
func (c *PatchPresenceChecker) Check(ref ArtifactRef) (Presence, error) {
	return Presence{}, errNotImplemented
}

// Explain replays the evidence of the last verdict for an artifact.
func (c *PatchPresenceChecker) Explain(id string) (Explanation, error) {
	return Explanation{}, errNotImplemented
}

// Batch checks many artifacts, deduplicating by artifact id.
func (c *PatchPresenceChecker) Batch(refs []ArtifactRef) (map[string]Presence, error) {
	return nil, errNotImplemented
}

// Fingerprint normalises an instruction list and returns its SHA-256 digest.
func (c *PatchPresenceChecker) Fingerprint(instructions []string) string {
	return ""
}

// CompareVersions compares two dotted numeric versions.
func (c *PatchPresenceChecker) CompareVersions(a, b string) (int, error) {
	return 0, errNotImplemented
}

// Fixes returns every registered fix ordered by id.
func (c *PatchPresenceChecker) Fixes() []Fix {
	return nil
}

// Cached returns the cached verdict for an artifact id.
func (c *PatchPresenceChecker) Cached(id string) (Presence, bool) {
	return Presence{}, false
}

// Stats returns a consistent view of the counters.
func (c *PatchPresenceChecker) Stats() CheckerStats {
	return CheckerStats{}
}

// Snapshot serialises the whole checker state.
func (c *PatchPresenceChecker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole checker state from a snapshot.
func (c *PatchPresenceChecker) Restore(data []byte) error {
	return errNotImplemented
}

// Reset returns the checker to its initial state.
func (c *PatchPresenceChecker) Reset() error {
	return errNotImplemented
}
