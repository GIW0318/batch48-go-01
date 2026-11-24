package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-02"
)

func testPlayers() []string {
	return []string{"player-a", "player-b"}
}

func testConfig(wait, budget int) impl.FrameSyncConfig {
	return impl.FrameSyncConfig{MaxWaitFrames: wait, CatchUpPerTick: budget}
}

func submitBoth(t *testing.T, m *impl.FrameSyncMachine, frame uint64, a, b string) {
	t.Helper()
	if err := m.Submit("player-a", frame, impl.Input{Seq: int(frame), Action: a}); err != nil {
		t.Fatalf("submit player-a frame %d failed: %v", frame, err)
	}
	if err := m.Submit("player-b", frame, impl.Input{Seq: int(frame), Action: b}); err != nil {
		t.Fatalf("submit player-b frame %d failed: %v", frame, err)
	}
}

func TestNewMachineValidatesRosterAndBudgets(t *testing.T) {
	if _, err := impl.NewFrameSyncMachine(nil, testConfig(3, 1)); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("empty roster must be rejected with ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewFrameSyncMachine([]string{"player-a", ""}, testConfig(3, 1)); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("blank player must be rejected with ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewFrameSyncMachine([]string{"player-a", "player-a"}, testConfig(3, 1)); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("duplicate player must be rejected with ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(0, 1)); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero wait limit must be rejected with ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 0)); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero catch up budget must be rejected with ErrInvalidConfig, got %v", err)
	}
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("valid config must build a machine, got %v", err)
	}
	if m.Frame() != 0 {
		t.Fatalf("fresh machine frame = %d, want 0", m.Frame())
	}
	if m.WaitDepth() != 0 {
		t.Fatalf("fresh machine wait depth = %d, want 0", m.WaitDepth())
	}
}

func TestTickAdvancesAFullyCollectedFrame(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, m, 1, "move", "fire")
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 1 {
		t.Fatalf("advanced = %d, want 1", result.Advanced)
	}
	if result.Frame != 1 || m.Frame() != 1 {
		t.Fatalf("frame after tick = %d / %d, want 1", result.Frame, m.Frame())
	}
	if result.Waited {
		t.Fatalf("a complete frame must not be reported as a wait")
	}
	checksum, ok := m.ChecksumAt(1)
	if !ok {
		t.Fatalf("advanced frame must have a checksum")
	}
	if checksum == 0 {
		t.Fatalf("checksum of frame 1 must not be zero")
	}
	if m.WaitDepth() != 0 {
		t.Fatalf("wait depth after advancing = %d, want 0", m.WaitDepth())
	}
}

