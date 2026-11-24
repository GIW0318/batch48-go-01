package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-19"
)

func newArbiter(t *testing.T, capacity, ef, af, be uint64) *impl.TrafficClassArbiter {
	t.Helper()
	a, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity: capacity,
		Guarantees: map[impl.Class]uint64{
			impl.ClassEF: ef,
			impl.ClassAF: af,
			impl.ClassBE: be,
		},
		MaxFlows: 16,
	})
	if err != nil {
		t.Fatalf("arbiter build failed: %v", err)
	}
	return a
}

func TestNewArbiterValidatesConfig(t *testing.T) {
	over, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity: 100,
		Guarantees: map[impl.Class]uint64{
			impl.ClassEF: 60,
			impl.ClassAF: 30,
			impl.ClassBE: 20,
		},
		MaxFlows: 8,
	})
	if !errors.Is(err, impl.ErrGuaranteeOverCapacity) || over != nil {
		t.Fatalf("over committed quotas = (%v, %v), want ErrGuaranteeOverCapacity", over, err)
	}
	if _, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity:   0,
		Guarantees: map[impl.Class]uint64{impl.ClassEF: 1},
		MaxFlows:   8,
	}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero capacity must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity:   10,
		Guarantees: map[impl.Class]uint64{impl.ClassEF: 1},
		MaxFlows:   0,
	}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero flow limit must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity:   10,
		Guarantees: map[impl.Class]uint64{impl.Class(9): 1},
		MaxFlows:   8,
	}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("unknown guarantee class must report ErrInvalidConfig, got %v", err)
	}
	a, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity: 100,
		Guarantees: map[impl.Class]uint64{
			impl.ClassEF: 50,
			impl.ClassAF: 30,
			impl.ClassBE: 20,
		},
		MaxFlows: 8,
	})
	if err != nil || a == nil {
		t.Fatalf("valid config must build an arbiter, got (%v, %v)", a, err)
	}
}

func TestAdmitGrantsWithinGuarantee(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	d, err := a.Admit(1, impl.ClassEF, 30)
	if err != nil {
		t.Fatalf("admit must succeed, got %v", err)
	}
	if !d.Admitted || d.Granted != 30 || d.QueuePosition != 0 {
		t.Fatalf("decision = %+v, want admitted with 30 bytes and no queue position", d)
	}
	if len(d.BorrowedFrom) != 0 {
		t.Fatalf("decision borrowed from %v, want no lender", d.BorrowedFrom)
	}
	if got := a.Usage(impl.ClassEF); got != 30 {
		t.Fatalf("EF usage = %d, want 30", got)
	}
	if got := a.Usage(impl.ClassAF); got != 0 {
		t.Fatalf("AF usage = %d, want 0", got)
	}
	stats := a.Stats()
	if stats.Grants != 1 || stats.EFUsed != 30 {
		t.Fatalf("stats = %+v, want one grant and 30 EF bytes", stats)
	}
}

func TestAdmitRejectsInvalidRequests(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	if _, err := a.Admit(1, impl.ClassEF, 0); !errors.Is(err, impl.ErrZeroDemand) {
		t.Fatalf("zero demand must report ErrZeroDemand, got %v", err)
	}
	if _, err := a.Admit(1, impl.Class(0), 10); !errors.Is(err, impl.ErrInvalidClass) {
		t.Fatalf("unknown class must report ErrInvalidClass, got %v", err)
	}
	if _, err := a.Admit(1, impl.ClassEF, 10); err != nil {
		t.Fatalf("first admit must succeed, got %v", err)
	}
	if _, err := a.Admit(1, impl.ClassEF, 10); !errors.Is(err, impl.ErrFlowExists) {
		t.Fatalf("duplicate flow must report ErrFlowExists, got %v", err)
	}
	small, err := impl.NewTrafficClassArbiter(impl.ArbiterConfig{
		Capacity:   100,
		Guarantees: map[impl.Class]uint64{impl.ClassEF: 60, impl.ClassAF: 20, impl.ClassBE: 20},
		MaxFlows:   1,
	})
	if err != nil {
		t.Fatalf("arbiter build failed: %v", err)
	}
	if _, err := small.Admit(1, impl.ClassEF, 5); err != nil {
		t.Fatalf("first admit must succeed, got %v", err)
	}
	if _, err := small.Admit(2, impl.ClassEF, 5); !errors.Is(err, impl.ErrFlowLimit) {
		t.Fatalf("admit past the flow cap must report ErrFlowLimit, got %v", err)
	}
}

