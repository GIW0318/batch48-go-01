package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-11"
)

func restartOptions() impl.Options {
	return impl.Options{
		BaseDelayMs:     1000,
		MaxDelayMs:      8000,
		JitterPct:       10,
		MaxCrashes:      3,
		StableWindowMs:  600000,
		MaxAttempts:     16,
		ConfigExitCodes: []int{78, 126, 127},
	}
}

func jitterRange(base int64, pct int) (int64, int64) {
	span := base * int64(pct) / 100
	return base - span, base + span
}

func TestNormalExitLeavesNoRestart(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("edge-cache", impl.ExitStatus{Code: 0}, 1000)
	if err != nil {
		t.Fatalf("report normal exit: %v", err)
	}
	if d.Restart {
		t.Fatalf("code 0 must not schedule a restart: %+v", d)
	}
	if d.Outcome != impl.OutcomeNormal {
		t.Fatalf("outcome = %q, want %q", d.Outcome, impl.OutcomeNormal)
	}
	if got := len(g.History("edge-cache")); got != 1 {
		t.Fatalf("history entries = %d, want 1", got)
	}
}

func TestConfigFailureNeverBacksOff(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	for i := 0; i < 5; i++ {
		d, err := g.Report("sidecar", impl.ExitStatus{Code: 78}, int64(1000+i))
		if err != nil {
			t.Fatalf("report config failure %d: %v", i, err)
		}
		if d.Restart {
			t.Fatalf("config failure %d scheduled a restart: %+v", i, d)
		}
		if d.Outcome != impl.OutcomeConfigError {
			t.Fatalf("config failure %d outcome = %q", i, d.Outcome)
		}
	}
	if g.CircuitOpen("sidecar") {
		t.Fatalf("config failures must not open the breaker")
	}
	if got := g.Crashes("sidecar"); got != 0 {
		t.Fatalf("crash counter = %d, want 0", got)
	}
}

func TestOOMExitSchedulesRetry(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("batch", impl.ExitStatus{Code: 137, Signal: "KILL", OOMKilled: true}, 4000)
	if err != nil {
		t.Fatalf("report oom exit: %v", err)
	}
	if !d.Restart {
		t.Fatalf("oom exit must schedule a retry: %+v", d)
	}
	if d.Outcome != impl.OutcomeRetry {
		t.Fatalf("outcome = %q, want %q", d.Outcome, impl.OutcomeRetry)
	}
	if d.ResumeAt != 4000+d.DelayMs {
		t.Fatalf("resume at = %d, want 4000 plus delay %d", d.ResumeAt, d.DelayMs)
	}
	lo, hi := jitterRange(1000, 10)
	if d.DelayMs < lo || d.DelayMs > hi {
		t.Fatalf("first delay = %d, want within [%d,%d]", d.DelayMs, lo, hi)
	}
	if got := g.Crashes("batch"); got != 1 {
		t.Fatalf("crash counter = %d, want 1", got)
	}
}

func TestPlainNonZeroExitRetries(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("worker", impl.ExitStatus{Code: 3}, 2000)
	if err != nil {
		t.Fatalf("report exit: %v", err)
	}
	if !d.Restart || d.Outcome != impl.OutcomeRetry {
		t.Fatalf("plain non-zero exit decision = %+v", d)
	}
	lo, hi := jitterRange(1000, 10)
	if d.DelayMs < lo || d.DelayMs > hi {
		t.Fatalf("delay = %d, want within [%d,%d]", d.DelayMs, lo, hi)
	}
}

