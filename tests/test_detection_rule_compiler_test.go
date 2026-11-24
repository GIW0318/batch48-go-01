package task

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-c-01"
)

func newCompiler() *impl.DetectionRuleCompiler {
	return impl.NewDetectionRuleCompiler(impl.Options{Name: "sensor-rules", Version: 4})
}

func compileOK(t *testing.T, c *impl.DetectionRuleCompiler, src string) *impl.RulePack {
	t.Helper()
	pack, err := c.Compile([]byte(src))
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	if pack == nil {
		t.Fatal("Compile returned a nil pack")
	}
	return pack
}

func TestCompileRejectsDuplicateRuleIDs(t *testing.T) {
	c := newCompiler()
	src := "dup | aaa | - | - | - | - | 1\ndup | bbb | - | - | - | - | 2\n"
	pack, err := c.Compile([]byte(src))
	if err == nil {
		t.Fatalf("two rules named dup must be rejected, got pack %v", pack)
	}
	if !errors.Is(err, impl.ErrDuplicateRule) {
		t.Fatalf("duplicate rule error = %v, want ErrDuplicateRule", err)
	}
	if !strings.Contains(err.Error(), "dup") {
		t.Fatalf("duplicate rule error %q must name the rule", err.Error())
	}
}

func TestCompileRejectsDependencyCycle(t *testing.T) {
	c := newCompiler()
	src := "one | aaa | - | - | two | - | 1\ntwo | bbb | - | - | one | - | 1\n"
	_, err := c.Compile([]byte(src))
	if !errors.Is(err, impl.ErrDependencyCycle) {
		t.Fatalf("cycle error = %v, want ErrDependencyCycle", err)
	}
	for _, id := range []string{"one", "two"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("cycle error %q must mention %s", err.Error(), id)
		}
	}
}

func TestCompileOrdersRulesTopologically(t *testing.T) {
	c := newCompiler()
	src := "gamma | zzz | - | - | beta | - | 3\n" +
		"beta | yyy | - | - | alpha | - | 2\n" +
		"alpha | xxx | - | - | - | - | 1\n"
	pack := compileOK(t, c, src)
	got := pack.OrderedIDs()
	want := []string{"alpha", "beta", "gamma"}
	if !slices.Equal(got, want) {
		t.Fatalf("OrderedIDs = %v, want %v", got, want)
	}
}

func TestCompileIsByteIdenticalAcrossRuns(t *testing.T) {
	c := newCompiler()
	src := "beta | a*b+c | - | i | alpha | - | 3\nalpha | abcd | - | - | - | - | 2\n"
	first := compileOK(t, c, src)
	second := compileOK(t, c, src)
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("serialized packs differ between runs:\n%q\n%q", first.Bytes(), second.Bytes())
	}
	if len(first.Bytes()) == 0 {
		t.Fatal("serialized pack must not be empty")
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("digests differ: %x vs %x", first.Digest(), second.Digest())
	}
}