func TestAdmitQueuesWhenQuotaExhausted(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	if err := a.SetPolicy(impl.ArbiterPolicy{Borrowing: false, Preemption: true}); err != nil {
		t.Fatalf("policy must apply, got %v", err)
	}
	if d, err := a.Admit(1, impl.ClassEF, 50); err != nil || d.Granted != 50 {
		t.Fatalf("first admit = (%+v, %v), want 50 bytes", d, err)
	}
	d, err := a.Admit(2, impl.ClassEF, 10)
	if err != nil {
		t.Fatalf("queued admit must not fail, got %v", err)
	}
	if d.Admitted || d.QueuePosition != 1 {
		t.Fatalf("decision = %+v, want waiting at position 1", d)
	}
	stats := a.Stats()
	if stats.Waiting != 1 || stats.Grants != 1 || stats.Queued != 1 {
		t.Fatalf("stats = %+v, want one grant and one waiting flow", stats)
	}
	a.Release(1)
	if preemptions := a.Tick(time.Unix(1700000000, 0)); len(preemptions) != 0 {
		t.Fatalf("tick on a released quota must not preempt, got %v", preemptions)
	}
	if got := a.Usage(impl.ClassEF); got != 10 {
		t.Fatalf("EF usage after the retry = %d, want 10", got)
	}
	if got := a.Stats().Waiting; got != 0 {
		t.Fatalf("waiting flows after the retry = %d, want 0", got)
	}
}

func TestAdmitBorrowsIdleQuota(t *testing.T) {
	a := newArbiter(t, 100, 20, 30, 50)
	d, err := a.Admit(1, impl.ClassEF, 40)
	if err != nil {
		t.Fatalf("admit must succeed, got %v", err)
	}
	if !d.Admitted || d.Granted != 40 {
		t.Fatalf("decision = %+v, want admitted with 40 bytes", d)
	}
	if len(d.BorrowedFrom) != 1 || d.BorrowedFrom[0] != impl.ClassBE {
		t.Fatalf("borrowed from %v, want [BE]", d.BorrowedFrom)
	}
	if got := a.Usage(impl.ClassEF); got != 20 {
		t.Fatalf("EF usage = %d, want 20", got)
	}
	if got := a.Usage(impl.ClassBE); got != 20 {
		t.Fatalf("BE usage = %d, want 20 attributed to the borrowed quota", got)
	}
	if got := a.Stats().Borrowed; got != 20 {
		t.Fatalf("borrowed bytes = %d, want 20", got)
	}
}

func TestBorrowDisabledCapsGrant(t *testing.T) {
	a := newArbiter(t, 100, 20, 30, 50)
	if err := a.SetPolicy(impl.ArbiterPolicy{Borrowing: false}); err != nil {
		t.Fatalf("policy must apply, got %v", err)
	}
	d, err := a.Admit(1, impl.ClassEF, 40)
	if err != nil {
		t.Fatalf("admit must succeed, got %v", err)
	}
	if !d.Admitted || d.Granted != 20 {
		t.Fatalf("decision = %+v, want a partial grant of 20 bytes", d)
	}
	if len(d.BorrowedFrom) != 0 {
		t.Fatalf("borrowed from %v while borrowing is disabled", d.BorrowedFrom)
	}
	if got := a.Usage(impl.ClassBE); got != 0 {
		t.Fatalf("BE usage = %d, want 0", got)
	}
	if got := a.Stats().PartialGrants; got != 1 {
		t.Fatalf("partial grant counter = %d, want 1", got)
	}
}

