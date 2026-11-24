package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-21"
)

func newGuard() *impl.SkillCooldownGuard {
	return impl.NewSkillCooldownGuard(impl.Options{MaxReducePct: 60, GroupCooldownMs: 5000})
}

func TestDefineRejectsInvalidDefinition(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "", CooldownMs: 1000, Charges: 1}); !errors.Is(err, impl.ErrInvalidSkillDef) {
		t.Fatalf("empty skill name error = %v, want ErrInvalidSkillDef", err)
	}
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 0, Charges: 1}); !errors.Is(err, impl.ErrInvalidSkillDef) {
		t.Fatalf("zero cooldown error = %v, want ErrInvalidSkillDef", err)
	}
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 1000, Charges: 3, RechargeMs: 0}); !errors.Is(err, impl.ErrInvalidSkillDef) {
		t.Fatalf("multi charge without recharge step error = %v, want ErrInvalidSkillDef", err)
	}
}

func TestDefineRejectsDuplicateName(t *testing.T) {
	g := newGuard()
	def := impl.SkillDef{Name: "fire", CooldownMs: 1000, Charges: 1}
	if err := g.Define(def); err != nil {
		t.Fatalf("first define: %v", err)
	}
	if err := g.Define(def); !errors.Is(err, impl.ErrDuplicateSkill) {
		t.Fatalf("duplicate define error = %v, want ErrDuplicateSkill", err)
	}
}

func TestCastUnknownSkillRejected(t *testing.T) {
	g := newGuard()
	if _, err := g.Cast("ghost", 0); !errors.Is(err, impl.ErrUnknownSkill) {
		t.Fatalf("cast unknown skill error = %v, want ErrUnknownSkill", err)
	}
}

func TestCastConsumesSingleCharge(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 1000, Charges: 1}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	res, err := g.Cast("fire", 0)
	if err != nil {
		t.Fatalf("cast fire: %v", err)
	}
	if res.Skill != "fire" || res.Charges != 0 || res.ChargesMax != 1 {
		t.Fatalf("cast result = %+v, want skill fire with 0 of 1 charges", res)
	}
	if got := g.Charges("fire", 0); got != 0 {
		t.Fatalf("charges right after the cast = %d, want 0", got)
	}
}

func TestCastWithoutChargesReturnsNotReady(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 2000, Charges: 1}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	if _, err := g.Cast("fire", 0); err != nil {
		t.Fatalf("first cast: %v", err)
	}
	if _, err := g.Cast("fire", 500); !errors.Is(err, impl.ErrNotReady) {
		t.Fatalf("cast without charges error = %v, want ErrNotReady", err)
	}
}

func TestChargesRecoverAtStepBoundary(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "dash", CooldownMs: 100, Charges: 2, RechargeMs: 1000}); err != nil {
		t.Fatalf("define dash: %v", err)
	}
	if _, err := g.Cast("dash", 0); err != nil {
		t.Fatalf("cast dash: %v", err)
	}
	if got := g.Charges("dash", 1000); got != 2 {
		t.Fatalf("charges at one full recharge step = %d, want 2", got)
	}
}

func TestGroupCastBlocksSiblingSkill(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 1000, Charges: 1, Group: "burst"}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	if err := g.Define(impl.SkillDef{Name: "frost", CooldownMs: 1000, Charges: 1, Group: "burst"}); err != nil {
		t.Fatalf("define frost: %v", err)
	}
	if _, err := g.Cast("fire", 0); err != nil {
		t.Fatalf("cast fire: %v", err)
	}
	if g.Ready("frost", 1000) {
		t.Fatalf("sibling skill is ready while the shared cooldown group is still cooling")
	}
	if g.Ready("fire", 4000) {
		t.Fatalf("casting skill is ready before the shared cooldown group expires")
	}
	if !g.Ready("frost", 5000) {
		t.Fatalf("sibling skill is still blocked after the shared cooldown group expired")
	}
}

