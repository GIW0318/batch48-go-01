package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-15"
)

func newNegotiator() *impl.InputDelayNegotiator {
	return impl.NewInputDelayNegotiator(8)
}

func mustPropose(t *testing.T, n *impl.InputDelayNegotiator, peer string, frames int) {
	t.Helper()
	if err := n.Propose(peer, frames); err != nil {
		t.Fatalf("propose %d for %s: %v", frames, peer, err)
	}
}

func mustCommit(t *testing.T, n *impl.InputDelayNegotiator, frame uint64, want int) {
	t.Helper()
	got, err := n.Commit(frame)
	if err != nil {
		t.Fatalf("commit frame %d: %v", frame, err)
	}
	if got != want {
		t.Fatalf("commit frame %d returned %d, want %d", frame, got, want)
	}
}

func TestProposeRejectsEmptyPeerID(t *testing.T) {
	n := newNegotiator()
	err := n.Propose("", 3)
	if !errors.Is(err, impl.ErrInvalidPeer) {
		t.Fatalf("proposal without peer id: got %v, want ErrInvalidPeer", err)
	}
	if got := n.Agreed(); got != 0 {
		t.Fatalf("agreed = %d after the rejected proposal, want 0", got)
	}
}

func TestProposeRejectsNegativeFrames(t *testing.T) {
	n := newNegotiator()
	err := n.Propose("peer-a", -1)
	if !errors.Is(err, impl.ErrInvalidProposal) {
		t.Fatalf("negative proposal: got %v, want ErrInvalidProposal", err)
	}
	if _, ok := n.Proposal("peer-a"); ok {
		t.Fatalf("rejected proposal must not be stored")
	}
}

func TestProposeAboveMaxDelayRejected(t *testing.T) {
	n := newNegotiator()
	err := n.Propose("peer-a", 20)
	if !errors.Is(err, impl.ErrDelayTooLarge) {
		t.Fatalf("proposal above the 8 frame cap: got %v, want ErrDelayTooLarge", err)
	}
	if got := n.MaxDelay(); got != 8 {
		t.Fatalf("max delay = %d, want 8", got)
	}
	mustPropose(t, n, "peer-a", 8)
	if got := n.Agreed(); got != 8 {
		t.Fatalf("agreed = %d at the cap, want 8", got)
	}
}

func TestAgreedTakesLargerProposal(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 5)
	mustPropose(t, n, "peer-b", 3)
	if got := n.Agreed(); got != 5 {
		t.Fatalf("agreed = %d, want the larger proposal 5", got)
	}
	if got, ok := n.Proposal("peer-b"); !ok || got != 3 {
		t.Fatalf("proposal of peer-b = %d ok=%v, want 3", got, ok)
	}
	if _, ok := n.Proposal("peer-z"); ok {
		t.Fatalf("unknown peer must not report a proposal")
	}
	peers := n.Peers()
	if len(peers) != 2 || peers[0] != "peer-a" || peers[1] != "peer-b" {
		t.Fatalf("peers = %v, want ascending [peer-a peer-b]", peers)
	}
}

func TestCommitAppliesAgreedValueAtFrameBoundary(t *testing.T) {
	n := newNegotiator()
	if got := n.Current(); got != 0 {
		t.Fatalf("current before the first commit = %d, want 0", got)
	}
	mustPropose(t, n, "peer-a", 3)
	if got := n.Current(); got != 0 {
		t.Fatalf("current = %d before the commit, want the proposal to wait for a frame boundary", got)
	}
	mustCommit(t, n, 10, 3)
	if got := n.Current(); got != 3 {
		t.Fatalf("current after the commit = %d, want 3", got)
	}
}

func TestCommitRejectsFrameRegression(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 10, 2)
	if _, err := n.Commit(10); !errors.Is(err, impl.ErrCommitRegressed) {
		t.Fatalf("commit of the same frame: got %v, want ErrCommitRegressed", err)
	}
	if _, err := n.Commit(9); !errors.Is(err, impl.ErrCommitRegressed) {
		t.Fatalf("commit of an older frame: got %v, want ErrCommitRegressed", err)
	}
	if got := n.Current(); got != 2 {
		t.Fatalf("current = %d after the rejected commits, want 2", got)
	}
}

func TestUpliftProposalAppliesOnNextCommit(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 3)
	mustCommit(t, n, 10, 3)
	mustPropose(t, n, "peer-a", 6)
	mustCommit(t, n, 11, 6)
	if got := n.Current(); got != 6 {
		t.Fatalf("current after the uplift = %d, want 6", got)
	}
}

