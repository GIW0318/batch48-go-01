package task

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-b-12"
)

func quarantineOptions() impl.Options {
	return impl.Options{
		HoldMs:           30000,
		GraceMs:          5000,
		MaxCases:         8,
		MaxEvidenceFiles: 4,
		Resolve: func(pid int) (impl.ProcRef, bool) {
			if pid <= 0 || pid > 500 {
				return impl.ProcRef{}, false
			}
			return impl.ProcRef{Pid: pid, StartTime: int64(pid) * 100, Comm: "miner"}, true
		},
		Inspect: func(pid int) []string {
			base := "/proc/" + strconv.Itoa(pid)
			return []string{base + "/fd/9", "/tmp/stage", base + "/fd/9", base + "/fd/1"}
		},
	}
}

func openStoppedCase(t *testing.T, g *impl.QuarantineGuard, pid int) impl.Case {
	t.Helper()
	c, err := g.Quarantine(pid, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine pid %d: %v", pid, err)
	}
	if _, err := g.Collect(c.ID); err != nil {
		t.Fatalf("collect case %s: %v", c.ID, err)
	}
	got, ok := g.Lookup(c.ID)
	if !ok {
		t.Fatalf("case %s missing after collect", c.ID)
	}
	return got
}

func TestQuarantineRejectsIncompleteRequest(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if _, err := g.Quarantine(0, "cpu spike", "edr"); !errors.Is(err, impl.ErrBadCase) {
		t.Fatalf("pid 0 = %v, want ErrBadCase", err)
	}
	if _, err := g.Quarantine(7, "   ", "edr"); !errors.Is(err, impl.ErrBadCase) {
		t.Fatalf("blank reason = %v, want ErrBadCase", err)
	}
	if _, err := g.Quarantine(7, "cpu spike", " "); !errors.Is(err, impl.ErrBadCase) {
		t.Fatalf("blank operator = %v, want ErrBadCase", err)
	}
}

func TestQuarantineRejectsUnresolvableProcess(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if _, err := g.Quarantine(900, "cpu spike", "edr"); !errors.Is(err, impl.ErrProcUnavailable) {
		t.Fatalf("unresolvable pid = %v, want ErrProcUnavailable", err)
	}
}