func TestSignalKillWithoutOOMDoesNotRestart(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("probe", impl.ExitStatus{Code: 137, Signal: "KILL"}, 3000)
	if err != nil {
		t.Fatalf("report kill: %v", err)
	}
	if d.Restart {
		t.Fatalf("external kill must not schedule a restart: %+v", d)
	}
	if d.Outcome != impl.OutcomeExternalKill {
		t.Fatalf("outcome = %q, want %q", d.Outcome, impl.OutcomeExternalKill)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	cases := []struct {
		attempts int
		want     int64
	}{
		{1, 1000},
		{2, 2000},
		{3, 4000},
		{4, 8000},
		{9, 8000},
	}
	for _, c := range cases {
		if got := g.Backoff(c.attempts); got != c.want {
			t.Fatalf("backoff(%d) = %d, want %d", c.attempts, got, c.want)
		}
	}
}

func TestDueReportsScheduleAndArrival(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("queue", impl.ExitStatus{Code: 137, OOMKilled: true}, 1000)
	if err != nil {
		t.Fatalf("report crash: %v", err)
	}
	at, ready := g.Due("queue", 1000)
	if at != d.ResumeAt {
		t.Fatalf("due time = %d, want %d", at, d.ResumeAt)
	}
	if ready {
		t.Fatalf("restart must not be ready before its scheduled time")
	}
	at, ready = g.Due("queue", d.ResumeAt)
	if at != d.ResumeAt || !ready {
		t.Fatalf("due at resume time = (%d,%v), want (%d,true)", at, ready, d.ResumeAt)
	}
}

func TestNormalExitCancelsScheduledRestart(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	d, err := g.Report("queue", impl.ExitStatus{Code: 137, OOMKilled: true}, 1000)
	if err != nil {
		t.Fatalf("report crash: %v", err)
	}
	if _, err := g.Report("queue", impl.ExitStatus{Code: 0}, d.ResumeAt-10); err != nil {
		t.Fatalf("report normal stop: %v", err)
	}
	at, ready := g.Due("queue", d.ResumeAt+5000)
	if ready {
		t.Fatalf("restart still due at %d after a normal stop", at)
	}
}

func TestBreakerOpensAtCrashLimit(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	for i, at := range []int64{1000, 3000, 8000} {
		d, err := g.Report("scan", impl.ExitStatus{Code: 1}, at)
		if err != nil {
			t.Fatalf("crash %d report: %v", i, err)
		}
		if !d.Restart {
			t.Fatalf("crash %d should still schedule a retry: %+v", i, d)
		}
	}
	if !g.CircuitOpen("scan") {
		t.Fatalf("breaker must be open after three consecutive crashes")
	}
	d, err := g.Report("scan", impl.ExitStatus{Code: 1}, 9000)
	if !errors.Is(err, impl.ErrBreakerOpen) {
		t.Fatalf("report while open = %v, want ErrBreakerOpen", err)
	}
	if d.Restart {
		t.Fatalf("no restart may be scheduled while the breaker is open")
	}
}

func TestOperatorResetClearsBreaker(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	for _, at := range []int64{1000, 3000, 8000} {
		if _, err := g.Report("scan", impl.ExitStatus{Code: 1}, at); err != nil {
			t.Fatalf("crash report: %v", err)
		}
	}
	if err := g.Reset("scan", "oncall-42"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if g.CircuitOpen("scan") {
		t.Fatalf("reset must close the breaker")
	}
	if got := g.Crashes("scan"); got != 0 {
		t.Fatalf("crash counter after reset = %d, want 0", got)
	}
	d, err := g.Report("scan", impl.ExitStatus{Code: 1}, 20000)
	if err != nil {
		t.Fatalf("report after reset: %v", err)
	}
	if !d.Restart {
		t.Fatalf("report after reset must schedule a restart: %+v", d)
	}
}

func TestResetValidatesContainerAndOperator(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	if err := g.Reset("ghost", "oncall"); !errors.Is(err, impl.ErrUnknownContainer) {
		t.Fatalf("reset unknown container = %v, want ErrUnknownContainer", err)
	}
	if _, err := g.Report("real", impl.ExitStatus{Code: 1}, 1000); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := g.Reset("real", "   "); !errors.Is(err, impl.ErrEmptyOperator) {
		t.Fatalf("reset without operator = %v, want ErrEmptyOperator", err)
	}
}

func TestHistoryKeepsChronologicalAttempts(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	for _, at := range []int64{500, 3000} {
		if _, err := g.Report("tail", impl.ExitStatus{Code: 1}, at); err != nil {
			t.Fatalf("report at %d: %v", at, err)
		}
	}
	h := g.History("tail")
	if len(h) != 2 {
		t.Fatalf("history entries = %d, want 2", len(h))
	}
	if h[0].At != 500 || h[1].At != 3000 {
		t.Fatalf("history times = [%d %d], want [500 3000]", h[0].At, h[1].At)
	}
	if h[0].Code != 1 || h[1].Code != 1 {
		t.Fatalf("history codes = [%d %d], want [1 1]", h[0].Code, h[1].Code)
	}
}

func TestSnapshotRestoreKeepsBreakerState(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	for _, at := range []int64{1000, 3000, 8000} {
		if _, err := g.Report("scan", impl.ExitStatus{Code: 1}, at); err != nil {
			t.Fatalf("crash report: %v", err)
		}
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewRestartPolicyGuard(restartOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !other.CircuitOpen("scan") {
		t.Fatalf("restored guard lost the open breaker")
	}
	if got := other.Crashes("scan"); got != 3 {
		t.Fatalf("restored crash counter = %d, want 3", got)
	}
	if got := len(other.History("scan")); got != 3 {
		t.Fatalf("restored history entries = %d, want 3", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	if err := g.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrCorruptSnapshot) {
		t.Fatalf("corrupt restore = %v, want ErrCorruptSnapshot", err)
	}
}

func TestConsecutiveCrashesEscalateBackoff(t *testing.T) {
	g := impl.NewRestartPolicyGuard(restartOptions())
	bases := []int64{1000, 2000, 4000}
	times := []int64{1000, 3000, 8000}
	for i, base := range bases {
		d, err := g.Report("cron", impl.ExitStatus{Code: 9}, times[i])
		if err != nil {
			t.Fatalf("crash %d report: %v", i, err)
		}
		lo, hi := jitterRange(base, 10)
		if d.DelayMs < lo || d.DelayMs > hi {
			t.Fatalf("crash %d delay = %d, want within [%d,%d]", i, d.DelayMs, lo, hi)
		}
	}
}