func TestDowngradeWaitsForRecoveryWindow(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 6)
	mustCommit(t, n, 10, 6)
	n.SetRecoveryWindow(2)
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 11, 6)
	mustCommit(t, n, 12, 5)
	mustCommit(t, n, 13, 5)
	mustCommit(t, n, 14, 4)
	if got := n.Current(); got != 4 {
		t.Fatalf("current after four commits = %d, want the stepped down 4", got)
	}
}

func TestStallBelowThresholdIgnored(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 10, 2)
	n.ObserveStall(11, 40, impl.StallInput)
	if got := n.StallCount(impl.StallInput); got != 0 {
		t.Fatalf("stall count = %d for a 40ms stall, want 0 below the 100ms threshold", got)
	}
	if got := n.Current(); got != 2 {
		t.Fatalf("current = %d after a short stall, want the unchanged 2", got)
	}
}

func TestInputStallRaisesDelayByOne(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 10, 2)
	n.ObserveStall(11, 250, impl.StallInput)
	if got := n.StallCount(impl.StallInput); got != 1 {
		t.Fatalf("input stall count = %d, want 1", got)
	}
	if got := n.Current(); got != 3 {
		t.Fatalf("current = %d after an input stall, want one frame of uplift", got)
	}
}

func TestStallCountsTrackedByCause(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 10, 2)
	n.ObserveStall(11, 300, impl.StallNetwork)
	n.ObserveStall(12, 500, impl.StallRender)
	if got := n.StallCount(impl.StallNetwork); got != 1 {
		t.Fatalf("network stall count = %d, want 1", got)
	}
	if got := n.StallCount(impl.StallRender); got != 1 {
		t.Fatalf("render stall count = %d, want 1", got)
	}
	if got := n.StallCount(impl.StallInput); got != 0 {
		t.Fatalf("input stall count = %d, want 0", got)
	}
}

func TestUpliftCappedAtMaxDelay(t *testing.T) {
	n := impl.NewInputDelayNegotiator(4)
	mustPropose(t, n, "peer-a", 4)
	mustCommit(t, n, 10, 4)
	for i := 0; i < 3; i++ {
		n.ObserveStall(uint64(11+i), 300, impl.StallInput)
	}
	if got := n.Current(); got != 4 {
		t.Fatalf("current = %d after repeated input stalls, want the cap 4", got)
	}
	if got := n.StallCount(impl.StallInput); got != 3 {
		t.Fatalf("input stall count = %d, want all 3 attributed", got)
	}
}

func TestSetRecoveryWindowClampsToAtLeastOne(t *testing.T) {
	n := newNegotiator()
	n.SetRecoveryWindow(0)
	if got := n.RecoveryWindow(); got != 1 {
		t.Fatalf("recovery window = %d after setting 0, want 1", got)
	}
	n.SetRecoveryWindow(4)
	if got := n.RecoveryWindow(); got != 4 {
		t.Fatalf("recovery window = %d after setting 4, want 4", got)
	}
}

func TestConfirmRejectsRegressionAndAhead(t *testing.T) {
	n := newNegotiator()
	mustPropose(t, n, "peer-a", 2)
	mustCommit(t, n, 10, 2)
	if err := n.Confirm(11); !errors.Is(err, impl.ErrConfirmAhead) {
		t.Fatalf("confirm beyond the committed frame: got %v, want ErrConfirmAhead", err)
	}
	if err := n.Confirm(10); err != nil {
		t.Fatalf("confirm of the committed frame: %v", err)
	}
	if err := n.Confirm(9); !errors.Is(err, impl.ErrConfirmRegressed) {
		t.Fatalf("confirm of an older frame: got %v, want ErrConfirmRegressed", err)
	}
	if err := n.Confirm(10); err != nil {
		t.Fatalf("repeated confirm of the same frame: %v", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	n := newNegotiator()
	n.SetRecoveryWindow(2)
	mustPropose(t, n, "peer-a", 3)
	mustCommit(t, n, 10, 3)
	n.ObserveStall(11, 300, impl.StallInput)
	if got := n.Current(); got != 4 {
		t.Fatalf("current after the input stall = %d, want 4", got)
	}
	data, err := n.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	mustPropose(t, n, "peer-b", 7)
	mustCommit(t, n, 12, 7)
	if err := n.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := n.Current(); got != 4 {
		t.Fatalf("current after restore = %d, want 4", got)
	}
	if got := n.Agreed(); got != 3 {
		t.Fatalf("agreed after restore = %d, want 3", got)
	}
	if got := n.StallCount(impl.StallInput); got != 1 {
		t.Fatalf("input stall count after restore = %d, want 1", got)
	}
	if got := n.RecoveryWindow(); got != 2 {
		t.Fatalf("recovery window after restore = %d, want 2", got)
	}
	if err := n.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of garbage: got %v, want ErrBadSnapshot", err)
	}
	if got := n.Current(); got != 4 {
		t.Fatalf("current after rejected restore = %d, want the previous 4", got)
	}
}
