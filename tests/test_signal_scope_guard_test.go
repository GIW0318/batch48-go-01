package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-14"
)

func scopeOptions() impl.Options {
	return impl.Options{
		Burst:           3,
		WindowMs:        1000,
		MaxProcs:        8,
		GuardianSignals: []impl.Signal{impl.SigStop, impl.SigTerm},
		Now:             func() int64 { return 1000 },
	}
}

func mustRegister(t *testing.T, g *impl.SignalScopeGuard, p impl.Proc) {
	t.Helper()
	if err := g.Register(p); err != nil {
		t.Fatalf("register pid %d: %v", p.Pid, err)
	}
}

func TestRegisterValidatesProcess(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	if err := g.Register(impl.Proc{Pid: 0, Domain: "svc", StartTime: 10}); !errors.Is(err, impl.ErrBadProcess) {
		t.Fatalf("pid 0 = %v, want ErrBadProcess", err)
	}
	if err := g.Register(impl.Proc{Pid: 10, Domain: "   ", StartTime: 10}); !errors.Is(err, impl.ErrBadProcess) {
		t.Fatalf("blank domain = %v, want ErrBadProcess", err)
	}
}

func TestRegisterRejectsDuplicateAndOverLimit(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 1, Domain: "svc", StartTime: 10})
	if err := g.Register(impl.Proc{Pid: 1, Domain: "other", StartTime: 20}); !errors.Is(err, impl.ErrPidTaken) {
		t.Fatalf("duplicate pid = %v, want ErrPidTaken", err)
	}
	opts := scopeOptions()
	opts.MaxProcs = 1
	small := impl.NewSignalScopeGuard(opts)
	mustRegister(t, small, impl.Proc{Pid: 2, Domain: "svc", StartTime: 10})
	if err := small.Register(impl.Proc{Pid: 3, Domain: "svc", StartTime: 10}); !errors.Is(err, impl.ErrRegistryFull) {
		t.Fatalf("register over limit = %v, want ErrRegistryFull", err)
	}
}

func TestAllowRejectsUnknownParticipants(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	if _, err := g.Allow(impl.SigTerm, 1, 2); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("unknown sender = %v, want ErrUnknownTarget", err)
	}
	mustRegister(t, g, impl.Proc{Pid: 1, Domain: "svc", StartTime: 10})
	if _, err := g.Allow(impl.SigTerm, 1, 2); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("unknown target = %v, want ErrUnknownTarget", err)
	}
}

func TestAllowSameDomain(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 10, Domain: "svc", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 11, Domain: "svc", StartTime: 20})
	v, err := g.Allow(impl.SigTerm, 10, 11)
	if err != nil {
		t.Fatalf("allow same domain: %v", err)
	}
	if !v.Allow || v.Code != impl.CodeSameDomain {
		t.Fatalf("verdict = %+v, want allow %q", v, impl.CodeSameDomain)
	}
}

func TestAllowSelfSignal(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 12, Domain: "svc", StartTime: 10})
	v, err := g.Allow(impl.SigHup, 12, 12)
	if err != nil {
		t.Fatalf("allow self: %v", err)
	}
	if !v.Allow || v.Code != impl.CodeSelfSignal {
		t.Fatalf("verdict = %+v, want allow %q", v, impl.CodeSelfSignal)
	}
}

func TestAllowCrossDomainDenied(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 20, Domain: "alpha", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 21, Domain: "beta", StartTime: 20})
	v, err := g.Allow(impl.SigTerm, 20, 21)
	if err != nil {
		t.Fatalf("allow cross domain: %v", err)
	}
	if v.Allow {
		t.Fatalf("ordinary process must not cross domains: %+v", v)
	}
	if v.Code != impl.CodeForeignDomain {
		t.Fatalf("code = %q, want %q", v.Code, impl.CodeForeignDomain)
	}
}

func TestAllowGuardianBridgeSignal(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 30, Domain: "alpha", StartTime: 10, Guardian: true})
	mustRegister(t, g, impl.Proc{Pid: 31, Domain: "beta", StartTime: 20})
	v, err := g.Allow(impl.SigStop, 30, 31)
	if err != nil {
		t.Fatalf("allow guardian bridge: %v", err)
	}
	if !v.Allow || v.Code != impl.CodeGuardianBridge {
		t.Fatalf("verdict = %+v, want allow %q", v, impl.CodeGuardianBridge)
	}
}

func TestAllowGuardianSignalNotRegistered(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 40, Domain: "alpha", StartTime: 10, Guardian: true})
	mustRegister(t, g, impl.Proc{Pid: 41, Domain: "beta", StartTime: 20})
	v, err := g.Allow(impl.SigHup, 40, 41)
	if err != nil {
		t.Fatalf("allow guardian hup: %v", err)
	}
	if v.Allow {
		t.Fatalf("signal outside the guardian allowlist must be denied: %+v", v)
	}
	if v.Code != impl.CodeForeignDomain {
		t.Fatalf("code = %q, want %q", v.Code, impl.CodeForeignDomain)
	}
}

