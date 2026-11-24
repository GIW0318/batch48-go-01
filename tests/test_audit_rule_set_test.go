package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-13"
)

func TestNormalizePathCollapsesRedundantSegments(t *testing.T) {
	got, err := impl.NormalizePath("/etc//ssh/./sshd_config/")
	if err != nil {
		t.Fatalf("normalising an absolute path failed: %v", err)
	}
	if got != "/etc/ssh/sshd_config" {
		t.Fatalf("normalised path is %q, want /etc/ssh/sshd_config", got)
	}
	root, err := impl.NormalizePath("/")
	if err != nil {
		t.Fatalf("normalising the root path failed: %v", err)
	}
	if root != "/" {
		t.Fatalf("root normalised to %q, want /", root)
	}
}

func TestNormalizePathRejectsRelativePath(t *testing.T) {
	got, err := impl.NormalizePath("etc/passwd")
	if !errors.Is(err, impl.ErrPathNotAbsolute) {
		t.Fatalf("expected ErrPathNotAbsolute, got %v", err)
	}
	if got != "" {
		t.Fatalf("rejected path returned %q, want empty string", got)
	}
}

func TestCompileRejectsUnknownField(t *testing.T) {
	rules := []impl.Rule{{
		Path:   "/etc/passwd",
		Perm:   "w",
		Fields: []impl.Field{{Name: "mystery", Op: "=", Value: "1"}},
	}}
	if _, err := impl.Compile(rules); !errors.Is(err, impl.ErrBadField) {
		t.Fatalf("expected ErrBadField for an unknown field, got %v", err)
	}
}

func TestCompileRejectsOperatorNotAllowedForField(t *testing.T) {
	rules := []impl.Rule{{
		Path:   "/usr/bin/ssh",
		Perm:   "x",
		Fields: []impl.Field{{Name: "comm", Op: ">", Value: "sshd"}},
	}}
	if _, err := impl.Compile(rules); !errors.Is(err, impl.ErrBadOperator) {
		t.Fatalf("expected ErrBadOperator for comm with >, got %v", err)
	}
}

func TestCompileRejectsEmptyRuleList(t *testing.T) {
	if _, err := impl.Compile(nil); !errors.Is(err, impl.ErrEmptyRuleList) {
		t.Fatalf("expected ErrEmptyRuleList, got %v", err)
	}
}

func TestCompileOrdersByPriorityThenDeclaration(t *testing.T) {
	rules := []impl.Rule{
		{Path: "/etc/a", Perm: "w", Priority: 5},
		{Path: "/etc/b", Perm: "w", Priority: 10},
		{Path: "/etc/c", Perm: "w", Priority: 10},
	}
	program, err := impl.Compile(rules)
	if err != nil {
		t.Fatalf("compiling three rules failed: %v", err)
	}
	if len(program.Rules) != 3 {
		t.Fatalf("program carries %d rules, want 3", len(program.Rules))
	}
	want := []string{"/etc/b", "/etc/c", "/etc/a"}
	for i, path := range want {
		if program.Rules[i].Path != path {
			t.Fatalf("rule %d is %q, want %q", i, program.Rules[i].Path, path)
		}
	}
}

func TestCompileIsByteReproducible(t *testing.T) {
	build := func() []impl.Rule {
		return []impl.Rule{
			{Path: "/etc/passwd", Perm: "wa", Priority: 2, Fields: []impl.Field{{Name: "uid", Op: "=", Value: "1000"}}},
			{Path: "/var/log/auth.log", Perm: "w", Priority: 1},
			{Path: "/etc/shadow", Perm: "r", Priority: 3},
		}
	}
	first, err := impl.Compile(build())
	if err != nil {
		t.Fatalf("first compile failed: %v", err)
	}
	second, err := impl.Compile(build())
	if err != nil {
		t.Fatalf("second compile failed: %v", err)
	}
	a := impl.Render(first)
	b := impl.Render(second)
	if a == "" {
		t.Fatalf("render returned an empty program")
	}
	if a != b {
		t.Fatalf("two compiles of the same rules render differently:\n%s\n---\n%s", a, b)
	}
}

func TestMergeEquivalentWatches(t *testing.T) {
	rules := []impl.Rule{
		{Path: "/etc/passwd", Perm: "wa", Fields: []impl.Field{{Name: "uid", Op: "=", Value: "2000"}}},
		{Path: "/etc/passwd/", Perm: "wa", Fields: []impl.Field{{Name: "auid", Op: "=", Value: "1000"}}},
	}
	got := impl.MergeWatches(rules)
	if len(got) != 1 {
		t.Fatalf("equivalent rules merged into %d watches, want 1", len(got))
	}
	if got[0].Path != "/etc/passwd" || got[0].Perm != "wa" {
		t.Fatalf("merged watch is path=%q perm=%q, want /etc/passwd wa", got[0].Path, got[0].Perm)
	}
	if len(got[0].Filters) != 2 {
		t.Fatalf("merged watch carries %d filters, want 2", len(got[0].Filters))
	}
	if got[0].Filters[0] != "auid=1000" || got[0].Filters[1] != "uid=2000" {
		t.Fatalf("merged filters are %v, want sorted [auid=1000 uid=2000]", got[0].Filters)
	}
	if len(got[0].Indexes) != 2 || got[0].Indexes[0] != 0 || got[0].Indexes[1] != 1 {
		t.Fatalf("merged indexes are %v, want [0 1]", got[0].Indexes)
	}
}

