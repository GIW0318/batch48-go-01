package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-c-11"
)

func TestStageRejectsNilAndInvalidPack(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	if _, err := s.Stage(nil); !errors.Is(err, impl.ErrNilPack) {
		t.Fatalf("stage nil pack: want ErrNilPack, got %v", err)
	}
	badSeverity := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 0},
	}}
	if _, err := s.Stage(badSeverity); !errors.Is(err, impl.ErrInvalidPack) {
		t.Fatalf("severity 0: want ErrInvalidPack, got %v", err)
	}
	duplicateID := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 1},
		{ID: "r1", Pattern: "exec(", Severity: 2},
	}}
	if _, err := s.Stage(duplicateID); !errors.Is(err, impl.ErrInvalidPack) {
		t.Fatalf("duplicate rule id: want ErrInvalidPack, got %v", err)
	}
}

func TestStageIsIdempotentForSameContent(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	first := &impl.RulePack{Version: 7, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 3},
		{ID: "r2", Pattern: "exec(", Severity: 4},
	}}
	ver, err := s.Stage(first)
	if err != nil {
		t.Fatalf("stage first pack: %v", err)
	}
	if ver != 7 {
		t.Fatalf("staged version: want 7, got %d", ver)
	}
	reordered := &impl.RulePack{Version: 7, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r2", Pattern: "exec(", Severity: 4},
		{ID: "r1", Pattern: "eval(", Severity: 3},
	}}
	ver, err = s.Stage(reordered)
	if err != nil {
		t.Fatalf("restage same content: %v", err)
	}
	if ver != 7 {
		t.Fatalf("restaged version: want 7, got %d", ver)
	}
	if got := s.Stats().Staged; got != 1 {
		t.Fatalf("staged counter: want 1, got %d", got)
	}
}

func TestStageDigestIgnoresRuleOrder(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	a := &impl.RulePack{Version: 3, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 2},
		{ID: "r2", Pattern: "exec(", Severity: 5},
	}}
	b := &impl.RulePack{Version: 3, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r2", Pattern: "exec(", Severity: 5},
		{ID: "r1", Pattern: "eval(", Severity: 2},
	}}
	if da, db := s.Digest(a), s.Digest(b); da != db {
		t.Fatalf("digest must ignore rule order: %q vs %q", da, db)
	}
	c := &impl.RulePack{Version: 3, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 1},
		{ID: "r2", Pattern: "exec(", Severity: 5},
	}}
	if s.Digest(a) == s.Digest(c) {
		t.Fatalf("digest must change when a severity changes")
	}
}

func TestStageEnforcesStagedLimit(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 2, MaxRetained: 4})
	for _, ver := range []uint64{1, 2} {
		p := &impl.RulePack{Version: ver, Name: "pack", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 1}}}
		if _, err := s.Stage(p); err != nil {
			t.Fatalf("stage version %d: %v", ver, err)
		}
	}
	overflow := &impl.RulePack{Version: 3, Name: "pack", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 1}}}
	if _, err := s.Stage(overflow); !errors.Is(err, impl.ErrStageLimit) {
		t.Fatalf("third staged pack: want ErrStageLimit, got %v", err)
	}
	if got := s.Stats().Staged; got != 2 {
		t.Fatalf("staged counter after rejection: want 2, got %d", got)
	}
}

func TestPublishRejectsUnknownVersion(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	if err := s.Publish(42); !errors.Is(err, impl.ErrNotStaged) {
		t.Fatalf("publish unknown version: want ErrNotStaged, got %v", err)
	}
	if s.Active() != nil {
		t.Fatalf("active pack must stay nil after a rejected publish")
	}
}

func TestPublishActivatesWholePack(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	pack := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 3},
		{ID: "r2", Pattern: "exec(", Severity: 4},
	}}
	if _, err := s.Stage(pack); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish: %v", err)
	}
	active := s.Active()
	if active == nil {
		t.Fatalf("active pack: want pack version 1, got nil")
	}
	if active.Version != 1 || active.Name != "pack-a" || len(active.Rules) != 2 {
		t.Fatalf("active pack incomplete: version=%d name=%q rules=%d", active.Version, active.Name, len(active.Rules))
	}
}

func TestPublishSameVersionIsIdempotent(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	pack := &impl.RulePack{Version: 5, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	if _, err := s.Stage(pack); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := s.Publish(5); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := s.Publish(5); err != nil {
		t.Fatalf("second publish of the same version must be idempotent: %v", err)
	}
	if got := s.Stats().Published; got != 1 {
		t.Fatalf("published counter: want 1, got %d", got)
	}
}

func TestEvaluateBindsHitsToActiveVersion(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	pack := &impl.RulePack{Version: 9, Name: "pack-a", Rules: []impl.Rule{
		{ID: "r1", Pattern: "eval(", Severity: 3},
		{ID: "r2", Pattern: "exec(", Severity: 4},
	}}
	if _, err := s.Stage(pack); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := s.Publish(9); err != nil {
		t.Fatalf("publish: %v", err)
	}
	hits, err := s.Evaluate("x eval(a) y exec(b) z")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits: want 2, got %d", len(hits))
	}
	rules := []string{hits[0].RuleID, hits[1].RuleID}
	if rules[0] != "r1" || rules[1] != "r2" {
		t.Fatalf("hit rule order: want [r1 r2], got %v", rules)
	}
	for _, h := range hits {
		if h.Version != 9 {
			t.Fatalf("hit %s bound to version %d, want 9", h.ID, h.Version)
		}
	}
	if got := s.Stats().Hits; got != 2 {
		t.Fatalf("hit counter: want 2, got %d", got)
	}
}

