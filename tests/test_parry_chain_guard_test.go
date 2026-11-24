package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-24"
)

func newGuard() *impl.ParryChainGuard {
	return impl.NewParryChainGuard(impl.Options{
		HitChancePct:     90,
		ParryKeepPct:     25,
		BlockReducePct:   40,
		CritMultPct:      200,
		BackstabBonusPct: 0,
		MinDamage:        1,
	})
}

func hit(target string, roll, base int) impl.HitRequest {
	return impl.HitRequest{
		Target:     target,
		Seq:        0,
		At:         0,
		Roll:       roll,
		DodgeRoll:  99,
		ParryRoll:  99,
		BlockRoll:  99,
		CritRoll:   99,
		BaseDamage: base,
	}
}

func TestSetModifiersRejectsOutOfRange(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Dodge: 150}); !errors.Is(err, impl.ErrInvalidModifiers) {
		t.Fatalf("dodge above hundred error = %v, want ErrInvalidModifiers", err)
	}
	if err := g.SetModifiers("boss", impl.Modifiers{HitBonus: -200}); !errors.Is(err, impl.ErrInvalidModifiers) {
		t.Fatalf("hit bonus below minus hundred error = %v, want ErrInvalidModifiers", err)
	}
}

func TestResolveRejectsEmptyTarget(t *testing.T) {
	g := newGuard()
	if _, err := g.Resolve(hit("", 0, 100)); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty target error = %v, want ErrInvalidRequest", err)
	}
}

func TestImmuneTargetTakesNoDamage(t *testing.T) {
	g := newGuard()
	if err := g.Immune("boss", 1000); err != nil {
		t.Fatalf("immune: %v", err)
	}
	req := hit("boss", 0, 100)
	req.At = 500
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "immune" || res.Damage != 0 {
		t.Fatalf("immunity outcome = %+v, want reason immune with zero damage", res)
	}
}

func TestImmunityExpiresAfterWindow(t *testing.T) {
	g := newGuard()
	if err := g.Immune("boss", 1000); err != nil {
		t.Fatalf("immune: %v", err)
	}
	if until, ok := g.ImmuneUntil("boss"); !ok || until != 1000 {
		t.Fatalf("immune deadline = %d/%v, want 1000/true", until, ok)
	}
	req := hit("boss", 0, 100)
	req.At = 1500
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "hit" || res.Damage != 100 {
		t.Fatalf("outcome after immunity = %+v, want a 100 damage hit", res)
	}
}

func TestMissWhenRollReachesHitChance(t *testing.T) {
	g := newGuard()
	res, err := g.Resolve(hit("boss", 90, 100))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "miss" || res.Damage != 0 {
		t.Fatalf("hit roll at the hit chance = %+v, want reason miss with zero damage", res)
	}
}

func TestDodgeShortCircuitsDamage(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Dodge: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.DodgeRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "dodge" || res.Damage != 0 {
		t.Fatalf("dodge outcome = %+v, want reason dodge with zero damage", res)
	}
}

func TestParryKeepsConfiguredFraction(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Parry: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.ParryRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "parry" || res.Damage != 25 {
		t.Fatalf("parry outcome = %+v, want reason parry with 25 damage", res)
	}
	if res.Blocked {
		t.Fatalf("parry outcome also reported a block")
	}
}

func TestBlockReducesLandedDamage(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Block: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.BlockRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "block" || res.Damage != 60 || !res.Blocked {
		t.Fatalf("block outcome = %+v, want reason block with 60 damage", res)
	}
}

func TestCritMultipliesLandedDamage(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Crit: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.CritRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "crit" || res.Damage != 200 || !res.Crit {
		t.Fatalf("crit outcome = %+v, want reason crit with 200 damage", res)
	}
}

func TestBackstabSkipsAvoidance(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Dodge: 100, Parry: 100, Block: 100}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.Behind = true
	req.DodgeRoll = 0
	req.ParryRoll = 0
	req.BlockRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Reason != "hit" || res.Damage != 100 || res.Blocked {
		t.Fatalf("backstab outcome = %+v, want a plain 100 damage hit", res)
	}
}

