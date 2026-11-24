package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-c-06"
)

func suppressor(window time.Duration) *impl.AlertSuppressor {
	return impl.NewAlertSuppressor(impl.SuppressorOptions{Window: window})
}

func alert(rule string, target string, port int) impl.Alert {
	return impl.Alert{ID: rule + "-" + target, RuleID: rule, Target: target, Port: port}
}

func TestFingerprintFormatIgnoresIDAndSeverity(t *testing.T) {
	s := suppressor(5 * time.Minute)
	first := impl.Alert{ID: "a-1", RuleID: "  Rule-9 ", Target: " web-01 ", Port: 443, Severity: 3}
	second := impl.Alert{ID: "a-2", RuleID: "rule-9", Target: "web-01", Port: 443, Severity: 5}
	got := s.Fingerprint(first)
	if got != "rule-9|web-01|443" {
		t.Fatalf("fingerprint = %q, want %q", got, "rule-9|web-01|443")
	}
	if other := s.Fingerprint(second); other != got {
		t.Fatalf("fingerprint of second alert = %q, want %q", other, got)
	}
}

func TestFingerprintSeparatesPortTargetAndRule(t *testing.T) {
	s := suppressor(time.Minute)
	base := s.Fingerprint(alert("r-1", "h-1", 22))
	if base == "" {
		t.Fatal("fingerprint must not be empty")
	}
	if s.Fingerprint(alert("r-1", "h-1", 80)) == base {
		t.Fatal("different port must produce a different fingerprint")
	}
	if s.Fingerprint(alert("r-1", "h-2", 22)) == base {
		t.Fatal("different target must produce a different fingerprint")
	}
	if s.Fingerprint(alert("r-2", "h-1", 22)) == base {
		t.Fatal("different rule must produce a different fingerprint")
	}
}

func TestFirstAlertEmittedThenDuplicateSuppressed(t *testing.T) {
	s := suppressor(5 * time.Minute)
	a := alert("r-1", "h-1", 22)
	now := time.Unix(1700000000, 0)
	suppressed, reason := s.Suppress(a, now)
	if suppressed {
		t.Fatal("first alert of a fingerprint must be emitted")
	}
	if reason != impl.ReasonEmitted {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonEmitted)
	}
	suppressed, reason = s.Suppress(a, now.Add(time.Minute))
	if !suppressed {
		t.Fatal("second alert inside the window must be suppressed")
	}
	if reason != impl.ReasonDuplicate {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonDuplicate)
	}
}

func TestSuppressionCountTracksDuplicates(t *testing.T) {
	s := suppressor(5 * time.Minute)
	a := alert("r-2", "h-3", 8443)
	now := time.Unix(1700000000, 0)
	fp := s.Fingerprint(a)
	s.Suppress(a, now)
	if got := s.Count(fp); got != 1 {
		t.Fatalf("count after emit = %d, want 1", got)
	}
	for i := 0; i < 3; i++ {
		s.Suppress(a, now.Add(time.Duration(i+1)*time.Second))
	}
	if got := s.Count(fp); got != 4 {
		t.Fatalf("count after three duplicates = %d, want 4", got)
	}
	if got := s.Stats().Suppressed; got != 3 {
		t.Fatalf("suppressed stat = %d, want 3", got)
	}
}

func TestWindowReopensAfterExpiry(t *testing.T) {
	s := suppressor(5 * time.Minute)
	a := alert("r-3", "h-4", 9000)
	now := time.Unix(1700000000, 0)
	fp := s.Fingerprint(a)
	s.Suppress(a, now)
	s.Suppress(a, now.Add(time.Minute))
	suppressed, reason := s.Suppress(a, now.Add(5*time.Minute+time.Second))
	if suppressed {
		t.Fatal("alert after the window must be emitted again")
	}
	if reason != impl.ReasonEmitted {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonEmitted)
	}
	if got := s.Count(fp); got != 1 {
		t.Fatalf("count after window reopen = %d, want 1", got)
	}
}

func TestActiveExceptionSuppressesAndKeepsCountZero(t *testing.T) {
	s := suppressor(5 * time.Minute)
	a := alert("r-4", "h-5", 443)
	now := time.Unix(1700000000, 0)
	fp := s.Fingerprint(a)
	if err := s.AddException(impl.Exception{Fingerprint: fp, Expires: now.Add(time.Hour), Note: "known cdn"}); err != nil {
		t.Fatalf("add exception: unexpected error %v", err)
	}
	suppressed, reason := s.Suppress(a, now)
	if !suppressed {
		t.Fatal("alert covered by an active exception must be suppressed")
	}
	if reason != impl.ReasonException {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonException)
	}
	if got := s.Count(fp); got != 0 {
		t.Fatalf("count after exception suppression = %d, want 0", got)
	}
	if got := s.Stats().Emitted; got != 0 {
		t.Fatalf("emitted stat = %d, want 0", got)
	}
}