func TestReleaseReturnsBorrowedQuota(t *testing.T) {
	a := newArbiter(t, 100, 20, 30, 50)
	if _, err := a.Admit(1, impl.ClassEF, 40); err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	a.Release(1)
	if got := a.Usage(impl.ClassEF); got != 0 {
		t.Fatalf("EF usage after release = %d, want 0", got)
	}
	if got := a.Usage(impl.ClassBE); got != 0 {
		t.Fatalf("BE usage after release = %d, want the borrowed quota returned", got)
	}
	if got := a.Stats().Releases; got != 1 {
		t.Fatalf("release counter = %d, want 1", got)
	}
	d, err := a.Admit(2, impl.ClassBE, 20)
	if err != nil || !d.Admitted || d.Granted != 20 {
		t.Fatalf("BE admit after the release = (%+v, %v), want 20 bytes", d, err)
	}
	if got := a.Usage(impl.ClassBE); got != 20 {
		t.Fatalf("BE usage after the new admit = %d, want 20", got)
	}
}

func TestReclassRejectsUnknownAndSameClass(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	if _, err := a.Admit(1, impl.ClassAF, 10); err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err := a.Reclass(99, impl.ClassEF); !errors.Is(err, impl.ErrUnknownFlow) {
		t.Fatalf("reclass of an unknown flow must report ErrUnknownFlow, got %v", err)
	}
	if err := a.Reclass(1, impl.ClassAF); !errors.Is(err, impl.ErrSameClass) {
		t.Fatalf("reclass to the same class must report ErrSameClass, got %v", err)
	}
	if err := a.Reclass(1, impl.Class(9)); !errors.Is(err, impl.ErrInvalidClass) {
		t.Fatalf("reclass to an unknown class must report ErrInvalidClass, got %v", err)
	}
	if got := a.Usage(impl.ClassAF); got != 10 {
		t.Fatalf("AF usage after the rejected reclass = %d, want 10", got)
	}
}

func TestReclassRollsBackWhenTargetFull(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	if _, err := a.Admit(1, impl.ClassEF, 50); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if _, err := a.Admit(2, impl.ClassAF, 10); err != nil {
		t.Fatalf("AF admit failed: %v", err)
	}
	if err := a.SetPolicy(impl.ArbiterPolicy{Borrowing: false}); err != nil {
		t.Fatalf("policy must apply, got %v", err)
	}
	if err := a.Reclass(2, impl.ClassEF); !errors.Is(err, impl.ErrClassGuaranteeFull) {
		t.Fatalf("reclass into a full class must report ErrClassGuaranteeFull, got %v", err)
	}
	if got := a.Usage(impl.ClassEF); got != 50 {
		t.Fatalf("EF usage after the rollback = %d, want 50", got)
	}
	if got := a.Usage(impl.ClassAF); got != 10 {
		t.Fatalf("AF usage after the rollback = %d, want the old quota kept", got)
	}
	flows := a.Flows()
	if len(flows) != 2 || flows[0] != 1 || flows[1] != 2 {
		t.Fatalf("flow table after the rollback = %v, want [1 2]", flows)
	}
}

func TestReclassMovesQuota(t *testing.T) {
	a := newArbiter(t, 60, 10, 30, 20)
	if _, err := a.Admit(1, impl.ClassAF, 10); err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err := a.Reclass(1, impl.ClassBE); err != nil {
		t.Fatalf("reclass must succeed, got %v", err)
	}
	if got := a.Usage(impl.ClassAF); got != 0 {
		t.Fatalf("AF usage after reclass = %d, want 0", got)
	}
	if got := a.Usage(impl.ClassBE); got != 10 {
		t.Fatalf("BE usage after reclass = %d, want 10", got)
	}
}