func TestReduceShortensFinalCooldown(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 2000, Charges: 1}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	if err := g.Reduce("fire", 50); err != nil {
		t.Fatalf("reduce fire: %v", err)
	}
	if _, err := g.Cast("fire", 0); err != nil {
		t.Fatalf("cast fire: %v", err)
	}
	if got := g.CooldownRemaining("fire", 0); got != 1000 {
		t.Fatalf("remaining cooldown after 50 percent reduction = %d, want 1000", got)
	}
	if g.Ready("fire", 999) {
		t.Fatalf("skill is ready 1ms before the reduced cooldown expires")
	}
	if !g.Ready("fire", 1000) {
		t.Fatalf("skill is not ready at the reduced cooldown boundary")
	}
}

func TestResetRestoresFullCharges(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "dash", CooldownMs: 100, Charges: 2, RechargeMs: 1000}); err != nil {
		t.Fatalf("define dash: %v", err)
	}
	if _, err := g.Cast("dash", 0); err != nil {
		t.Fatalf("cast dash: %v", err)
	}
	if err := g.Reset("dash"); err != nil {
		t.Fatalf("reset dash: %v", err)
	}
	if got := g.Charges("dash", 0); got != 2 {
		t.Fatalf("charges after reset = %d, want 2", got)
	}
	if !g.Ready("dash", 0) {
		t.Fatalf("skill is not ready right after reset")
	}
}

func TestSnapshotRestorePreservesCharges(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "dash", CooldownMs: 100, Charges: 3, RechargeMs: 500}); err != nil {
		t.Fatalf("define dash: %v", err)
	}
	if _, err := g.Cast("dash", 0); err != nil {
		t.Fatalf("first cast: %v", err)
	}
	if _, err := g.Cast("dash", 0); err != nil {
		t.Fatalf("second cast: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newGuard()
	if err := other.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := other.Charges("dash", 0); got != 1 {
		t.Fatalf("restored charges = %d, want 1", got)
	}
	if got := other.CooldownRemaining("dash", 0); got != 500 {
		t.Fatalf("restored recharge anchor gives %d ms, want 500", got)
	}
}

func TestSnapshotRejectsGarbage(t *testing.T) {
	g := newGuard()
	if err := g.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot error = %v, want ErrBadSnapshot", err)
	}
}

func TestSkillsReturnsSortedNames(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "zeta", CooldownMs: 1000, Charges: 1}); err != nil {
		t.Fatalf("define zeta: %v", err)
	}
	if err := g.Define(impl.SkillDef{Name: "alpha", CooldownMs: 1000, Charges: 1}); err != nil {
		t.Fatalf("define alpha: %v", err)
	}
	got := g.Skills()
	if len(got) != 2 {
		t.Fatalf("skills count = %d, want 2", len(got))
	}
	if got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("skills order = %v, want [alpha zeta]", got)
	}
}

func TestCooldownRemainingCountsDown(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 2000, Charges: 1}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	if _, err := g.Cast("fire", 0); err != nil {
		t.Fatalf("cast fire: %v", err)
	}
	if got := g.CooldownRemaining("fire", 500); got != 1500 {
		t.Fatalf("remaining at 500ms = %d, want 1500", got)
	}
	if got := g.CooldownRemaining("fire", 2000); got != 0 {
		t.Fatalf("remaining at the cooldown boundary = %d, want 0", got)
	}
}

func TestGroupRemainingReportsSharedWindow(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 1000, Charges: 1, Group: "burst"}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	if _, err := g.Cast("fire", 100); err != nil {
		t.Fatalf("cast fire: %v", err)
	}
	if got := g.GroupRemaining("burst", 600); got != 4500 {
		t.Fatalf("group remaining at 600ms = %d, want 4500", got)
	}
	if got := g.GroupRemaining("burst", 5100); got != 0 {
		t.Fatalf("group remaining after the shared window = %d, want 0", got)
	}
}

func TestConcurrentCastConsumesSingleChargeOnce(t *testing.T) {
	g := newGuard()
	if err := g.Define(impl.SkillDef{Name: "fire", CooldownMs: 5000, Charges: 1}); err != nil {
		t.Fatalf("define fire: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	notReady := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := g.Cast("fire", 0)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, impl.ErrNotReady):
				notReady++
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted casts under concurrency = %d, want exactly 1", accepted)
	}
	if notReady != 7 {
		t.Fatalf("rejected casts = %d, want 7", notReady)
	}
}