func TestAddExceptionValidatesFingerprint(t *testing.T) {
	s := suppressor(time.Minute)
	now := time.Unix(1700000000, 0)
	if err := s.AddException(impl.Exception{Fingerprint: "", Expires: now.Add(time.Hour)}); !errors.Is(err, impl.ErrInvalidException) {
		t.Fatalf("empty fingerprint error = %v, want ErrInvalidException", err)
	}
	if err := s.AddException(impl.Exception{Fingerprint: "r-1|h-1|22"}); err != nil {
		t.Fatalf("exception without expiry must be accepted, got %v", err)
	}
	if err := s.RemoveException("missing|fp|1"); !errors.Is(err, impl.ErrInvalidException) {
		t.Fatalf("remove unknown error = %v, want ErrInvalidException", err)
	}
}

func TestExceptionTableLimitAndExpiryPurge(t *testing.T) {
	s := impl.NewAlertSuppressor(impl.SuppressorOptions{Window: time.Minute, MaxExceptions: 2})
	now := time.Unix(1700000000, 0)
	a := alert("r-5", "h-6", 22)
	s.Suppress(a, now)
	first := impl.Exception{Fingerprint: "r-5|h-6|22", Expires: now.Add(time.Second)}
	second := impl.Exception{Fingerprint: "r-6|h-6|23", Expires: now.Add(time.Hour)}
	third := impl.Exception{Fingerprint: "r-7|h-6|24", Expires: now.Add(3 * time.Hour)}
	if err := s.AddException(first); err != nil {
		t.Fatalf("add first exception: %v", err)
	}
	if err := s.AddException(second); err != nil {
		t.Fatalf("add second exception: %v", err)
	}
	if err := s.AddException(third); !errors.Is(err, impl.ErrExceptionLimit) {
		t.Fatalf("third exception error = %v, want ErrExceptionLimit", err)
	}
	s.Suppress(a, now.Add(2*time.Second))
	if err := s.AddException(third); err != nil {
		t.Fatalf("add after expiry purge: unexpected error %v", err)
	}
	active := s.Exceptions()
	if len(active) != 2 {
		t.Fatalf("active exceptions = %d, want 2", len(active))
	}
	if active[0].Fingerprint != second.Fingerprint {
		t.Fatalf("first active exception = %q, want %q", active[0].Fingerprint, second.Fingerprint)
	}
	if got := s.Stats().Exceptions; got != 2 {
		t.Fatalf("exception stat = %d, want 2", got)
	}
}

func TestBaselineShiftEmitsAndRestartsWindow(t *testing.T) {
	s := suppressor(5 * time.Minute)
	now := time.Unix(1700000000, 0)
	a := alert("r-8", "h-7", 22)
	if err := s.SetBaseline("h-7", 7); err != nil {
		t.Fatalf("set baseline: unexpected error %v", err)
	}
	if got := s.Baseline("h-7"); got != 7 {
		t.Fatalf("baseline = %d, want 7", got)
	}
	stale := a
	stale.Baseline = 6
	suppressed, reason := s.Suppress(stale, now)
	if suppressed {
		t.Fatal("alert from an old baseline must be emitted again")
	}
	if reason != impl.ReasonBaselineShift {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonBaselineShift)
	}
	current := a
	current.Baseline = 7
	suppressed, reason = s.Suppress(current, now.Add(time.Second))
	if !suppressed || reason != impl.ReasonDuplicate {
		t.Fatalf("after baseline shift: suppressed=%v reason=%q, want true/%q", suppressed, reason, impl.ReasonDuplicate)
	}
}

func TestSetBaselineValidatesTarget(t *testing.T) {
	s := suppressor(time.Minute)
	if err := s.SetBaseline("", 1); !errors.Is(err, impl.ErrInvalidTarget) {
		t.Fatalf("empty target error = %v, want ErrInvalidTarget", err)
	}
	if err := s.SetBaseline("h-9", -1); !errors.Is(err, impl.ErrInvalidTarget) {
		t.Fatalf("negative version error = %v, want ErrInvalidTarget", err)
	}
	if err := s.SetBaseline("h-9", 3); err != nil {
		t.Fatalf("valid baseline: unexpected error %v", err)
	}
	if got := s.Baseline("h-9"); got != 3 {
		t.Fatalf("baseline = %d, want 3", got)
	}
	if got := s.Baseline("unknown"); got != 0 {
		t.Fatalf("unset baseline = %d, want 0", got)
	}
}

