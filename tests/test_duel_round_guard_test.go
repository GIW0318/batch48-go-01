package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-35"
)

func duelOptions() impl.Options {
	return impl.Options{
		MaxHP:        100,
		ActionPoints: 1,
		AutoAction:   impl.Action{Kind: "wait"},
	}
}

func beginDuel(t *testing.T, g *impl.DuelRoundGuard, firstInit, secondInit int) impl.Turn {
	t.Helper()
	if err := g.Join("p1", firstInit); err != nil {
		t.Fatalf("join p1: unexpected error %v", err)
	}
	if err := g.Join("p2", secondInit); err != nil {
		t.Fatalf("join p2: unexpected error %v", err)
	}
	turn, err := g.Begin(1000)
	if err != nil {
		t.Fatalf("begin: unexpected error %v", err)
	}
	return turn
}

func TestJoinRegistersPlayersWithFullHP(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	if err := g.Join("p1", 10); err != nil {
		t.Fatalf("join p1: unexpected error %v", err)
	}
	if err := g.Join("p2", 20); err != nil {
		t.Fatalf("join p2: unexpected error %v", err)
	}
	if got := g.HP("p1"); got != 100 {
		t.Fatalf("p1 hp = %d, want 100", got)
	}
	if got := g.HP("p2"); got != 100 {
		t.Fatalf("p2 hp = %d, want 100", got)
	}
	if got := g.Round(); got != 0 {
		t.Fatalf("round before begin = %d, want 0", got)
	}
}

func TestJoinDuplicateRejected(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	if err := g.Join("p1", 10); err != nil {
		t.Fatalf("join p1: unexpected error %v", err)
	}
	if err := g.Join("p1", 99); !errors.Is(err, impl.ErrAlreadyJoined) {
		t.Fatalf("duplicate join error = %v, want ErrAlreadyJoined", err)
	}
}

func TestJoinAfterBeginRejected(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 20)
	if err := g.Join("p3", 30); !errors.Is(err, impl.ErrAlreadyStarted) {
		t.Fatalf("join after begin error = %v, want ErrAlreadyStarted", err)
	}
}

func TestBeginOrdersByInitiative(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	turn := beginDuel(t, g, 10, 30)
	if turn.Player != "p2" {
		t.Fatalf("first player = %q, want p2 with the higher initiative", turn.Player)
	}
	if turn.Round != 1 {
		t.Fatalf("round = %d, want 1", turn.Round)
	}
	if turn.ActionsLeft != 1 {
		t.Fatalf("actions left = %d, want 1", turn.ActionsLeft)
	}
	order := g.TurnOrder()
	if len(order) != 2 || order[0] != "p2" || order[1] != "p1" {
		t.Fatalf("turn order = %v, want [p2 p1]", order)
	}
}

func TestBeginRequiresTwoPlayers(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	if err := g.Join("p1", 10); err != nil {
		t.Fatalf("join p1: unexpected error %v", err)
	}
	if _, err := g.Begin(1000); !errors.Is(err, impl.ErrNotEnoughPlayers) {
		t.Fatalf("begin with one player error = %v, want ErrNotEnoughPlayers", err)
	}
}

func TestActOutOfTurnRejected(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	if _, err := g.Act("p1", impl.Action{Kind: "attack", Target: "p2", Power: 40}); !errors.Is(err, impl.ErrOutOfTurn) {
		t.Fatalf("out of turn error = %v, want ErrOutOfTurn", err)
	}
	if got := g.HP("p2"); got != 100 {
		t.Fatalf("p2 hp after illegal action = %d, want 100", got)
	}
}

func TestAttackReducesHPAndPassesTurn(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	turn, err := g.Act("p2", impl.Action{Kind: "attack", Target: "p1", Power: 30})
	if err != nil {
		t.Fatalf("attack: unexpected error %v", err)
	}
	if got := g.HP("p1"); got != 70 {
		t.Fatalf("p1 hp = %d, want 70", got)
	}
	if turn.Player != "p1" || turn.Round != 1 || turn.ActionsLeft != 1 {
		t.Fatalf("next turn = %+v, want p1 in round 1 with 1 action", turn)
	}
}

func TestActionPointsTrackedWithinTurn(t *testing.T) {
	opts := duelOptions()
	opts.ActionPoints = 2
	g := impl.NewDuelRoundGuard(opts)
	beginDuel(t, g, 10, 30)
	turn, err := g.Act("p2", impl.Action{Kind: "attack", Target: "p1", Power: 10})
	if err != nil {
		t.Fatalf("attack: unexpected error %v", err)
	}
	if turn.Player != "p2" || turn.ActionsLeft != 1 {
		t.Fatalf("turn after first action = %+v, want p2 with 1 action left", turn)
	}
	if got := g.ActionPoints("p2"); got != 1 {
		t.Fatalf("p2 action points = %d, want 1", got)
	}
	if got := g.HP("p1"); got != 90 {
		t.Fatalf("p1 hp = %d, want 90", got)
	}
}

