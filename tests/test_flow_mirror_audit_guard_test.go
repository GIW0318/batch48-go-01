package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-07"
)

func basePolicy(id string) impl.MirrorPolicy {
	return impl.MirrorPolicy{
		ID:          id,
		Tenant:      "tenant-1",
		Priority:    10,
		SampleRate:  1,
		Granularity: 24,
		Fields:      []string{"src_ip", "dst_ip", "proto"},
		Family:      "ipv4",
	}
}

func testFlow() impl.FlowKey {
	return impl.FlowKey{
		SrcIP:   "10.1.2.3",
		DstIP:   "10.9.8.7",
		SrcPort: 1234,
		DstPort: 443,
		Proto:   "tcp",
		Tenant:  "tenant-1",
	}
}

func addPolicy(t *testing.T, g *impl.MirrorAuditGuard, p impl.MirrorPolicy) {
	t.Helper()
	if err := g.AddPolicy(p); err != nil {
		t.Fatalf("add policy %s: %v", p.ID, err)
	}
}

func addTarget(t *testing.T, g *impl.MirrorAuditGuard, name string, maxPPS int, healthy bool) {
	t.Helper()
	if err := g.SetTarget(impl.MirrorTarget{Name: name, MaxPPS: maxPPS, Healthy: healthy}); err != nil {
		t.Fatalf("set target %s: %v", name, err)
	}
}

func TestAddPolicyRejectsInvalidConfiguration(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	if err := g.AddPolicy(basePolicy("")); !errors.Is(err, impl.ErrPolicyConfig) {
		t.Fatalf("empty policy id: want ErrPolicyConfig, got %v", err)
	}
	badRate := basePolicy("pol-rate")
	badRate.SampleRate = 0
	if err := g.AddPolicy(badRate); !errors.Is(err, impl.ErrPolicyConfig) {
		t.Fatalf("sample rate 0: want ErrPolicyConfig, got %v", err)
	}
	badFamily := basePolicy("pol-family")
	badFamily.Family = "ipx"
	if err := g.AddPolicy(badFamily); !errors.Is(err, impl.ErrPolicyConfig) {
		t.Fatalf("family ipx: want ErrPolicyConfig, got %v", err)
	}
}

func TestAddPolicyRejectsDuplicateID(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-1"))
	if err := g.AddPolicy(basePolicy("pol-1")); !errors.Is(err, impl.ErrPolicyExists) {
		t.Fatalf("duplicate policy: want ErrPolicyExists, got %v", err)
	}
}

func TestAddPolicyTracksPolicyCount(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	if got := g.Stats().Policies; got != 0 {
		t.Fatalf("fresh guard policies = %d, want 0", got)
	}
	addPolicy(t, g, basePolicy("pol-1"))
	addPolicy(t, g, basePolicy("pol-2"))
	stats := g.Stats()
	if stats.Policies != 2 {
		t.Fatalf("policies = %d, want 2", stats.Policies)
	}
	if stats.Sessions != 0 {
		t.Fatalf("sessions = %d, want 0", stats.Sessions)
	}
}

func TestMatchRejectsFlowFromOtherTenant(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-1"))
	f := testFlow()
	f.Tenant = "tenant-2"
	if _, ok := g.Match(f); ok {
		t.Fatalf("flow from tenant-2 must not match a tenant-1 policy")
	}
	empty := testFlow()
	empty.SrcIP = ""
	if _, ok := g.Match(empty); ok {
		t.Fatalf("flow without source address must not match")
	}
}

func TestMatchReturnsPolicyForTenant(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	p := basePolicy("pol-1")
	addPolicy(t, g, p)
	got, ok := g.Match(testFlow())
	if !ok {
		t.Fatalf("tenant-1 flow must match policy pol-1")
	}
	if got.ID != "pol-1" || got.SampleRate != 1 {
		t.Fatalf("matched policy = %+v", got)
	}
	if stats := g.Stats(); stats.Matched != 1 {
		t.Fatalf("matched counter = %d, want 1", stats.Matched)
	}
}