func TestRecountDropsStaleBaselineCounts(t *testing.T) {
	s := suppressor(5 * time.Minute)
	now := time.Unix(1700000000, 0)
	a := alert("r-9", "h-10", 22)
	fp := s.Fingerprint(a)
	s.Suppress(a, now)
	s.Suppress(a, now.Add(time.Second))
	if got := s.Count(fp); got != 2 {
		t.Fatalf("count before recount = %d, want 2", got)
	}
	if err := s.SetBaseline("h-10", 4); err != nil {
		t.Fatalf("set baseline: unexpected error %v", err)
	}
	if err := s.Recount(); err != nil {
		t.Fatalf("recount: unexpected error %v", err)
	}
	if got := s.Count(fp); got != 0 {
		t.Fatalf("count after recount = %d, want 0", got)
	}
	if got := s.Stats().Fingerprints; got != 0 {
		t.Fatalf("fingerprint stat = %d, want 0", got)
	}
	if err := s.Recount(); err != nil {
		t.Fatalf("second recount: unexpected error %v", err)
	}
	if got := s.Count(fp); got != 0 {
		t.Fatalf("count after second recount = %d, want 0", got)
	}
}

func TestFingerprintTableEvictsEarliestExpiring(t *testing.T) {
	s := impl.NewAlertSuppressor(impl.SuppressorOptions{Window: 5 * time.Minute, MaxFingerprints: 2})
	now := time.Unix(1700000000, 0)
	first := alert("r-10", "h-11", 22)
	second := alert("r-11", "h-11", 23)
	third := alert("r-12", "h-11", 24)
	s.Suppress(first, now)
	s.Suppress(second, now.Add(time.Minute))
	if got := s.Stats().Fingerprints; got != 2 {
		t.Fatalf("fingerprint stat = %d, want 2", got)
	}
	s.Suppress(third, now.Add(2*time.Minute))
	if got := s.Stats().Evicted; got != 1 {
		t.Fatalf("evicted stat = %d, want 1", got)
	}
	suppressed, reason := s.Suppress(first, now.Add(2*time.Minute+time.Second))
	if suppressed {
		t.Fatal("evicted fingerprint must be emitted again")
	}
	if reason != impl.ReasonEmitted {
		t.Fatalf("reason = %q, want %q", reason, impl.ReasonEmitted)
	}
}

func TestConcurrentSuppressEmitsExactlyOnce(t *testing.T) {
	s := suppressor(time.Minute)
	a := alert("r-13", "h-12", 443)
	now := time.Unix(1700000000, 0)
	var emitted int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			suppressed, _ := s.Suppress(a, now)
			if !suppressed {
				atomic.AddInt32(&emitted, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&emitted); got != 1 {
		t.Fatalf("emitted alerts = %d, want 1", got)
	}
	if got := s.Stats().Emitted; got != 1 {
		t.Fatalf("emitted stat = %d, want 1", got)
	}
	if got := s.Stats().Suppressed; got != 63 {
		t.Fatalf("suppressed stat = %d, want 63", got)
	}
}

func TestSnapshotRestoreKeepsCounters(t *testing.T) {
	s := suppressor(5 * time.Minute)
	a := alert("r-14", "h-13", 8080)
	now := time.Unix(1700000000, 0)
	fp := s.Fingerprint(a)
	s.Suppress(a, now)
	s.Suppress(a, now.Add(time.Second))
	s.Suppress(a, now.Add(2*time.Second))
	data, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := suppressor(5 * time.Minute)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if got := restored.Count(fp); got != 3 {
		t.Fatalf("restored count = %d, want 3", got)
	}
	if got := restored.Stats().Suppressed; got != 2 {
		t.Fatalf("restored suppressed stat = %d, want 2", got)
	}
	suppressed, reason := restored.Suppress(a, now.Add(3*time.Second))
	if !suppressed || reason != impl.ReasonDuplicate {
		t.Fatalf("alert after restore: suppressed=%v reason=%q, want true/%q", suppressed, reason, impl.ReasonDuplicate)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	s := suppressor(time.Minute)
	if err := s.Restore([]byte(`{"window":"not-a-duration"`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore error = %v, want ErrBadSnapshot", err)
	}
	if got := s.Stats().Emitted; got != 0 {
		t.Fatalf("emitted stat after failed restore = %d, want 0", got)
	}
}