func TestKillEndsDuelWithKO(t *testing.T) {
	opts := duelOptions()
	opts.MaxHP = 40
	g := impl.NewDuelRoundGuard(opts)
	beginDuel(t, g, 10, 30)
	if _, err := g.Act("p2", impl.Action{Kind: "attack", Target: "p1", Power: 50}); err != nil {
		t.Fatalf("attack: unexpected error %v", err)
	}
	if got := g.HP("p1"); got != 0 {
		t.Fatalf("p1 hp = %d, want 0 after the killing blow", got)
	}
	res, ok := g.Result()
	if !ok {
		t.Fatalf("result must be available after the kill")
	}
	if res.Winner != "p2" || res.Loser != "p1" || res.Reason != "ko" || res.Rounds != 1 {
		t.Fatalf("result = %+v, want p2 beats p1 by ko in round 1", res)
	}
}

func TestActAfterEndRejected(t *testing.T) {
	opts := duelOptions()
	opts.MaxHP = 40
	g := impl.NewDuelRoundGuard(opts)
	beginDuel(t, g, 10, 30)
	if _, err := g.Act("p2", impl.Action{Kind: "attack", Target: "p1", Power: 50}); err != nil {
		t.Fatalf("attack: unexpected error %v", err)
	}
	if _, err := g.Act("p1", impl.Action{Kind: "wait"}); !errors.Is(err, impl.ErrDuelOver) {
		t.Fatalf("act after end error = %v, want ErrDuelOver", err)
	}
}

func TestCooldownBlocksRepeatedSkill(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	if _, err := g.Act("p2", impl.Action{Kind: "skill", Target: "p1", Power: 1, Cooldown: 2}); err != nil {
		t.Fatalf("first skill: unexpected error %v", err)
	}
	if _, err := g.Act("p1", impl.Action{Kind: "wait"}); err != nil {
		t.Fatalf("p1 wait: unexpected error %v", err)
	}
	if _, err := g.Act("p2", impl.Action{Kind: "skill", Target: "p1", Power: 1, Cooldown: 2}); !errors.Is(err, impl.ErrOnCooldown) {
		t.Fatalf("cooldown error = %v, want ErrOnCooldown", err)
	}
	if got := g.HP("p1"); got != 99 {
		t.Fatalf("p1 hp after blocked skill = %d, want 99", got)
	}
}

func TestTimeoutUsesAutoAction(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	action, err := g.Timeout(1200)
	if err != nil {
		t.Fatalf("timeout: unexpected error %v", err)
	}
	if action.Kind != "wait" {
		t.Fatalf("auto action = %q, want wait", action.Kind)
	}
	if got := g.ActionPoints("p1"); got != 1 {
		t.Fatalf("p1 action points after timeout = %d, want the turn to have passed", got)
	}
	if got := g.ActionPoints("p2"); got != 0 {
		t.Fatalf("p2 action points after timeout = %d, want 0", got)
	}
}

func TestSurrenderSettlesForOpponent(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	if err := g.Surrender("p1"); err != nil {
		t.Fatalf("surrender: unexpected error %v", err)
	}
	res, ok := g.Result()
	if !ok {
		t.Fatalf("result must be available after surrender")
	}
	if res.Winner != "p2" || res.Loser != "p1" || res.Reason != "surrender" {
		t.Fatalf("result = %+v, want p2 wins by surrender", res)
	}
	again, ok := g.Result()
	if !ok || again != res {
		t.Fatalf("repeated result = %+v, want the same %+v", again, res)
	}
}

func TestDisconnectSettlesWithOwnReason(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	if err := g.Disconnect("p2"); err != nil {
		t.Fatalf("disconnect: unexpected error %v", err)
	}
	res, ok := g.Result()
	if !ok {
		t.Fatalf("result must be available after disconnect")
	}
	if res.Winner != "p1" || res.Loser != "p2" || res.Reason != "disconnect" {
		t.Fatalf("result = %+v, want p1 wins by disconnect", res)
	}
}

func TestSnapshotRestoreKeepsDuelState(t *testing.T) {
	g := impl.NewDuelRoundGuard(duelOptions())
	beginDuel(t, g, 10, 30)
	if _, err := g.Act("p2", impl.Action{Kind: "attack", Target: "p1", Power: 25}); err != nil {
		t.Fatalf("attack: unexpected error %v", err)
	}
	if _, ok := g.Result(); ok {
		t.Fatalf("a running duel must not report a result")
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := impl.NewDuelRoundGuard(impl.Options{})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if got := restored.HP("p1"); got != 75 {
		t.Fatalf("restored p1 hp = %d, want 75", got)
	}
	if got := restored.Round(); got != 1 {
		t.Fatalf("restored round = %d, want 1", got)
	}
	order := restored.TurnOrder()
	if len(order) != 2 || order[0] != "p2" || order[1] != "p1" {
		t.Fatalf("restored order = %v, want [p2 p1]", order)
	}
	if got := restored.ActionPoints("p1"); got != 1 {
		t.Fatalf("restored p1 action points = %d, want 1", got)
	}
	if err := restored.Restore([]byte("junk")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore error = %v, want ErrBadSnapshot", err)
	}
}
