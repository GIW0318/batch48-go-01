package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-23"
)

func newGuard() *impl.ThreatTableGuard {
	return impl.NewThreatTableGuard(impl.Options{
		DamageFactorPct: 100,
		HealCasterPct:   40,
		HealTargetPct:   40,
		SwitchRatioPct:  110,
		StickMs:         1000,
		DecayPct:        25,
	})
}

func TestAddDamageRejectsNonPositiveAmount(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 0); !errors.Is(err, impl.ErrInvalidAmount) {
		t.Fatalf("zero damage error = %v, want ErrInvalidAmount", err)
	}
	if err := g.AddDamage("boss", "tank", -5); !errors.Is(err, impl.ErrInvalidAmount) {
		t.Fatalf("negative damage error = %v, want ErrInvalidAmount", err)
	}
}

func TestAddDamageAccumulatesThreatInOrder(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 300); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "healer", 200); err != nil {
		t.Fatalf("damage healer: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 100); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	got := g.Table("boss")
	if len(got) != 3 {
		t.Fatalf("table size = %d, want 3", len(got))
	}
	want := []struct {
		player string
		threat int64
	}{{"tank", 300}, {"healer", 200}, {"dps", 100}}
	for i, w := range want {
		if got[i].Player != w.player || got[i].Threat != w.threat {
			t.Fatalf("row %d = %s/%d, want %s/%d", i, got[i].Player, got[i].Threat, w.player, w.threat)
		}
	}
}

func TestAddHealSplitsBetweenCasterAndTarget(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 100); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddHeal("boss", "healer", 100); err != nil {
		t.Fatalf("heal: %v", err)
	}
	got := g.Table("boss")
	if len(got) != 2 {
		t.Fatalf("table size = %d, want 2", len(got))
	}
	if got[0].Player != "tank" || got[0].Threat != 140 {
		t.Fatalf("current target row = %s/%d, want tank/140", got[0].Player, got[0].Threat)
	}
	if got[1].Player != "healer" || got[1].Threat != 40 {
		t.Fatalf("healer row = %s/%d, want healer/40", got[1].Player, got[1].Threat)
	}
}

func TestTauntLocksCurrentDuringWindow(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 100); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 10); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	if err := g.Taunt("boss", "dps", 5000); err != nil {
		t.Fatalf("taunt: %v", err)
	}
	if who, ok := g.Current("boss", 1000); !ok || who != "dps" {
		t.Fatalf("current during taunt = %q/%v, want dps/true", who, ok)
	}
	if who, ok := g.Current("boss", 4999); !ok || who != "dps" {
		t.Fatalf("current just before taunt expiry = %q/%v, want dps/true", who, ok)
	}
}

func TestTauntRejectsInvalidWindow(t *testing.T) {
	g := newGuard()
	if err := g.Taunt("boss", "tank", 0); !errors.Is(err, impl.ErrInvalidTaunt) {
		t.Fatalf("zero taunt window error = %v, want ErrInvalidTaunt", err)
	}
	if err := g.Taunt("boss", "tank", -10); !errors.Is(err, impl.ErrInvalidTaunt) {
		t.Fatalf("negative taunt window error = %v, want ErrInvalidTaunt", err)
	}
}

func TestCurrentPicksHighestThreat(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 100); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 300); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	who, ok := g.Current("boss", 0)
	if !ok || who != "dps" {
		t.Fatalf("current = %q/%v, want dps/true", who, ok)
	}
}

func TestCurrentKeepsTargetBelowSwitchMargin(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 10); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	if who, ok := g.Current("boss", 0); !ok || who != "tank" {
		t.Fatalf("initial current = %q/%v, want tank/true", who, ok)
	}
	if err := g.AddDamage("boss", "dps", 180); err != nil {
		t.Fatalf("second damage dps: %v", err)
	}
	if who, ok := g.Current("boss", 5000); !ok || who != "tank" {
		t.Fatalf("current with a challenger below the switch margin = %q/%v, want tank/true", who, ok)
	}
}

func TestCurrentSwitchesAfterMarginAndStickWindow(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 10); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	if who, ok := g.Current("boss", 0); !ok || who != "tank" {
		t.Fatalf("initial current = %q/%v, want tank/true", who, ok)
	}
	if err := g.AddDamage("boss", "dps", 300); err != nil {
		t.Fatalf("second damage dps: %v", err)
	}
	if who, _ := g.Current("boss", 100); who != "tank" {
		t.Fatalf("current when the challenger just appeared = %q, want tank", who)
	}
	if who, _ := g.Current("boss", 600); who != "tank" {
		t.Fatalf("current before the stick window elapsed = %q, want tank", who)
	}
	if who, _ := g.Current("boss", 1100); who != "dps" {
		t.Fatalf("current after the stick window = %q, want dps", who)
	}
}