func TestExplainReturnsReasonCode(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Parry: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	req := hit("boss", 0, 100)
	req.ParryRoll = 0
	res, err := g.Resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := g.Explain(res); got != "parry" {
		t.Fatalf("explain of a parried hit = %q, want parry", got)
	}
	plain, err := g.Resolve(hit("boss", 0, 100))
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got := g.Explain(plain); got != "hit" {
		t.Fatalf("explain of a landed hit = %q, want hit", got)
	}
}

func TestStatsCountEveryOutcome(t *testing.T) {
	g := newGuard()
	if err := g.Immune("boss", 1000); err != nil {
		t.Fatalf("immune: %v", err)
	}
	if err := g.SetModifiers("boss", impl.Modifiers{Dodge: 50, Parry: 50, Crit: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	immuneReq := hit("boss", 0, 100)
	immuneReq.At = 500
	cases := []impl.HitRequest{
		immuneReq,
		hit("boss", 95, 100),
		hit("boss", 0, 100),
		hit("boss", 0, 100),
		hit("boss", 0, 100),
	}
	for i := 1; i < len(cases); i++ {
		cases[i].At = 2000
	}
	cases[2].DodgeRoll = 0
	cases[3].ParryRoll = 0
	cases[4].CritRoll = 0
	for i, req := range cases {
		if _, err := g.Resolve(req); err != nil {
			t.Fatalf("resolve case %d: %v", i, err)
		}
	}
	got := g.Stats()
	want := impl.Stats{Total: 5, Hits: 1, Misses: 1, Dodges: 1, Parries: 1, Crits: 1, Immunes: 1}
	if got != want {
		t.Fatalf("stats = %+v, want %+v", got, want)
	}
}

func TestBatchResolvesEachRequest(t *testing.T) {
	g := newGuard()
	reqs := []impl.HitRequest{
		hit("alpha", 0, 100),
		hit("beta", 95, 100),
		hit("gamma", 0, 50),
	}
	reqs[1].At = 10
	reqs[2].At = 20
	got, err := g.Batch(reqs)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("batch results = %d, want 3", len(got))
	}
	if got[0].Target != "alpha" || got[0].Reason != "hit" || got[0].Damage != 100 {
		t.Fatalf("first batch outcome = %+v, want alpha hit for 100", got[0])
	}
	if got[1].Target != "beta" || got[1].Reason != "miss" {
		t.Fatalf("second batch outcome = %+v, want beta miss", got[1])
	}
	if got[2].Target != "gamma" || got[2].Damage != 50 {
		t.Fatalf("third batch outcome = %+v, want gamma hit for 50", got[2])
	}
	if total := g.Stats().Total; total != 3 {
		t.Fatalf("stats total after batch = %d, want 3", total)
	}
}

func TestSnapshotRestorePreservesModifiersAndStats(t *testing.T) {
	g := newGuard()
	if err := g.SetModifiers("boss", impl.Modifiers{Dodge: 50}); err != nil {
		t.Fatalf("modifiers: %v", err)
	}
	if err := g.Immune("boss", 800); err != nil {
		t.Fatalf("immune: %v", err)
	}
	req := hit("boss", 0, 100)
	req.At = 900
	req.DodgeRoll = 0
	if _, err := g.Resolve(req); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newGuard()
	if err := other.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := other.Stats(); got != g.Stats() {
		t.Fatalf("restored stats = %+v, want %+v", got, g.Stats())
	}
	if until, ok := other.ImmuneUntil("boss"); !ok || until != 800 {
		t.Fatalf("restored immunity = %d/%v, want 800/true", until, ok)
	}
	again := hit("boss", 0, 100)
	again.At = 2000
	again.DodgeRoll = 0
	res, err := other.Resolve(again)
	if err != nil {
		t.Fatalf("resolve after restore: %v", err)
	}
	if res.Reason != "dodge" {
		t.Fatalf("restored modifiers gave reason %q, want dodge", res.Reason)
	}
}

func TestConcurrentResolveKeepsStatsConsistent(t *testing.T) {
	g := newGuard()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				req := hit("boss", 0, 100)
				req.Seq = worker*20 + j
				if _, err := g.Resolve(req); err != nil {
					return
				}
			}
		}(i)
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = g.Immune("aux", int64(1000+j))
			}
		}(i)
	}
	wg.Wait()
	if got := g.Stats().Total; got != 160 {
		t.Fatalf("stats total after concurrent resolves = %d, want 160", got)
	}
}