func TestTickWaitsForTheMissingPlayer(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := m.Submit("player-a", 1, impl.Input{Seq: 1, Action: "move"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 0 || result.Frame != 0 {
		t.Fatalf("tick with a missing player advanced %d frames to %d, want 0", result.Advanced, result.Frame)
	}
	if !result.Waited {
		t.Fatalf("a missing input must be reported as a wait")
	}
	if m.WaitDepth() != 1 {
		t.Fatalf("wait depth = %d, want 1 incomplete frame", m.WaitDepth())
	}
	if err := m.Submit("player-b", 1, impl.Input{Seq: 1, Action: "fire"}); err != nil {
		t.Fatalf("second submit must succeed, got %v", err)
	}
	result, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 1 || m.Frame() != 1 {
		t.Fatalf("completed frame must advance, advanced %d frame %d", result.Advanced, m.Frame())
	}
	if m.WaitDepth() != 0 {
		t.Fatalf("wait depth after the frame completed = %d, want 0", m.WaitDepth())
	}
}

func TestChecksumFollowsTheCollectedInputs(t *testing.T) {
	first, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	second, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	third, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, first, 1, "move", "fire")
	submitBoth(t, second, 1, "move", "dash")
	submitBoth(t, third, 1, "move", "fire")
	for _, m := range []*impl.FrameSyncMachine{first, second, third} {
		if _, err := m.Tick(time.Now()); err != nil {
			t.Fatalf("tick must succeed, got %v", err)
		}
	}
	a, ok := first.ChecksumAt(1)
	if !ok {
		t.Fatalf("frame 1 must have a checksum")
	}
	b, ok := second.ChecksumAt(1)
	if !ok {
		t.Fatalf("frame 1 must have a checksum")
	}
	c, ok := third.ChecksumAt(1)
	if !ok {
		t.Fatalf("frame 1 must have a checksum")
	}
	if a == b {
		t.Fatalf("different input sets produced the same checksum %d", a)
	}
	if a != c {
		t.Fatalf("identical input sets produced different checksums %d and %d", a, c)
	}
	if _, ok := first.ChecksumAt(2); ok {
		t.Fatalf("frame 2 is not applied yet, checksum must not be readable")
	}
}

func TestChecksumHistoryIgnoresArrivalOrder(t *testing.T) {
	ordered, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	shuffled, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	actionsA := []string{"action-a", "action-b", "action-c"}
	actionsB := []string{"action-x", "action-y", "action-z"}
	for frame := uint64(1); frame <= 3; frame++ {
		submitBoth(t, ordered, frame, actionsA[frame-1], actionsB[frame-1])
	}
	submitBoth(t, shuffled, 3, actionsA[2], actionsB[2])
	submitBoth(t, shuffled, 1, actionsA[0], actionsB[0])
	submitBoth(t, shuffled, 2, actionsA[1], actionsB[1])
	for i := 0; i < 3; i++ {
		if _, err := ordered.Tick(time.Now()); err != nil {
			t.Fatalf("ordered tick must succeed, got %v", err)
		}
		if _, err := shuffled.Tick(time.Now()); err != nil {
			t.Fatalf("shuffled tick must succeed, got %v", err)
		}
	}
	for frame := uint64(1); frame <= 3; frame++ {
		want, ok := ordered.ChecksumAt(frame)
		if !ok {
			t.Fatalf("ordered machine must have checksum for frame %d", frame)
		}
		got, ok := shuffled.ChecksumAt(frame)
		if !ok {
			t.Fatalf("shuffled machine must have checksum for frame %d", frame)
		}
		if got != want {
			t.Fatalf("frame %d checksum depends on arrival order: %d vs %d", frame, got, want)
		}
	}
}

func TestTickHonoursTheCatchUpBudget(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 2))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	for frame := uint64(1); frame <= 4; frame++ {
		submitBoth(t, m, frame, "move", "fire")
	}
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 2 || m.Frame() != 2 {
		t.Fatalf("first tick advanced %d to frame %d, want 2 frames up to 2", result.Advanced, m.Frame())
	}
	result, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 2 || m.Frame() != 4 {
		t.Fatalf("second tick advanced %d to frame %d, want 2 frames up to 4", result.Advanced, m.Frame())
	}
	if result.Waited {
		t.Fatalf("a tick that advanced frames must not be reported as a wait")
	}
}

func TestPauseStopsAdvanceAndResumeRestarts(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, m, 1, "move", "fire")
	if err := m.Pause(); err != nil {
		t.Fatalf("pause must succeed, got %v", err)
	}
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick while paused must not error, got %v", err)
	}
	if result.Advanced != 0 || m.Frame() != 0 {
		t.Fatalf("paused machine advanced %d frames to %d, want 0", result.Advanced, m.Frame())
	}
	if err := m.Pause(); !errors.Is(err, impl.ErrAlreadyPaused) {
		t.Fatalf("pausing twice must report ErrAlreadyPaused, got %v", err)
	}
	if err := m.Resume(); err != nil {
		t.Fatalf("resume must succeed, got %v", err)
	}
	if err := m.Resume(); !errors.Is(err, impl.ErrNotPaused) {
		t.Fatalf("resuming a running machine must report ErrNotPaused, got %v", err)
	}
	result, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick after resume must succeed, got %v", err)
	}
	if result.Advanced != 1 || m.Frame() != 1 {
		t.Fatalf("tick after resume advanced %d to frame %d, want frame 1", result.Advanced, m.Frame())
	}
}

func TestCatchUpAppliesOnlyCollectedFrames(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if _, err := m.CatchUp(0); !errors.Is(err, impl.ErrInvalidBudget) {
		t.Fatalf("zero budget must report ErrInvalidBudget, got %v", err)
	}
	if _, err := m.CatchUp(-2); !errors.Is(err, impl.ErrInvalidBudget) {
		t.Fatalf("negative budget must report ErrInvalidBudget, got %v", err)
	}
	for frame := uint64(1); frame <= 3; frame++ {
		submitBoth(t, m, frame, "move", "fire")
	}
	advanced, err := m.CatchUp(5)
	if err != nil {
		t.Fatalf("catch up must succeed, got %v", err)
	}
	if advanced != 3 || m.Frame() != 3 {
		t.Fatalf("catch up advanced %d frames to %d, want 3 frames up to 3", advanced, m.Frame())
	}
	advanced, err = m.CatchUp(2)
	if err != nil {
		t.Fatalf("catch up without ready frames must not error, got %v", err)
	}
	if advanced != 0 || m.Frame() != 3 {
		t.Fatalf("catch up without ready frames advanced %d to %d, want 0", advanced, m.Frame())
	}
}