func TestQuarantineOpensPendingCase(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c, err := g.Quarantine(7, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if c.ID == "" {
		t.Fatalf("case id must not be empty")
	}
	if c.State != impl.StatePending {
		t.Fatalf("state = %q, want %q", c.State, impl.StatePending)
	}
	if c.Pid != 7 || c.StartTime != 700 || c.Reason != "cpu spike" {
		t.Fatalf("case identity = %+v", c)
	}
	if len(c.Sources) != 1 || c.Sources[0] != "edr" {
		t.Fatalf("case sources = %v, want [edr]", c.Sources)
	}
	if _, ok := g.Active(7); !ok {
		t.Fatalf("pending case must be active for pid 7")
	}
}

func TestQuarantineHonorsCaseLimit(t *testing.T) {
	opts := quarantineOptions()
	opts.MaxCases = 1
	g := impl.NewQuarantineGuard(opts)
	if _, err := g.Quarantine(7, "cpu spike", "edr"); err != nil {
		t.Fatalf("first quarantine: %v", err)
	}
	if _, err := g.Quarantine(8, "net spike", "edr"); !errors.Is(err, impl.ErrQuarantineFull) {
		t.Fatalf("case over limit = %v, want ErrQuarantineFull", err)
	}
}

func TestCollectPublishesSortedEvidence(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c, err := g.Quarantine(7, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	ev, err := g.Collect(c.ID)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := []string{"/proc/7/fd/1", "/proc/7/fd/9", "/tmp/stage"}
	if strings.Join(ev.Files, ",") != strings.Join(want, ",") {
		t.Fatalf("evidence files = %v, want %v", ev.Files, want)
	}
	if ev.Pid != 7 || ev.StartTime != 700 {
		t.Fatalf("evidence identity = %+v", ev)
	}
	if ev.Digest == "" {
		t.Fatalf("evidence digest must not be empty")
	}
	got, ok := g.Lookup(c.ID)
	if !ok {
		t.Fatalf("case missing after collect")
	}
	if got.State != impl.StateStopped {
		t.Fatalf("state after collect = %q, want %q", got.State, impl.StateStopped)
	}
	if got.Evidence.Digest != ev.Digest {
		t.Fatalf("stored evidence digest = %q, want %q", got.Evidence.Digest, ev.Digest)
	}
}

func TestCollectRejectsRepeatedCollection(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c, err := g.Quarantine(7, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if _, err := g.Collect(c.ID); err != nil {
		t.Fatalf("first collect: %v", err)
	}
	if _, err := g.Collect(c.ID); !errors.Is(err, impl.ErrStateConflict) {
		t.Fatalf("second collect = %v, want ErrStateConflict", err)
	}
}

func TestCollectUnknownCaseFails(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if _, err := g.Collect("q-404"); !errors.Is(err, impl.ErrCaseNotFound) {
		t.Fatalf("collect unknown = %v, want ErrCaseNotFound", err)
	}
}

func TestReleaseRequiresStoppedCase(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c, err := g.Quarantine(7, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if err := g.Release(c.ID, "lead"); !errors.Is(err, impl.ErrStateConflict) {
		t.Fatalf("release pending = %v, want ErrStateConflict", err)
	}
	if _, err := g.Collect(c.ID); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := g.Release(c.ID, "lead"); err != nil {
		t.Fatalf("release stopped: %v", err)
	}
	got, ok := g.Lookup(c.ID)
	if !ok {
		t.Fatalf("case missing after release")
	}
	if got.State != impl.StateReleased || got.ClosedBy != "lead" {
		t.Fatalf("released case = %+v", got)
	}
}

func TestReleaseUnknownCaseFails(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if err := g.Release("q-404", "lead"); !errors.Is(err, impl.ErrCaseNotFound) {
		t.Fatalf("release unknown = %v, want ErrCaseNotFound", err)
	}
}

func TestTerminateRequiresStoppedCase(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c, err := g.Quarantine(7, "cpu spike", "edr")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if err := g.Terminate(c.ID, "lead"); !errors.Is(err, impl.ErrStateConflict) {
		t.Fatalf("terminate pending = %v, want ErrStateConflict", err)
	}
	if _, err := g.Collect(c.ID); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := g.Terminate(c.ID, "lead"); err != nil {
		t.Fatalf("terminate stopped: %v", err)
	}
	got, ok := g.Lookup(c.ID)
	if !ok {
		t.Fatalf("case missing after terminate")
	}
	if got.State != impl.StateTerminated || got.ClosedBy != "lead" {
		t.Fatalf("terminated case = %+v", got)
	}
}

func TestExpireEscalatesOneTierPerCall(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c := openStoppedCase(t, g, 7)
	if c.Deadline <= 0 {
		t.Fatalf("stopped case must carry a deadline, got %d", c.Deadline)
	}
	first, err := g.Expire(c.Deadline)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expired cases = %d, want 1", len(first))
	}
	if first[0].Tier != 1 || first[0].State != impl.StateStopped {
		t.Fatalf("first expiry = tier %d state %q", first[0].Tier, first[0].State)
	}
	again, err := g.Expire(c.Deadline)
	if err != nil {
		t.Fatalf("second expire: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("same instant must not escalate twice, got %d cases", len(again))
	}
}

func TestExpireTerminatesAfterGrace(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	c := openStoppedCase(t, g, 7)
	if _, err := g.Expire(c.Deadline); err != nil {
		t.Fatalf("first expire: %v", err)
	}
	final, err := g.Expire(c.Deadline + 5000)
	if err != nil {
		t.Fatalf("expire past grace: %v", err)
	}
	if len(final) != 1 {
		t.Fatalf("cases touched after grace = %d, want 1", len(final))
	}
	if final[0].State != impl.StateTerminated || final[0].Tier != 2 {
		t.Fatalf("final state = %q tier %d, want terminated tier 2", final[0].State, final[0].Tier)
	}
	if _, ok := g.Active(7); ok {
		t.Fatalf("terminated case must not stay active")
	}
}

func TestCasesFiltersByState(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if _, err := g.Quarantine(7, "cpu spike", "edr"); err != nil {
		t.Fatalf("pending case: %v", err)
	}
	stopped := openStoppedCase(t, g, 8)
	released := openStoppedCase(t, g, 9)
	if err := g.Release(released.ID, "lead"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := len(g.Cases("")); got != 3 {
		t.Fatalf("all cases = %d, want 3", got)
	}
	if got := g.Cases(impl.StatePending); len(got) != 1 || got[0].Pid != 7 {
		t.Fatalf("pending cases = %+v", got)
	}
	if got := g.Cases(impl.StateStopped); len(got) != 1 || got[0].ID != stopped.ID {
		t.Fatalf("stopped cases = %+v", got)
	}
}

func TestSnapshotRestoreKeepsCaseAndEvidence(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	stopped := openStoppedCase(t, g, 7)
	released := openStoppedCase(t, g, 8)
	if err := g.Release(released.ID, "lead"); err != nil {
		t.Fatalf("release: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewQuarantineGuard(quarantineOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(other.Cases("")); got != 2 {
		t.Fatalf("restored cases = %d, want 2", got)
	}
	got, ok := other.Lookup(stopped.ID)
	if !ok {
		t.Fatalf("restored guard lost stopped case")
	}
	if got.State != impl.StateStopped || len(got.Evidence.Files) != 3 {
		t.Fatalf("restored case = state %q files %v", got.State, got.Evidence.Files)
	}
	if again, ok := other.Lookup(released.ID); !ok || again.State != impl.StateReleased {
		t.Fatalf("restored released case = %+v ok %v", again, ok)
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	g := impl.NewQuarantineGuard(quarantineOptions())
	if _, err := g.Quarantine(7, "cpu spike", "edr"); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadPayload) {
		t.Fatalf("corrupt restore = %v, want ErrBadPayload", err)
	}
	if got := len(g.Cases("")); got != 1 {
		t.Fatalf("rejected restore must keep live cases, got %d", got)
	}
}