func TestEvaluateWithoutActiveBundle(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	if _, err := s.Evaluate("eval(a)"); !errors.Is(err, impl.ErrUnknownBundle) {
		t.Fatalf("evaluate before publish: want ErrUnknownBundle, got %v", err)
	}
}

func TestRollbackRestoresPreviousPublishedPack(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	first := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	second := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 4}}}
	if _, err := s.Stage(first); err != nil {
		t.Fatalf("stage first: %v", err)
	}
	if _, err := s.Stage(second); err != nil {
		t.Fatalf("stage second: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if err := s.Publish(2); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	if err := s.Rollback(1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	active := s.Active()
	if active == nil || active.Version != 1 || active.Name != "pack-a" {
		t.Fatalf("active pack after rollback: want version 1 (pack-a), got %+v", active)
	}
	if got := s.Stats().Rollbacks; got != 1 {
		t.Fatalf("rollback counter: want 1, got %d", got)
	}
}

func TestRollbackRejectsNeverPublishedVersion(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	p1 := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	p2 := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{{ID: "r1", Pattern: "exec(", Severity: 2}}}
	if _, err := s.Stage(p1); err != nil {
		t.Fatalf("stage first: %v", err)
	}
	if _, err := s.Stage(p2); err != nil {
		t.Fatalf("stage second: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if err := s.Rollback(2); !errors.Is(err, impl.ErrUnknownBundle) {
		t.Fatalf("rollback to never published version: want ErrUnknownBundle, got %v", err)
	}
}

func TestRetainedTrimsOldestBeyondLimit(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 1})
	p1 := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	p2 := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{{ID: "r1", Pattern: "exec(", Severity: 2}}}
	if _, err := s.Stage(p1); err != nil {
		t.Fatalf("stage first: %v", err)
	}
	if _, err := s.Stage(p2); err != nil {
		t.Fatalf("stage second: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if err := s.Publish(2); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	retained := s.Retained()
	if len(retained) != 1 || retained[0] != 2 {
		t.Fatalf("retained versions: want [2], got %v", retained)
	}
	if got := s.Stats().Evicted; got != 1 {
		t.Fatalf("evicted counter: want 1, got %d", got)
	}
	if active := s.Active(); active == nil || active.Version != 2 {
		t.Fatalf("active version must survive eviction, got %+v", active)
	}
}

func TestResolveHitSurvivesRollback(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	p1 := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	p2 := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{{ID: "r1", Pattern: "exec(", Severity: 2}}}
	if _, err := s.Stage(p1); err != nil {
		t.Fatalf("stage first: %v", err)
	}
	if _, err := s.Stage(p2); err != nil {
		t.Fatalf("stage second: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	hits, err := s.Evaluate("eval(x)")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits: want 1, got %d", len(hits))
	}
	if err := s.Publish(2); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	if err := s.Rollback(1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	ver, err := s.ResolveHit(hits[0].ID)
	if err != nil {
		t.Fatalf("resolve hit after rollback: %v", err)
	}
	if ver != 1 {
		t.Fatalf("hit resolved to version %d, want 1", ver)
	}
	if _, err := s.ResolveHit("no-such-hit"); !errors.Is(err, impl.ErrHitNotFound) {
		t.Fatalf("unknown hit: want ErrHitNotFound, got %v", err)
	}
}

func TestSnapshotRestoreKeepsActiveAndHits(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	pack := &impl.RulePack{Version: 4, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	if _, err := s.Stage(pack); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := s.Publish(4); err != nil {
		t.Fatalf("publish: %v", err)
	}
	hits, err := s.Evaluate("eval(x)")
	if err != nil || len(hits) != 1 {
		t.Fatalf("evaluate: hits=%d err=%v", len(hits), err)
	}
	data, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if active := restored.Active(); active == nil || active.Version != 4 {
		t.Fatalf("active after restore: want version 4, got %+v", active)
	}
	ver, err := restored.ResolveHit(hits[0].ID)
	if err != nil || ver != 4 {
		t.Fatalf("hit resolution after restore: version=%d err=%v", ver, err)
	}
	if got := restored.Stats().Published; got != 1 {
		t.Fatalf("published counter after restore: want 1, got %d", got)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot: want ErrBadSnapshot, got %v", err)
	}
}

func TestConcurrentEvaluateBindsSingleVersion(t *testing.T) {
	s := impl.NewRuleBundleSwapper(impl.SwapperOptions{MaxStaged: 4, MaxRetained: 4})
	p1 := &impl.RulePack{Version: 1, Name: "pack-a", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	p2 := &impl.RulePack{Version: 2, Name: "pack-b", Rules: []impl.Rule{{ID: "r1", Pattern: "eval(", Severity: 2}}}
	if _, err := s.Stage(p1); err != nil {
		t.Fatalf("stage first: %v", err)
	}
	if _, err := s.Stage(p2); err != nil {
		t.Fatalf("stage second: %v", err)
	}
	if err := s.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	type outcome struct {
		hits []impl.Hit
		err  error
	}
	results := make(chan outcome, 16)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, err := s.Evaluate("eval(secret)")
			results <- outcome{hits: hits, err: err}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		results <- outcome{err: s.Publish(2)}
	}()
	wg.Wait()
	close(results)
	seen := 0
	for res := range results {
		if res.err != nil {
			t.Fatalf("concurrent operation failed: %v", res.err)
		}
		for _, h := range res.hits {
			seen++
			ver, err := s.ResolveHit(h.ID)
			if err != nil {
				t.Fatalf("resolve concurrent hit %s: %v", h.ID, err)
			}
			if ver != h.Version {
				t.Fatalf("hit %s reports version %d but resolves to %d", h.ID, h.Version, ver)
			}
		}
	}
	if seen != 8 {
		t.Fatalf("expected one hit from each of 8 evaluations, got %d", seen)
	}
}