func TestCatchUpRejectsWhilePaused(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, m, 1, "move", "fire")
	if err := m.Pause(); err != nil {
		t.Fatalf("pause must succeed, got %v", err)
	}
	if _, err := m.CatchUp(1); !errors.Is(err, impl.ErrPaused) {
		t.Fatalf("catch up while paused must report ErrPaused, got %v", err)
	}
	if err := m.Resume(); err != nil {
		t.Fatalf("resume must succeed, got %v", err)
	}
	advanced, err := m.CatchUp(1)
	if err != nil {
		t.Fatalf("catch up after resume must succeed, got %v", err)
	}
	if advanced != 1 || m.Frame() != 1 {
		t.Fatalf("catch up after resume advanced %d to frame %d, want 1", advanced, m.Frame())
	}
}

func TestLongWaitDegradesInsteadOfStalling(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(2, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := m.Submit("player-a", 1, impl.Input{Seq: 1, Action: "move"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("first tick must succeed, got %v", err)
	}
	if result.Advanced != 0 || result.Degraded {
		t.Fatalf("first tick advanced %d degraded=%v, want a plain wait", result.Advanced, result.Degraded)
	}
	result, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("second tick must succeed, got %v", err)
	}
	if result.Advanced != 1 || m.Frame() != 1 {
		t.Fatalf("second tick advanced %d to frame %d, want the frame to be released", result.Advanced, m.Frame())
	}
	if !result.Degraded {
		t.Fatalf("a frame advanced without every input must be reported as degraded")
	}
	degraded := m.DegradedFrames()
	if len(degraded) != 1 || degraded[0] != 1 {
		t.Fatalf("degraded frames = %v, want [1]", degraded)
	}
	if _, ok := m.ChecksumAt(1); !ok {
		t.Fatalf("a degraded frame must still carry a checksum")
	}
}

func TestSubmitValidatesPlayerFrameAndDuplicates(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := m.Submit("player-x", 1, impl.Input{Seq: 1, Action: "move"}); !errors.Is(err, impl.ErrUnknownPlayer) {
		t.Fatalf("unknown player must report ErrUnknownPlayer, got %v", err)
	}
	if err := m.Submit("player-a", 0, impl.Input{Seq: 1, Action: "move"}); !errors.Is(err, impl.ErrInvalidFrame) {
		t.Fatalf("frame zero must report ErrInvalidFrame, got %v", err)
	}
	if err := m.Submit("player-a", 1, impl.Input{Seq: 1, Action: "move"}); err != nil {
		t.Fatalf("first submit must succeed, got %v", err)
	}
	if err := m.Submit("player-a", 1, impl.Input{Seq: 2, Action: "dash"}); !errors.Is(err, impl.ErrDuplicateInput) {
		t.Fatalf("second submit for the same frame must report ErrDuplicateInput, got %v", err)
	}
	if err := m.Submit("player-a", 2, impl.Input{Seq: 2, Action: "dash"}); err != nil {
		t.Fatalf("a later frame must be accepted, got %v", err)
	}
}

func TestWaitDepthCountsIncompleteFrames(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := m.Submit("player-a", 1, impl.Input{Seq: 1, Action: "move"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if err := m.Submit("player-b", 1, impl.Input{Seq: 1, Action: "fire"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if err := m.Submit("player-a", 2, impl.Input{Seq: 2, Action: "move"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if depth := m.WaitDepth(); depth != 1 {
		t.Fatalf("wait depth = %d, want 1 frame blocked on player-b", depth)
	}
	if err := m.Submit("player-b", 2, impl.Input{Seq: 2, Action: "fire"}); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if depth := m.WaitDepth(); depth != 0 {
		t.Fatalf("wait depth = %d, want 0 once every frame is complete", depth)
	}
	result, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if result.Advanced != 1 || m.Frame() != 1 {
		t.Fatalf("budget of one advanced %d to %d, want frame 1", result.Advanced, m.Frame())
	}
}

func TestSnapshotRestoreResumesFromTheSameFrame(t *testing.T) {
	original, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, original, 1, "move", "fire")
	submitBoth(t, original, 2, "move", "dash")
	for i := 0; i < 2; i++ {
		if _, err := original.Tick(time.Now()); err != nil {
			t.Fatalf("tick must succeed, got %v", err)
		}
	}
	if err := original.Submit("player-a", 3, impl.Input{Seq: 3, Action: "cast"}); err != nil {
		t.Fatalf("buffering frame 3 must succeed, got %v", err)
	}
	data, err := original.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}

	restored, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if restored.Frame() != 2 {
		t.Fatalf("restored frame = %d, want 2", restored.Frame())
	}
	for frame := uint64(1); frame <= 2; frame++ {
		want, _ := original.ChecksumAt(frame)
		got, ok := restored.ChecksumAt(frame)
		if !ok || got != want {
			t.Fatalf("restored checksum of frame %d = %d ok=%v, want %d", frame, got, ok, want)
		}
	}
	if err := restored.Submit("player-b", 3, impl.Input{Seq: 3, Action: "cast"}); err != nil {
		t.Fatalf("restored machine must accept the missing input, got %v", err)
	}
	if err := original.Submit("player-b", 3, impl.Input{Seq: 3, Action: "cast"}); err != nil {
		t.Fatalf("original machine must accept the missing input, got %v", err)
	}
	if _, err := original.Tick(time.Now()); err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if _, err := restored.Tick(time.Now()); err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if restored.Frame() != 3 {
		t.Fatalf("restored frame after resume = %d, want 3", restored.Frame())
	}
	want, _ := original.ChecksumAt(3)
	got, ok := restored.ChecksumAt(3)
	if !ok || got != want {
		t.Fatalf("restored checksum of frame 3 = %d ok=%v, want %d", got, ok, want)
	}
	if err := restored.Restore([]byte("[]")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
}

func TestSnapshotRestoreKeepsPausedState(t *testing.T) {
	original, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	submitBoth(t, original, 1, "move", "fire")
	if err := original.Pause(); err != nil {
		t.Fatalf("pause must succeed, got %v", err)
	}
	data, err := original.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	result, err := restored.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick must not error, got %v", err)
	}
	if result.Advanced != 0 || restored.Frame() != 0 {
		t.Fatalf("restored paused machine advanced %d to %d, want no advance", result.Advanced, restored.Frame())
	}
	if err := restored.Resume(); err != nil {
		t.Fatalf("resume after restore must succeed, got %v", err)
	}
	result, err = restored.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick after resume must succeed, got %v", err)
	}
	if result.Advanced != 1 || restored.Frame() != 1 {
		t.Fatalf("tick after resume advanced %d to %d, want frame 1", result.Advanced, restored.Frame())
	}
}

func TestConcurrentSubmitKeepsOneInputPerPlayerFrame(t *testing.T) {
	m, err := impl.NewFrameSyncMachine(testPlayers(), testConfig(3, 1))
	if err != nil {
		t.Fatalf("machine must build, got %v", err)
	}
	const writers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			submitErr := m.Submit("player-a", 1, impl.Input{Seq: n, Action: "action-" + string(rune('a'+n))})
			mu.Lock()
			errs = append(errs, submitErr)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	winners := 0
	duplicates := 0
	for _, submitErr := range errs {
		switch {
		case submitErr == nil:
			winners++
		case errors.Is(submitErr, impl.ErrDuplicateInput):
			duplicates++
		default:
			t.Fatalf("unexpected submit error %v", submitErr)
		}
	}
	if winners != 1 || duplicates != writers-1 {
		t.Fatalf("concurrent submit produced %d winners and %d duplicates, want 1 and %d", winners, duplicates, writers-1)
	}
	if err := m.Submit("player-b", 1, impl.Input{Seq: 1, Action: "fire"}); err != nil {
		t.Fatalf("the other player must still submit, got %v", err)
	}
	if _, err := m.Tick(time.Now()); err != nil {
		t.Fatalf("tick must succeed, got %v", err)
	}
	if m.Frame() != 1 {
		t.Fatalf("frame after the concurrent submits = %d, want 1", m.Frame())
	}
}