func TestMergeKeepsDifferentPermissionsSeparate(t *testing.T) {
	rules := []impl.Rule{
		{Path: "/etc/passwd", Perm: "wa"},
		{Path: "/etc/passwd", Perm: "r"},
	}
	got := impl.MergeWatches(rules)
	if len(got) != 2 {
		t.Fatalf("different permission sets produced %d watches, want 2", len(got))
	}
	if got[0].Perm != "wa" || got[1].Perm != "r" {
		t.Fatalf("watch permissions are %q,%q, want wa,r", got[0].Perm, got[1].Perm)
	}
}

func TestLoadAssignsMonotonicIDs(t *testing.T) {
	set := impl.NewAuditRuleSet()
	program, err := impl.Compile([]impl.Rule{
		{Path: "/etc/a", Perm: "w"},
		{Path: "/etc/b", Perm: "r"},
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	ids, err := set.Load(program)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("assigned ids are %v, want [1 2]", ids)
	}
	active := set.Active()
	if len(active) != 2 || active[0] != 1 || active[1] != 2 {
		t.Fatalf("active ids are %v, want [1 2]", active)
	}
}

func TestLoadIsAtomicOnConflict(t *testing.T) {
	set := impl.NewAuditRuleSet()
	first, err := impl.Compile([]impl.Rule{{Path: "/etc/a", Perm: "w"}})
	if err != nil {
		t.Fatalf("first compile failed: %v", err)
	}
	if _, err := set.Load(first); err != nil {
		t.Fatalf("first load failed: %v", err)
	}
	clashing, err := impl.Compile([]impl.Rule{
		{Path: "/etc/b", Perm: "w"},
		{Path: "/etc/a", Perm: "w"},
	})
	if err != nil {
		t.Fatalf("second compile failed: %v", err)
	}
	if _, err := set.Load(clashing); !errors.Is(err, impl.ErrRuleAlreadyActive) {
		t.Fatalf("expected ErrRuleAlreadyActive, got %v", err)
	}
	active := set.Active()
	if len(active) != 1 || active[0] != 1 {
		t.Fatalf("failed load changed active ids to %v, want [1]", active)
	}
	if got := set.Rules(); len(got) != 1 || got[0].Path != "/etc/a" {
		t.Fatalf("failed load changed loaded rules to %+v", got)
	}
}

func TestUnloadIsAtomicWhenIDMissing(t *testing.T) {
	set := impl.NewAuditRuleSet()
	program, err := impl.Compile([]impl.Rule{
		{Path: "/etc/a", Perm: "w"},
		{Path: "/etc/b", Perm: "w"},
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if _, err := set.Load(program); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := set.Unload([]int{2, 999}); !errors.Is(err, impl.ErrUnknownRuleID) {
		t.Fatalf("expected ErrUnknownRuleID, got %v", err)
	}
	active := set.Active()
	if len(active) != 2 || active[0] != 1 || active[1] != 2 {
		t.Fatalf("failed unload changed active ids to %v, want [1 2]", active)
	}
}

func TestUnloadThenLoadDoesNotReuseIDs(t *testing.T) {
	set := impl.NewAuditRuleSet()
	program, err := impl.Compile([]impl.Rule{{Path: "/etc/a", Perm: "w"}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	ids, err := set.Load(program)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := set.Unload(ids); err != nil {
		t.Fatalf("unload failed: %v", err)
	}
	if active := set.Active(); len(active) != 0 {
		t.Fatalf("active ids after unload are %v, want empty", active)
	}
	again, err := set.Load(program)
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	if len(again) != 1 || again[0] <= ids[0] {
		t.Fatalf("reloaded id is %v, want a value greater than %d", again, ids[0])
	}
}

func TestSnapshotRestoreKeepsActiveAndWatermark(t *testing.T) {
	set := impl.NewAuditRuleSet()
	program, err := impl.Compile([]impl.Rule{
		{Path: "/etc/a", Perm: "w"},
		{Path: "/etc/b", Perm: "r"},
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if _, err := set.Load(program); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	snap, err := set.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewAuditRuleSet()
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	active := other.Active()
	if len(active) != 2 || active[0] != 1 || active[1] != 2 {
		t.Fatalf("restored active ids are %v, want [1 2]", active)
	}
	next, err := impl.Compile([]impl.Rule{{Path: "/etc/c", Perm: "w"}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	ids, err := other.Load(next)
	if err != nil {
		t.Fatalf("load after restore failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("post-restore load assigned %v, want [3]", ids)
	}
}

func TestStatsCountsCompilesLoadsAndRejections(t *testing.T) {
	set := impl.NewAuditRuleSet()
	good, err := impl.Compile([]impl.Rule{{Path: "/etc/a", Perm: "w"}})
	if err != nil {
		t.Fatalf("good compile failed: %v", err)
	}
	if _, err := impl.Compile([]impl.Rule{{Path: "/etc/b", Perm: "q"}}); !errors.Is(err, impl.ErrBadPermission) {
		t.Fatalf("expected ErrBadPermission for perm q, got %v", err)
	}
	if _, err := set.Load(good); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	stats := set.Stats()
	if stats.Compiled != 1 || stats.Loaded != 1 || stats.Rejected != 1 || stats.Unloaded != 0 {
		t.Fatalf("stats are %+v, want compiled=1 loaded=1 rejected=1 unloaded=0", stats)
	}
}