func TestPreemptionSelectsMinimalSet(t *testing.T) {
	a := newArbiter(t, 100, 10, 20, 70)
	if _, err := a.Admit(1, impl.ClassEF, 10); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if _, err := a.Admit(2, impl.ClassAF, 20); err != nil {
		t.Fatalf("AF admit failed: %v", err)
	}
	if d, err := a.Admit(3, impl.ClassBE, 60); err != nil || d.Granted != 60 {
		t.Fatalf("BE admit 3 = (%+v, %v), want 60 bytes", d, err)
	}
	if d, err := a.Admit(4, impl.ClassBE, 10); err != nil || d.Granted != 10 {
		t.Fatalf("BE admit 4 = (%+v, %v), want 10 bytes", d, err)
	}
	d, err := a.Admit(5, impl.ClassEF, 40)
	if err != nil {
		t.Fatalf("waiting EF admit must not fail, got %v", err)
	}
	if d.Admitted {
		t.Fatalf("EF flow 5 must wait while every pool is committed, got %+v", d)
	}
	preemptions := a.Tick(time.Unix(1700000000, 0))
	if len(preemptions) != 1 {
		t.Fatalf("preemptions = %+v, want exactly the single minimal victim", preemptions)
	}
	got := preemptions[0]
	if got.Flow != 3 || got.Class != impl.ClassBE || got.Released != 60 || got.PreemptedBy != 5 {
		t.Fatalf("preemption = %+v, want flow 3/BE/60 released for flow 5", got)
	}
	if got.QueuePosition != 1 {
		t.Fatalf("preempted queue position = %d, want 1", got.QueuePosition)
	}
	if got := a.Usage(impl.ClassBE); got != 50 {
		t.Fatalf("BE usage after preemption = %d, want 50", got)
	}
	if got := a.Usage(impl.ClassEF); got != 10 {
		t.Fatalf("EF usage after preemption = %d, want the old flow only", got)
	}
	if got := a.Stats().Preemptions; got != 1 {
		t.Fatalf("preemption counter = %d, want 1", got)
	}
	flows := a.Flows()
	if len(flows) != 4 || flows[0] != 1 || flows[1] != 2 || flows[2] != 4 || flows[3] != 5 {
		t.Fatalf("flow table after preemption = %v, want [1 2 4 5]", flows)
	}
}

func TestPreemptedFlowRequeuesAndIsRetried(t *testing.T) {
	a := newArbiter(t, 100, 10, 20, 70)
	if _, err := a.Admit(1, impl.ClassEF, 10); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if _, err := a.Admit(2, impl.ClassAF, 20); err != nil {
		t.Fatalf("AF admit failed: %v", err)
	}
	if _, err := a.Admit(3, impl.ClassBE, 60); err != nil {
		t.Fatalf("BE admit failed: %v", err)
	}
	if _, err := a.Admit(4, impl.ClassBE, 10); err != nil {
		t.Fatalf("BE admit failed: %v", err)
	}
	if _, err := a.Admit(5, impl.ClassEF, 40); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if preemptions := a.Tick(time.Unix(1700000000, 0)); len(preemptions) != 1 {
		t.Fatalf("first tick preemptions = %+v, want one victim", preemptions)
	}
	if got := a.Stats().Waiting; got != 1 {
		t.Fatalf("waiting flows after preemption = %d, want the preempted flow back in the queue", got)
	}
	if preemptions := a.Tick(time.Unix(1700000001, 0)); len(preemptions) != 0 {
		t.Fatalf("second tick preemptions = %+v, want none", preemptions)
	}
	if got := a.Usage(impl.ClassBE); got != 70 {
		t.Fatalf("BE usage after the retry = %d, want the freed 20 bytes reused", got)
	}
	if got := a.Stats().Preemptions; got != 1 {
		t.Fatalf("preemption counter = %d, want 1", got)
	}
	if got := a.Stats().Waiting; got != 0 {
		t.Fatalf("waiting flows after the retry = %d, want 0", got)
	}
}