func TestDeliverAuditsAcceptedSignal(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 50, Domain: "svc", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 51, Domain: "svc", StartTime: 20})
	if err := g.Deliver(impl.SigTerm, 50, 51); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	audit := g.Audit()
	if len(audit) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit))
	}
	rec := audit[0]
	if rec.Seq != 1 || rec.Sig != impl.SigTerm || rec.From != 50 || rec.To != 51 {
		t.Fatalf("audit record = %+v", rec)
	}
	if !rec.Allowed || rec.Code != impl.CodeSameDomain {
		t.Fatalf("audit verdict = %+v", rec)
	}
}

func TestDeliverRejectsForeignDomainAndAudits(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 20, Domain: "alpha", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 21, Domain: "beta", StartTime: 20})
	if err := g.Deliver(impl.SigTerm, 20, 21); !errors.Is(err, impl.ErrScopeDenied) {
		t.Fatalf("cross domain deliver = %v, want ErrScopeDenied", err)
	}
	audit := g.Audit()
	if len(audit) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit))
	}
	if audit[0].Allowed || audit[0].Code != impl.CodeForeignDomain {
		t.Fatalf("denied delivery audit = %+v", audit[0])
	}
}

func TestRateLimitConsumesBudget(t *testing.T) {
	opts := scopeOptions()
	opts.Burst = 2
	g := impl.NewSignalScopeGuard(opts)
	mustRegister(t, g, impl.Proc{Pid: 60, Domain: "svc", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 61, Domain: "svc", StartTime: 20})
	left, err := g.RateLimit(60, 1000)
	if err != nil || left != 2 {
		t.Fatalf("initial budget = %d err %v, want 2", left, err)
	}
	if err := g.Deliver(impl.SigTerm, 60, 61); err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	if err := g.Deliver(impl.SigTerm, 60, 61); err != nil {
		t.Fatalf("second deliver: %v", err)
	}
	left, err = g.RateLimit(60, 1000)
	if err != nil || left != 0 {
		t.Fatalf("budget after two sends = %d err %v, want 0", left, err)
	}
	if err := g.Deliver(impl.SigTerm, 60, 61); !errors.Is(err, impl.ErrSignalThrottled) {
		t.Fatalf("third deliver = %v, want ErrSignalThrottled", err)
	}
	audit := g.Audit()
	if len(audit) != 3 {
		t.Fatalf("audit entries = %d, want 3", len(audit))
	}
	if audit[2].Allowed || audit[2].Code != impl.CodeRateLimited {
		t.Fatalf("throttled audit = %+v", audit[2])
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	var clock int64 = 1000
	opts := scopeOptions()
	opts.Burst = 1
	opts.WindowMs = 500
	opts.Now = func() int64 { return clock }
	g := impl.NewSignalScopeGuard(opts)
	mustRegister(t, g, impl.Proc{Pid: 70, Domain: "svc", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 71, Domain: "svc", StartTime: 20})
	if err := g.Deliver(impl.SigTerm, 70, 71); err != nil {
		t.Fatalf("deliver in first window: %v", err)
	}
	clock = 1100
	if err := g.Deliver(impl.SigTerm, 70, 71); !errors.Is(err, impl.ErrSignalThrottled) {
		t.Fatalf("deliver inside window = %v, want ErrSignalThrottled", err)
	}
	clock = 1500
	if err := g.Deliver(impl.SigTerm, 70, 71); err != nil {
		t.Fatalf("deliver after window reset: %v", err)
	}
}

func TestUnregisterRemovesProcess(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 80, Domain: "svc", StartTime: 10})
	if err := g.Unregister(80); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := g.Lookup(80); ok {
		t.Fatalf("unregistered pid still resolvable")
	}
	if _, err := g.Allow(impl.SigTerm, 80, 80); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("allow after unregister = %v, want ErrUnknownTarget", err)
	}
	if err := g.Unregister(999); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("unregister unknown = %v, want ErrUnknownTarget", err)
	}
}

func TestSnapshotRestoreKeepsRegistryAndAudit(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	mustRegister(t, g, impl.Proc{Pid: 90, Domain: "svc", StartTime: 10})
	mustRegister(t, g, impl.Proc{Pid: 91, Domain: "svc", StartTime: 20})
	if err := g.Deliver(impl.SigTerm, 90, 91); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewSignalScopeGuard(scopeOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if p, ok := other.Lookup(90); !ok || p.Domain != "svc" || p.StartTime != 10 {
		t.Fatalf("restored process = %+v ok %v", p, ok)
	}
	if got := len(other.Audit()); got != 1 {
		t.Fatalf("restored audit entries = %d, want 1", got)
	}
}

func TestRestoreRejectsDamagedPayload(t *testing.T) {
	g := impl.NewSignalScopeGuard(scopeOptions())
	if err := g.Restore([]byte("nope")); !errors.Is(err, impl.ErrRestoreDamaged) {
		t.Fatalf("damaged restore = %v, want ErrRestoreDamaged", err)
	}
}
