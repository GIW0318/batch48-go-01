package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-01"
)

func baselineFingerprint() impl.Fingerprint {
	return impl.Fingerprint{
		UserAgent:  "Mozilla/5.0 (Windows NT 10.0) gsb-client/7",
		AcceptLang: "zh-CN,zh;q=0.9",
		Screen:     "1920x1080",
		TimeZone:   "Asia/Shanghai",
		Canvas:     "canvas-4f2a",
		TLSHash:    "tls-9c01",
		PluginHash: "plugin-77b3",
	}
}

func testLimits() impl.Limits {
	return impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 2, MaxEvents: 8}
}

func TestBeginSessionReturnsActiveBinding(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	s, err := b.BeginSession("user-1", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session must succeed, got %v", err)
	}
	if s.ID == "" {
		t.Fatalf("session id must not be empty")
	}
	if s.UserID != "user-1" || s.DeviceID != "device-1" {
		t.Fatalf("session identity mismatch: user=%q device=%q", s.UserID, s.DeviceID)
	}
	if s.Score != impl.FingerprintMaxScore {
		t.Fatalf("fresh session score = %d, want %d", s.Score, impl.FingerprintMaxScore)
	}
	if s.Seq <= 0 {
		t.Fatalf("session sequence = %d, want a positive monotonic number", s.Seq)
	}
	binding, ok := b.Binding("user-1", "device-1")
	if !ok {
		t.Fatalf("expected a device binding after the session begins")
	}
	if binding.State != impl.DeviceActive {
		t.Fatalf("binding state = %q, want %q", binding.State, impl.DeviceActive)
	}
	if binding.Sessions != 1 {
		t.Fatalf("binding session count = %d, want 1", binding.Sessions)
	}
	devices := b.ActiveDevices("user-1")
	if len(devices) != 1 || devices[0].DeviceID != "device-1" {
		t.Fatalf("active devices = %+v, want exactly device-1", devices)
	}
}

func TestBeginSessionRejectsBlankIdentity(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	if _, err := b.BeginSession("   ", "device-1", baselineFingerprint()); !errors.Is(err, impl.ErrBlankIdentity) {
		t.Fatalf("blank user id must be rejected with ErrBlankIdentity, got %v", err)
	}
	if _, err := b.BeginSession("user-1", "", baselineFingerprint()); !errors.Is(err, impl.ErrBlankIdentity) {
		t.Fatalf("blank device id must be rejected with ErrBlankIdentity, got %v", err)
	}
	if got := len(b.ActiveDevices("user-1")); got != 0 {
		t.Fatalf("rejected requests must not create bindings, got %d active devices", got)
	}
}

func TestBeginSessionEnforcesDeviceLimit(t *testing.T) {
	b := impl.NewDeviceSessionBinder(impl.Limits{MaxDevicesPerUser: 2, MaxSessionsPerDevice: 2, MaxEvents: 8})
	for _, device := range []string{"device-1", "device-2"} {
		if _, err := b.BeginSession("user-2", device, baselineFingerprint()); err != nil {
			t.Fatalf("begin session on %s: %v", device, err)
		}
	}
	_, err := b.BeginSession("user-2", "device-3", baselineFingerprint())
	if !errors.Is(err, impl.ErrDeviceLimit) {
		t.Fatalf("third device must hit the per user device limit, got %v", err)
	}
	if got := len(b.ActiveDevices("user-2")); got != 2 {
		t.Fatalf("active device count = %d, want the configured cap 2", got)
	}
	if _, ok := b.Binding("user-2", "device-3"); ok {
		t.Fatalf("rejected device must not get a binding")
	}
}

func TestBeginSessionEnforcesSessionLimitPerDevice(t *testing.T) {
	b := impl.NewDeviceSessionBinder(impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 2, MaxEvents: 8})
	for i := 0; i < 2; i++ {
		if _, err := b.BeginSession("user-3", "device-1", baselineFingerprint()); err != nil {
			t.Fatalf("session %d on the same device must succeed, got %v", i, err)
		}
	}
	if _, err := b.BeginSession("user-3", "device-1", baselineFingerprint()); !errors.Is(err, impl.ErrSessionLimit) {
		t.Fatalf("third session on one device must hit ErrSessionLimit, got %v", err)
	}
	binding, ok := b.Binding("user-3", "device-1")
	if !ok {
		t.Fatalf("expected the binding to exist")
	}
	if binding.Sessions != 2 {
		t.Fatalf("binding session count = %d, want 2", binding.Sessions)
	}
}