func TestPreemptionStaysOffWhenPolicyDisablesIt(t *testing.T) {
	a := newArbiter(t, 100, 10, 20, 70)
	if _, err := a.Admit(1, impl.ClassEF, 10); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if _, err := a.Admit(2, impl.ClassAF, 20); err != nil {
		t.Fatalf("AF admit failed: %v", err)
	}
	if _, err := a.Admit(3, impl.ClassBE, 70); err != nil {
		t.Fatalf("BE admit failed: %v", err)
	}
	if _, err := a.Admit(4, impl.ClassEF, 30); err != nil {
		t.Fatalf("EF admit failed: %v", err)
	}
	if err := a.SetPolicy(impl.ArbiterPolicy{Borrowing: true, Preemption: false}); err != nil {
		t.Fatalf("policy must apply, got %v", err)
	}
	if preemptions := a.Tick(time.Unix(1700000000, 0)); len(preemptions) != 0 {
		t.Fatalf("disabled preemption returned %+v, want none", preemptions)
	}
	stats := a.Stats()
	if stats.Preemptions != 0 || stats.Waiting != 1 {
		t.Fatalf("stats after the disabled tick = %+v, want no preemption and one waiting flow", stats)
	}
	if got := a.Usage(impl.ClassBE); got != 70 {
		t.Fatalf("BE usage = %d, want 70 untouched", got)
	}
	if got := a.Usage(impl.ClassEF); got != 10 {
		t.Fatalf("EF usage = %d, want 10 untouched", got)
	}
}

func TestSnapshotRestoresBorrowAttribution(t *testing.T) {
	a := newArbiter(t, 100, 20, 30, 50)
	if _, err := a.Admit(1, impl.ClassEF, 40); err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := newArbiter(t, 100, 20, 30, 50)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Usage(impl.ClassEF); got != 20 {
		t.Fatalf("restored EF usage = %d, want 20", got)
	}
	if got := restored.Usage(impl.ClassBE); got != 20 {
		t.Fatalf("restored BE attribution = %d, want 20", got)
	}
	if got := restored.Stats().Borrowed; got != 20 {
		t.Fatalf("restored borrowed counter = %d, want 20", got)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot must report ErrBadSnapshot, got %v", err)
	}
	if got := restored.Usage(impl.ClassBE); got != 20 {
		t.Fatalf("BE attribution after a rejected restore = %d, want 20", got)
	}
	restored.Release(1)
	if got := restored.Usage(impl.ClassEF); got != 0 {
		t.Fatalf("EF usage after release = %d, want 0", got)
	}
	if got := restored.Usage(impl.ClassBE); got != 0 {
		t.Fatalf("BE attribution after release = %d, want the borrowed quota returned to BE", got)
	}
}

func TestConcurrentAdmitReleaseKeepsInvariant(t *testing.T) {
	a := newArbiter(t, 100, 50, 30, 20)
	const workers = 6
	const rounds = 30
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				flow := impl.FlowID(worker*1000 + i + 1)
				d, err := a.Admit(flow, impl.ClassEF, 5)
				if err != nil {
					errCh <- fmt.Errorf("admit %d failed: %w", flow, err)
					return
				}
				if !d.Admitted || d.Granted != 5 {
					errCh <- fmt.Errorf("admit %d = %+v, want five bytes", flow, d)
					return
				}
				total := a.Usage(impl.ClassEF) + a.Usage(impl.ClassAF) + a.Usage(impl.ClassBE)
				if total > 100 {
					errCh <- fmt.Errorf("usage %d exceeds the link capacity", total)
					return
				}
				a.Release(flow)
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent run failed: %v", err)
	}
	if got := a.Usage(impl.ClassEF) + a.Usage(impl.ClassAF) + a.Usage(impl.ClassBE); got != 0 {
		t.Fatalf("usage after the run = %d, want 0", got)
	}
	if got := a.Stats().Grants; got != workers*rounds {
		t.Fatalf("grant counter = %d, want %d", got, workers*rounds)
	}
}