func TestSampleRateOneMirrorsEveryPacket(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	p := basePolicy("pol-1")
	for seq := int64(0); seq < 4; seq++ {
		if !g.Sample(testFlow(), p, seq) {
			t.Fatalf("rate 1 must sample seq %d", seq)
		}
	}
	if stats := g.Stats(); stats.Sampled != 4 {
		t.Fatalf("sampled counter = %d, want 4", stats.Sampled)
	}
}

func TestSampleRateHalfHalvesWindow(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	p := basePolicy("pol-1")
	p.SampleRate = 2
	hits := 0
	for seq := int64(0); seq < 10; seq++ {
		if g.Sample(testFlow(), p, seq) {
			hits++
		}
	}
	if hits != 5 {
		t.Fatalf("rate 2 sampled %d of 10 packets, want 5", hits)
	}
}

func TestSampleIsDeterministicPerFlowAndSequence(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	p := basePolicy("pol-1")
	p.SampleRate = 4
	first := make([]bool, 8)
	second := make([]bool, 8)
	for seq := int64(0); seq < 8; seq++ {
		first[seq] = g.Sample(testFlow(), p, seq)
		second[seq] = g.Sample(testFlow(), p, seq)
	}
	for seq := 0; seq < 8; seq++ {
		if first[seq] != second[seq] {
			t.Fatalf("seq %d changed result between identical calls", seq)
		}
	}
	other := testFlow()
	other.SrcPort = 4321
	otherHits := 0
	for seq := int64(0); seq < 8; seq++ {
		if g.Sample(other, p, seq) {
			otherHits++
		}
	}
	if otherHits != 2 {
		t.Fatalf("rate 4 sampled %d of 8 packets for another flow, want 2", otherHits)
	}
}

func TestProjectKeepsOnlyWhitelistedFields(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	p := basePolicy("pol-1")
	header := map[string]string{
		"src_ip":  "10.1.2.3",
		"dst_ip":  "10.9.8.7",
		"proto":   "tcp",
		"payload": "secret-body",
		"user":    "alice",
	}
	got := g.Project(testFlow(), p, header)
	if len(got) != 3 {
		t.Fatalf("projected fields = %v, want exactly 3", got)
	}
	if _, leaked := got["payload"]; leaked {
		t.Fatalf("payload must not be projected")
	}
	if _, leaked := got["user"]; leaked {
		t.Fatalf("user field must not be projected")
	}
	if got["proto"] != "tcp" {
		t.Fatalf("proto = %q, want tcp", got["proto"])
	}
	if len(header) != 5 {
		t.Fatalf("Project must not mutate the caller map, len = %d", len(header))
	}
}

func TestProjectMasksIPv4ByGranularity(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	header := map[string]string{"src_ip": "10.1.2.3", "dst_ip": "192.168.9.9", "proto": "udp"}
	p24 := basePolicy("pol-24")
	got := g.Project(testFlow(), p24, header)
	if got["src_ip"] != "10.1.2.0" {
		t.Fatalf("src_ip /24 = %q, want 10.1.2.0", got["src_ip"])
	}
	p16 := basePolicy("pol-16")
	p16.Granularity = 16
	got = g.Project(testFlow(), p16, header)
	if got["src_ip"] != "10.1.0.0" {
		t.Fatalf("src_ip /16 = %q, want 10.1.0.0", got["src_ip"])
	}
	if got["dst_ip"] != "192.168.0.0" {
		t.Fatalf("dst_ip /16 = %q, want 192.168.0.0", got["dst_ip"])
	}
}

func TestAddSessionRejectsUnknownPolicy(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addTarget(t, g, "tgt-1", 100, true)
	err := g.AddSession(impl.MirrorSession{ID: "sess-1", PolicyID: "pol-missing", Target: "tgt-1", Tenant: "tenant-1"})
	if !errors.Is(err, impl.ErrPolicyUnknown) {
		t.Fatalf("unknown policy: want ErrPolicyUnknown, got %v", err)
	}
}

func TestAddSessionRejectsUnknownTarget(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-1"))
	err := g.AddSession(impl.MirrorSession{ID: "sess-1", PolicyID: "pol-1", Target: "tgt-missing", Tenant: "tenant-1"})
	if !errors.Is(err, impl.ErrTargetUnknown) {
		t.Fatalf("unknown target: want ErrTargetUnknown, got %v", err)
	}
}