func TestConcurrentBeginSessionsRespectDeviceLimit(t *testing.T) {
	b := impl.NewDeviceSessionBinder(impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 1, MaxEvents: 8})
	var wg sync.WaitGroup
	var mu sync.Mutex
	started, limited := 0, 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := b.BeginSession("user-c", fmt.Sprintf("device-%02d", i), baselineFingerprint())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				started++
			case errors.Is(err, impl.ErrDeviceLimit):
				limited++
			}
		}(i)
	}
	wg.Wait()
	if started != 4 {
		t.Fatalf("concurrent begins started %d sessions, want exactly the cap 4", started)
	}
	if limited != 12 {
		t.Fatalf("concurrent begins reported %d limit errors, want 12", limited)
	}
	if got := len(b.ActiveDevices("user-c")); got != 4 {
		t.Fatalf("active device count after the race = %d, want 4", got)
	}
}

func TestReportStableFingerprintAllows(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	s, err := b.BeginSession("user-4", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	v, err := b.ReportFingerprint(s.ID, baselineFingerprint())
	if err != nil {
		t.Fatalf("report fingerprint: %v", err)
	}
	if v.Action != impl.ActionAllow || v.Reason != impl.ReasonStable {
		t.Fatalf("stable report got action=%q reason=%q, want allow/stable", v.Action, v.Reason)
	}
	if v.Score != impl.FingerprintMaxScore {
		t.Fatalf("stable report score = %d, want %d", v.Score, impl.FingerprintMaxScore)
	}
	if len(v.Drift) != 0 {
		t.Fatalf("stable report must not list drifted components, got %v", v.Drift)
	}
	binding, _ := b.Binding("user-4", "device-1")
	if binding.Score != impl.FingerprintMaxScore || binding.Failures != 0 {
		t.Fatalf("binding score=%d failures=%d, want %d and 0", binding.Score, binding.Failures, impl.FingerprintMaxScore)
	}
}

func TestReportHardDriftKicksSession(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	s, err := b.BeginSession("user-5", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	drifted := baselineFingerprint()
	drifted.TLSHash = "tls-stolen"
	drifted.UserAgent = "Mozilla/5.0 (X11) other-browser/1"
	drifted.Canvas = "canvas-0000"
	v, err := b.ReportFingerprint(s.ID, drifted)
	if err != nil {
		t.Fatalf("report fingerprint: %v", err)
	}
	if v.Action != impl.ActionKick || v.Reason != impl.ReasonHardDrift {
		t.Fatalf("hard drift got action=%q reason=%q, want kick/hard drift", v.Action, v.Reason)
	}
	if v.Score != 30 {
		t.Fatalf("hard drift score = %d, want 100-30-20-20 = 30", v.Score)
	}
	want := []string{"canvas", "tls_hash", "user_agent"}
	if len(v.Drift) != len(want) {
		t.Fatalf("drift components = %v, want %v", v.Drift, want)
	}
	for i, name := range want {
		if v.Drift[i] != name {
			t.Fatalf("drift components = %v, want the sorted list %v", v.Drift, want)
		}
	}
	binding, _ := b.Binding("user-5", "device-1")
	if binding.Failures != 1 {
		t.Fatalf("binding failure counter = %d, want 1 after a kicked report", binding.Failures)
	}
	if binding.Sessions != 0 {
		t.Fatalf("binding session count = %d, want 0 after the session is kicked", binding.Sessions)
	}
	if _, err := b.ReportFingerprint(s.ID, baselineFingerprint()); !errors.Is(err, impl.ErrUnknownSession) {
		t.Fatalf("reporting on a kicked session must fail with ErrUnknownSession, got %v", err)
	}
}

func TestReportUnknownSessionFails(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	if _, err := b.ReportFingerprint("sess-404", baselineFingerprint()); !errors.Is(err, impl.ErrUnknownSession) {
		t.Fatalf("unknown session must be rejected with ErrUnknownSession, got %v", err)
	}
	if _, err := b.ReportFingerprint("", baselineFingerprint()); !errors.Is(err, impl.ErrUnknownSession) {
		t.Fatalf("empty session id must be rejected with ErrUnknownSession, got %v", err)
	}
}

func TestSuspendDeviceRequiresReason(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	if _, err := b.BeginSession("user-6", "device-1", baselineFingerprint()); err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if err := b.SuspendDevice("user-6", "device-1", "   "); !errors.Is(err, impl.ErrBadReason) {
		t.Fatalf("suspend without a reason must fail with ErrBadReason, got %v", err)
	}
	binding, ok := b.Binding("user-6", "device-1")
	if !ok || binding.State != impl.DeviceActive {
		t.Fatalf("failed suspend must leave the binding active, got state=%q ok=%v", binding.State, ok)
	}
	if err := b.SuspendDevice("user-6", "device-404", "manual review"); !errors.Is(err, impl.ErrDeviceNotFound) {
		t.Fatalf("suspending an unknown device must fail with ErrDeviceNotFound, got %v", err)
	}
}

func TestSuspendDeviceClosesDeviceSessions(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	s, err := b.BeginSession("user-7", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if err := b.SuspendDevice("user-7", "device-1", "lost device"); err != nil {
		t.Fatalf("suspend device: %v", err)
	}
	binding, ok := b.Binding("user-7", "device-1")
	if !ok {
		t.Fatalf("suspended binding must stay readable")
	}
	if binding.State != impl.DeviceSuspended {
		t.Fatalf("binding state = %q, want %q", binding.State, impl.DeviceSuspended)
	}
	if binding.Reason != "lost device" {
		t.Fatalf("binding reason = %q, want the operator note", binding.Reason)
	}
	if binding.Sessions != 0 {
		t.Fatalf("suspended device still has %d live sessions, want 0", binding.Sessions)
	}
	if got := len(b.ActiveDevices("user-7")); got != 0 {
		t.Fatalf("active devices after suspend = %d, want 0", got)
	}
	if _, err := b.ReportFingerprint(s.ID, baselineFingerprint()); !errors.Is(err, impl.ErrUnknownSession) {
		t.Fatalf("reporting on a session of a suspended device must fail, got %v", err)
	}
}

func TestRevokeDeviceMarksBindingRevoked(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	if _, err := b.BeginSession("user-8", "device-1", baselineFingerprint()); err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if _, err := b.BeginSession("user-8", "device-2", baselineFingerprint()); err != nil {
		t.Fatalf("begin second device: %v", err)
	}
	if err := b.RevokeDevice("user-8", "device-1"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	binding, ok := b.Binding("user-8", "device-1")
	if !ok || binding.State != impl.DeviceRevoked {
		t.Fatalf("revoked binding state=%q ok=%v, want %q", binding.State, ok, impl.DeviceRevoked)
	}
	devices := b.ActiveDevices("user-8")
	if len(devices) != 1 || devices[0].DeviceID != "device-2" {
		t.Fatalf("active devices = %+v, want only device-2", devices)
	}
	if err := b.RevokeDevice("user-8", "device-1"); err != nil {
		t.Fatalf("revoking an already revoked device must be idempotent, got %v", err)
	}
	if err := b.RevokeDevice("user-8", "device-404"); !errors.Is(err, impl.ErrDeviceNotFound) {
		t.Fatalf("revoking an unknown device must fail with ErrDeviceNotFound, got %v", err)
	}
}

func TestEndSessionReleasesSlot(t *testing.T) {
	b := impl.NewDeviceSessionBinder(impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 1, MaxEvents: 8})
	s, err := b.BeginSession("user-9", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if _, err := b.BeginSession("user-9", "device-1", baselineFingerprint()); !errors.Is(err, impl.ErrSessionLimit) {
		t.Fatalf("second session must hit the per device cap, got %v", err)
	}
	if err := b.EndSession(s.ID); err != nil {
		t.Fatalf("end session: %v", err)
	}
	binding, _ := b.Binding("user-9", "device-1")
	if binding.Sessions != 0 {
		t.Fatalf("binding session count = %d, want 0 after end", binding.Sessions)
	}
	if _, err := b.BeginSession("user-9", "device-1", baselineFingerprint()); err != nil {
		t.Fatalf("slot must be released after the session ends, got %v", err)
	}
	if err := b.EndSession(s.ID); !errors.Is(err, impl.ErrUnknownSession) {
		t.Fatalf("ending a closed session must fail with ErrUnknownSession, got %v", err)
	}
}

func TestEventsStayWithinPerUserCap(t *testing.T) {
	b := impl.NewDeviceSessionBinder(impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 2, MaxEvents: 3})
	s, err := b.BeginSession("user-10", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	seqs := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		v, err := b.ReportFingerprint(s.ID, baselineFingerprint())
		if err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
		events := b.Events("user-10")
		if len(events) == 0 {
			t.Fatalf("report %d must record an event", i)
		}
		last := events[len(events)-1]
		if last.Seq != v.Seq {
			t.Fatalf("report %d verdict seq %d differs from event seq %d", i, v.Seq, last.Seq)
		}
		if last.Action != impl.ActionAllow {
			t.Fatalf("report %d action = %q, want allow", i, last.Action)
		}
		seqs = append(seqs, v.Seq)
	}
	events := b.Events("user-10")
	if len(events) != 3 {
		t.Fatalf("retained events = %d, want the configured cap 3", len(events))
	}
	for i, want := range seqs[2:] {
		if events[i].Seq != want {
			t.Fatalf("retained event %d has seq %d, want the newest window %d", i, events[i].Seq, want)
		}
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("verdict sequence must be strictly increasing, got %v", seqs)
		}
	}
	if len(b.Events("user-404")) != 0 {
		t.Fatalf("a user without reports must have an empty event log")
	}
}

func TestSnapshotRestoreKeepsRevocationWatermark(t *testing.T) {
	limits := impl.Limits{MaxDevicesPerUser: 4, MaxSessionsPerDevice: 2, MaxEvents: 8}
	b := impl.NewDeviceSessionBinder(limits)
	s1, err := b.BeginSession("user-11", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	drifted := baselineFingerprint()
	drifted.TLSHash = "tls-other"
	drifted.UserAgent = "Mozilla/5.0 (Macintosh) other-browser/1"
	drifted.Canvas = "canvas-9999"
	v, err := b.ReportFingerprint(s1.ID, drifted)
	if err != nil {
		t.Fatalf("report fingerprint: %v", err)
	}
	if v.Action != impl.ActionKick {
		t.Fatalf("report action = %q, want kick before revoking", v.Action)
	}
	if err := b.RevokeDevice("user-11", "device-1"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	data, err := b.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewDeviceSessionBinder(limits)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	binding, ok := restored.Binding("user-11", "device-1")
	if !ok {
		t.Fatalf("restored binder lost the device binding")
	}
	if binding.State != impl.DeviceRevoked {
		t.Fatalf("restored binding state = %q, want %q", binding.State, impl.DeviceRevoked)
	}
	if binding.Failures != 1 {
		t.Fatalf("restored failure counter = %d, want 1", binding.Failures)
	}
	if got := len(restored.ActiveDevices("user-11")); got != 0 {
		t.Fatalf("restored active devices = %d, want 0", got)
	}
	s2, err := restored.BeginSession("user-11", "device-2", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session after restore: %v", err)
	}
	if s2.Seq <= s1.Seq {
		t.Fatalf("session sequence after restore = %d, must continue past %d", s2.Seq, s1.Seq)
	}
	if s2.ID == s1.ID {
		t.Fatalf("session ids must not be reused after restore")
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	b := impl.NewDeviceSessionBinder(testLimits())
	s, err := b.BeginSession("user-12", "device-1", baselineFingerprint())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if err := b.Restore([]byte("{not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	binding, ok := b.Binding("user-12", "device-1")
	if !ok || binding.State != impl.DeviceActive || binding.Sessions != 1 {
		t.Fatalf("failed restore must not touch live state, got %+v ok=%v", binding, ok)
	}
	if _, err := b.ReportFingerprint(s.ID, baselineFingerprint()); err != nil {
		t.Fatalf("live session must keep working after a rejected restore, got %v", err)
	}
}