func TestTableReturnsSortedCopy(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "alpha", 100); err != nil {
		t.Fatalf("damage alpha: %v", err)
	}
	if err := g.AddDamage("boss", "beta", 300); err != nil {
		t.Fatalf("damage beta: %v", err)
	}
	got := g.Table("boss")
	if len(got) != 2 {
		t.Fatalf("table size = %d, want 2", len(got))
	}
	if got[0].Player != "beta" || got[1].Player != "alpha" {
		t.Fatalf("table order = %s,%s, want beta,alpha", got[0].Player, got[1].Player)
	}
	got[0].Threat = 9999
	again := g.Table("boss")
	if again[0].Threat == 9999 {
		t.Fatalf("mutating the returned slice changed the stored table")
	}
}

func TestTransferMovesPercentage(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.Transfer("tank", "dps", 25); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	got := g.Table("boss")
	if len(got) != 2 {
		t.Fatalf("table size = %d, want 2", len(got))
	}
	if got[0].Player != "tank" || got[0].Threat != 150 {
		t.Fatalf("source row = %s/%d, want tank/150", got[0].Player, got[0].Threat)
	}
	if got[1].Player != "dps" || got[1].Threat != 50 {
		t.Fatalf("target row = %s/%d, want dps/50", got[1].Player, got[1].Threat)
	}
}

func TestTransferRejectsInvalidPercentage(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.Transfer("tank", "dps", 0); !errors.Is(err, impl.ErrInvalidTransfer) {
		t.Fatalf("zero transfer percentage error = %v, want ErrInvalidTransfer", err)
	}
	if err := g.Transfer("tank", "dps", 101); !errors.Is(err, impl.ErrInvalidTransfer) {
		t.Fatalf("over hundred transfer percentage error = %v, want ErrInvalidTransfer", err)
	}
}

func TestTransferRejectsUnknownPlayer(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.Transfer("ghost", "dps", 50); !errors.Is(err, impl.ErrUnknownPlayer) {
		t.Fatalf("unknown source player error = %v, want ErrUnknownPlayer", err)
	}
}

func TestTickDecaysOutOfRangeEntry(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "scout", 200); err != nil {
		t.Fatalf("damage scout: %v", err)
	}
	if err := g.SetInRange("boss", "scout", false); err != nil {
		t.Fatalf("set scout out of range: %v", err)
	}
	g.Tick(1000)
	got := g.Table("boss")
	if len(got) != 2 {
		t.Fatalf("table size after decay = %d, want 2", len(got))
	}
	if got[0].Player != "tank" || got[0].Threat != 200 {
		t.Fatalf("in range row after decay = %s/%d, want tank/200", got[0].Player, got[0].Threat)
	}
	if got[1].Player != "scout" || got[1].Threat != 150 {
		t.Fatalf("out of range row after decay = %s/%d, want scout/150", got[1].Player, got[1].Threat)
	}
}

func TestSnapshotRestorePreservesThreatAndSticky(t *testing.T) {
	g := newGuard()
	if err := g.AddDamage("boss", "tank", 200); err != nil {
		t.Fatalf("damage tank: %v", err)
	}
	if err := g.AddDamage("boss", "dps", 50); err != nil {
		t.Fatalf("damage dps: %v", err)
	}
	if who, ok := g.Current("boss", 0); !ok || who != "tank" {
		t.Fatalf("initial current = %q/%v, want tank/true", who, ok)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newGuard()
	if err := other.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := other.Table("boss")
	if len(got) != 2 || got[0].Player != "tank" || got[0].Threat != 200 || got[1].Threat != 50 {
		t.Fatalf("restored table = %+v, want tank/200 and dps/50", got)
	}
	if who, ok := other.Current("boss", 10); !ok || who != "tank" {
		t.Fatalf("restored current = %q/%v, want tank/true", who, ok)
	}
}

func TestConcurrentDamageAndTickSumsUp(t *testing.T) {
	g := newGuard()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			player := "p" + string(rune('a'+i))
			_ = g.AddDamage("boss", player, int64(100+i))
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Tick(0)
		}()
	}
	wg.Wait()
	got := g.Table("boss")
	if len(got) != 8 {
		t.Fatalf("table size after concurrent writes = %d, want 8", len(got))
	}
	var sum int64
	for _, e := range got {
		sum += e.Threat
	}
	if sum != 828 {
		t.Fatalf("total threat after concurrent writes = %d, want 828", sum)
	}
}