func TestDistributeCapsAtTargetQuota(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-1"))
	addTarget(t, g, "tgt-1", 3, true)
	if err := g.AddSession(impl.MirrorSession{ID: "sess-1", PolicyID: "pol-1", Target: "tgt-1", Tenant: "tenant-1"}); err != nil {
		t.Fatalf("add session: %v", err)
	}
	delivered, err := g.Distribute("sess-1", 5)
	if err != nil {
		t.Fatalf("distribute: %v", err)
	}
	if delivered != 3 {
		t.Fatalf("first distribute delivered %d, want 3 (quota)", delivered)
	}
	if used, ok := g.TargetUsed("tgt-1"); !ok || used != 3 {
		t.Fatalf("target used = %d, %v; want 3", used, ok)
	}
	delivered, err = g.Distribute("sess-1", 2)
	if err != nil {
		t.Fatalf("second distribute: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("exhausted quota delivered %d, want 0", delivered)
	}
	stats := g.Stats()
	if stats.Delivered != 3 || stats.Dropped != 4 {
		t.Fatalf("delivered/dropped = %d/%d, want 3/4", stats.Delivered, stats.Dropped)
	}
	sess, ok := g.SessionStats("sess-1")
	if !ok || sess.Mirrored != 3 {
		t.Fatalf("session mirrored = %+v, %v; want 3", sess, ok)
	}
}

func TestDistributeDegradesToHighPriorityWhenTargetDown(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-low"))
	high := basePolicy("pol-high")
	high.Priority = 20
	high.HighPriority = true
	addPolicy(t, g, high)
	addTarget(t, g, "tgt-1", 100, false)
	if err := g.AddSession(impl.MirrorSession{ID: "sess-low", PolicyID: "pol-low", Target: "tgt-1", Tenant: "tenant-1"}); err != nil {
		t.Fatalf("add low session: %v", err)
	}
	if err := g.AddSession(impl.MirrorSession{ID: "sess-high", PolicyID: "pol-high", Target: "tgt-1", Tenant: "tenant-1"}); err != nil {
		t.Fatalf("add high session: %v", err)
	}
	low, err := g.Distribute("sess-low", 5)
	if err != nil {
		t.Fatalf("distribute low: %v", err)
	}
	if low != 0 {
		t.Fatalf("degraded target delivered %d low priority packets, want 0", low)
	}
	hi, err := g.Distribute("sess-high", 5)
	if err != nil {
		t.Fatalf("distribute high: %v", err)
	}
	if hi != 5 {
		t.Fatalf("degraded target delivered %d high priority packets, want 5", hi)
	}
	if stats := g.Stats(); stats.TargetsDown != 1 {
		t.Fatalf("targets down = %d, want 1", stats.TargetsDown)
	}
}

func TestSnapshotRestorePreservesPoliciesAndQuota(t *testing.T) {
	g := impl.NewMirrorAuditGuard()
	addPolicy(t, g, basePolicy("pol-1"))
	addTarget(t, g, "tgt-1", 100, true)
	if err := g.AddSession(impl.MirrorSession{ID: "sess-1", PolicyID: "pol-1", Target: "tgt-1", Tenant: "tenant-1"}); err != nil {
		t.Fatalf("add session: %v", err)
	}
	if _, err := g.Distribute("sess-1", 2); err != nil {
		t.Fatalf("distribute: %v", err)
	}
	blob, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewMirrorAuditGuard()
	if err := restored.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Stats().Policies; got != 1 {
		t.Fatalf("restored policies = %d, want 1", got)
	}
	if used, ok := restored.TargetUsed("tgt-1"); !ok || used != 2 {
		t.Fatalf("restored target used = %d, %v; want 2", used, ok)
	}
	sess, ok := restored.SessionStats("sess-1")
	if !ok || sess.Mirrored != 2 {
		t.Fatalf("restored session = %+v, %v; want mirrored 2", sess, ok)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrMirrorSnapshot) {
		t.Fatalf("garbage snapshot: want ErrMirrorSnapshot, got %v", err)
	}
	if used, ok := restored.TargetUsed("tgt-1"); !ok || used != 2 {
		t.Fatalf("bad snapshot disturbed state, used = %d, %v", used, ok)
	}
}