func TestCompileRejectsUnknownInternalDependency(t *testing.T) {
	c := newCompiler()
	_, err := c.Compile([]byte("solo | abc | - | - | ghost | - | 1\n"))
	if !errors.Is(err, impl.ErrUnknownDependency) {
		t.Fatalf("unknown dependency error = %v, want ErrUnknownDependency", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("error %q must name the missing dependency", err.Error())
	}
}

func TestCompileRejectsJumpOutOfRange(t *testing.T) {
	c := newCompiler()
	cases := []string{
		"hop | abc | 0x10-0x20@256 | - | - | - | 2\n",
		"hop | abc | 0x40-0x1FF@4 | - | - | - | 2\n",
		"hop | abc | 0x50-0x10@4 | - | - | - | 2\n",
	}
	for _, src := range cases {
		_, err := c.Compile([]byte(src))
		if !errors.Is(err, impl.ErrJumpOutOfRange) {
			t.Fatalf("source %q error = %v, want ErrJumpOutOfRange", src, err)
		}
		if !strings.Contains(err.Error(), "hop") {
			t.Fatalf("jump error %q must name the rule", err.Error())
		}
	}
}

func TestEstimateCostReportsExactBudget(t *testing.T) {
	c := newCompiler()
	src := "alpha | abcd | - | - | - | - | 2\nbeta | a*b+c | - | - | alpha | - | 3\n"
	pack := compileOK(t, c, src)
	budget, err := c.EstimateCost(pack)
	if err != nil {
		t.Fatalf("EstimateCost: unexpected error: %v", err)
	}
	want := impl.Budget{Rules: 2, Atoms: 7, Backtrack: 7, Bytes: 9}
	if budget != want {
		t.Fatalf("Budget = %+v, want %+v", budget, want)
	}
}

func TestEstimateCostRejectsBacktrackOverBudget(t *testing.T) {
	c := impl.NewDetectionRuleCompiler(impl.Options{Name: "tight", Version: 1, MaxBacktrack: 5})
	_, err := c.Compile([]byte("greedy | a*b*c*d*e* | - | - | - | - | 1\n"))
	if !errors.Is(err, impl.ErrCostBudget) {
		t.Fatalf("backtrack error = %v, want ErrCostBudget", err)
	}
	if !strings.Contains(err.Error(), "greedy") {
		t.Fatalf("budget error %q must name the rule", err.Error())
	}
}

func TestValidateReportsMutualExclusionAndDuplicates(t *testing.T) {
	c := newCompiler()
	pack := &impl.RulePack{
		Name:    "manual",
		Version: 1,
		Rules: []impl.Rule{
			{ID: "one", Pattern: "aa", Severity: 2, Deps: []string{"ghost"}, Conflicts: []string{"two"}},
			{ID: "two", Pattern: "bb", Severity: 3, Conflicts: []string{"one"}},
			{ID: "two", Pattern: "cc", Severity: 1},
		},
	}
	diags := c.Validate(pack)
	if len(diags) < 3 {
		t.Fatalf("Validate returned %d diagnostics, want at least 3: %+v", len(diags), diags)
	}
	codes := map[string]bool{}
	for _, d := range diags {
		if d.Rule == "" {
			t.Fatalf("diagnostic %+v must identify a rule", d)
		}
		codes[d.Code] = true
	}
	for _, want := range []string{impl.CodeDuplicateRule, impl.CodeMutualExclusion, impl.CodeUnknownRule} {
		if !codes[want] {
			t.Fatalf("Validate did not report %q, got %+v", want, diags)
		}
	}
}

func TestBindDepsResolvesExternalVersions(t *testing.T) {
	c := newCompiler()
	pack := &impl.RulePack{
		Name:    "bound",
		Version: 2,
		Rules:   []impl.Rule{{ID: "r", Pattern: "abc", Severity: 1}},
		Deps:    []impl.Dependency{{Name: "base", MinVersion: 3}},
	}
	if err := c.BindDeps(pack, map[string]uint64{"base": 7}); err != nil {
		t.Fatalf("BindDeps: unexpected error: %v", err)
	}
	if got := pack.Resolved["base"]; got != 7 {
		t.Fatalf("Resolved[base] = %d, want 7", got)
	}
}

func TestBindDepsRejectsUnknownPackage(t *testing.T) {
	c := newCompiler()
	pack := &impl.RulePack{
		Name:    "unbound",
		Version: 2,
		Rules:   []impl.Rule{{ID: "r", Pattern: "abc", Severity: 1}},
		Deps:    []impl.Dependency{{Name: "base", MinVersion: 3}},
	}
	err := c.BindDeps(pack, map[string]uint64{})
	if !errors.Is(err, impl.ErrUnknownDependency) {
		t.Fatalf("BindDeps error = %v, want ErrUnknownDependency", err)
	}
	if !strings.Contains(err.Error(), "base") {
		t.Fatalf("BindDeps error %q must name the package", err.Error())
	}
}

func TestPublishConcurrentCompilesSinglePack(t *testing.T) {
	c := newCompiler()
	src := []byte("alpha | abcd | - | - | - | - | 2\n")
	const workers = 8
	packs := make([]*impl.RulePack, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pack, err := c.Compile(src)
			if err != nil {
				errs[i] = err
				return
			}
			packs[i], errs[i] = c.Publish(pack)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	for i := 1; i < workers; i++ {
		if packs[i] != packs[0] {
			t.Fatalf("worker %d got a different active pack than worker 0", i)
		}
	}
	if st := c.Stats(); st.Publishes != 1 {
		t.Fatalf("Stats().Publishes = %d, want 1", st.Publishes)
	}
}

func TestAcquireKeepsRetiredPackUntilRelease(t *testing.T) {
	c := newCompiler()
	old := compileOK(t, c, "alpha | abcd | - | - | - | - | 2\n")
	if _, err := c.Publish(old); err != nil {
		t.Fatalf("Publish(old): %v", err)
	}
	borrowed, err := c.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if borrowed != old {
		t.Fatal("Acquire must return the active pack")
	}
	fresh := compileOK(t, c, "alpha | abce | - | - | - | - | 4\n")
	if _, err := c.Publish(fresh); err != nil {
		t.Fatalf("Publish(fresh): %v", err)
	}
	st := c.Stats()
	if st.Retired != 1 {
		t.Fatalf("Stats().Retired = %d, want 1", st.Retired)
	}
	if st.Released != 0 {
		t.Fatalf("retired pack must stay alive while borrowed, Released = %d", st.Released)
	}
	if err := c.Release(old); err != nil {
		t.Fatalf("Release(old): %v", err)
	}
	if st := c.Stats(); st.Released != 1 {
		t.Fatalf("Stats().Released = %d, want 1 after the last reference", st.Released)
	}
}

func TestReloadKeepsActivePackOnSyntaxError(t *testing.T) {
	c := newCompiler()
	pack := compileOK(t, c, "alpha | abcd | - | - | - | - | 2\n")
	if _, err := c.Publish(pack); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := c.Reload([]byte("this line has no fields at all")); err == nil {
		t.Fatal("Reload with malformed source must return an error")
	}
	if active := c.Active(); active != pack {
		t.Fatal("failed Reload must keep the previous active pack")
	}
}

func TestCompileRejectsConflictingRules(t *testing.T) {
	c := newCompiler()
	src := "left | aaa | - | - | - | right | 1\nright | bbb | - | - | - | left | 1\n"
	_, err := c.Compile([]byte(src))
	if !errors.Is(err, impl.ErrMutualExclusion) {
		t.Fatalf("conflict error = %v, want ErrMutualExclusion", err)
	}
	for _, id := range []string{"left", "right"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("conflict error %q must mention %s", err.Error(), id)
		}
	}
}
