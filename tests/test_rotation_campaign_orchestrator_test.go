package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-08"
)

func makeTargets(n int) []impl.Target {
	targets := make([]impl.Target, 0, n)
	for i := 0; i < n; i++ {
		targets = append(targets, impl.Target{ID: fmt.Sprintf("obj-%02d", i), KeyID: fmt.Sprintf("key-%02d", i)})
	}
	return targets
}

func plannedCampaign(t *testing.T, o *impl.RotationCampaignOrchestrator, tenant string, total, batchSize int) impl.Campaign {
	t.Helper()
	campaign, err := o.Plan(tenant, makeTargets(total), batchSize)
	if err != nil {
		t.Fatalf("planning the campaign failed: %v", err)
	}
	return campaign
}

func TestPlanBuildsBatches(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if campaign.ID == "" || campaign.TenantID != "tenant-a" {
		t.Fatalf("campaign = %+v, want a non-empty id for tenant-a", campaign)
	}
	if campaign.Total != 12 || campaign.BatchSize != 5 || campaign.BatchCount != 3 {
		t.Fatalf("campaign = %+v, want 12 targets in 3 batches of 5", campaign)
	}
	if campaign.Status != "planned" {
		t.Fatalf("planned status = %q, want planned", campaign.Status)
	}
	progress, err := o.Progress(campaign.ID)
	if err != nil {
		t.Fatalf("reading progress failed: %v", err)
	}
	if progress != 0 {
		t.Fatalf("fresh campaign progress = %d, want 0", progress)
	}
}

func TestPlanRejectsInvalidInput(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{MaxTargets: 3})
	if _, err := o.Plan("", makeTargets(2), 1); !errors.Is(err, impl.ErrInvalidPlan) {
		t.Fatalf("empty tenant = %v, want ErrInvalidPlan", err)
	}
	if _, err := o.Plan("tenant-a", nil, 1); !errors.Is(err, impl.ErrInvalidPlan) {
		t.Fatalf("empty target list = %v, want ErrInvalidPlan", err)
	}
	if _, err := o.Plan("tenant-a", makeTargets(2), 0); !errors.Is(err, impl.ErrInvalidPlan) {
		t.Fatalf("zero batch size = %v, want ErrInvalidPlan", err)
	}
	duplicated := []impl.Target{{ID: "obj-00"}, {ID: "obj-00"}}
	if _, err := o.Plan("tenant-a", duplicated, 1); !errors.Is(err, impl.ErrInvalidPlan) {
		t.Fatalf("duplicated target ids = %v, want ErrInvalidPlan", err)
	}
	if _, err := o.Plan("tenant-a", makeTargets(4), 1); !errors.Is(err, impl.ErrTargetLimit) {
		t.Fatalf("plan over the target limit = %v, want ErrTargetLimit", err)
	}
}

func TestPlanAllowsOneActiveCampaignPerTenant(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	first := plannedCampaign(t, o, "tenant-a", 4, 2)
	if _, err := o.Plan("tenant-a", makeTargets(4), 2); !errors.Is(err, impl.ErrCampaignExists) {
		t.Fatalf("second campaign for the same tenant = %v, want ErrCampaignExists", err)
	}
	if _, err := o.Plan("tenant-b", makeTargets(4), 2); err != nil {
		t.Fatalf("another tenant must be able to plan, got %v", err)
	}
	if _, err := o.Rollback(first.ID); err != nil {
		t.Fatalf("rolling back a planned campaign failed: %v", err)
	}
	if _, err := o.Plan("tenant-a", makeTargets(4), 2); err != nil {
		t.Fatalf("planning after the rollback failed: %v", err)
	}
}

func TestStartRequiresPlannedState(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 4, 2)
	if err := o.Start("rc-missing-1"); !errors.Is(err, impl.ErrCampaignNotFound) {
		t.Fatalf("starting an unknown campaign = %v, want ErrCampaignNotFound", err)
	}
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("starting a planned campaign failed: %v", err)
	}
	status, err := o.Status(campaign.ID)
	if err != nil {
		t.Fatalf("reading status failed: %v", err)
	}
	if status != "running" {
		t.Fatalf("status after start = %q, want running", status)
	}
	if err := o.Start(campaign.ID); !errors.Is(err, impl.ErrInvalidTransition) {
		t.Fatalf("starting twice = %v, want ErrInvalidTransition", err)
	}
}

func TestApplyBatchRequiresRunningState(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 4, 2)
	if _, err := o.ApplyBatch(campaign.ID); !errors.Is(err, impl.ErrInvalidTransition) {
		t.Fatalf("applying before start = %v, want ErrInvalidTransition", err)
	}
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	result, err := o.ApplyBatch(campaign.ID)
	if err != nil {
		t.Fatalf("applying the first batch failed: %v", err)
	}
	if result.Batch != 0 {
		t.Fatalf("first dispatched batch index = %d, want 0", result.Batch)
	}
}

func TestApplyBatchDispatchesTargetsInOrder(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	result, err := o.ApplyBatch(campaign.ID)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	want := []string{"obj-00", "obj-01", "obj-02", "obj-03", "obj-04"}
	if len(result.Dispatched) != len(want) {
		t.Fatalf("dispatched %d targets, want %d", len(result.Dispatched), len(want))
	}
	for i, id := range want {
		if result.Dispatched[i] != id {
			t.Fatalf("dispatched target %d = %q, want %q", i, result.Dispatched[i], id)
		}
	}
	if result.BatchCount != 3 {
		t.Fatalf("batch count = %d, want 3", result.BatchCount)
	}
	if progress, err := o.Progress(campaign.ID); err != nil || progress != 0 {
		t.Fatalf("progress before the callback = (%d, %v), want (0, nil)", progress, err)
	}
}

func TestReportBatchCountsProgress(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	result, err := o.ReportBatch(campaign.ID, 0, nil)
	if err != nil {
		t.Fatalf("report failed: %v", err)
	}
	if result.Progress != 5 {
		t.Fatalf("reported progress = %d, want 5", result.Progress)
	}
	if result.Status != "running" {
		t.Fatalf("status after one reported batch = %q, want running", result.Status)
	}
	progress, err := o.Progress(campaign.ID)
	if err != nil {
		t.Fatalf("reading progress failed: %v", err)
	}
	if progress != 5 {
		t.Fatalf("campaign progress = %d, want 5", progress)
	}
}

func TestReportBatchRejectsUndispatchedBatch(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 1, nil); !errors.Is(err, impl.ErrBatchNotDispatched) {
		t.Fatalf("reporting an undispatched batch = %v, want ErrBatchNotDispatched", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 9, nil); !errors.Is(err, impl.ErrBatchNotDispatched) {
		t.Fatalf("reporting an out of range batch = %v, want ErrBatchNotDispatched", err)
	}
	if progress, err := o.Progress(campaign.ID); err != nil || progress != 0 {
		t.Fatalf("rejected report changed progress to (%d, %v)", progress, err)
	}
}

func TestReportBatchIsNotCountedTwice(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 0, nil); err != nil {
		t.Fatalf("first report failed: %v", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 0, nil); !errors.Is(err, impl.ErrBatchAlreadyReported) {
		t.Fatalf("duplicate report = %v, want ErrBatchAlreadyReported", err)
	}
	progress, err := o.Progress(campaign.ID)
	if err != nil {
		t.Fatalf("reading progress failed: %v", err)
	}
	if progress != 5 {
		t.Fatalf("duplicate report changed progress to %d, want 5", progress)
	}
}

func TestReportBatchRecordsFailures(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{FailureRateThreshold: 0.6})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	result, err := o.ReportBatch(campaign.ID, 0, []string{"obj-03"})
	if err != nil {
		t.Fatalf("report failed: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "obj-03" {
		t.Fatalf("failed targets = %v, want [obj-03]", result.Failed)
	}
	if result.Progress != 4 {
		t.Fatalf("progress with one failed target = %d, want 4", result.Progress)
	}
	if result.Status != "running" {
		t.Fatalf("a failure below the threshold changed status to %q", result.Status)
	}
	if _, err := o.ReportBatch(campaign.ID, 0, []string{"obj-99"}); !errors.Is(err, impl.ErrBatchAlreadyReported) {
		t.Fatalf("re-reporting a batch = %v, want ErrBatchAlreadyReported", err)
	}
}

func TestPauseBlocksDispatchUntilResume(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Pause(campaign.ID); !errors.Is(err, impl.ErrInvalidTransition) {
		t.Fatalf("pausing a planned campaign = %v, want ErrInvalidTransition", err)
	}
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := o.Pause(campaign.ID); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if status, _ := o.Status(campaign.ID); status != "paused" {
		t.Fatalf("status after pause = %q, want paused", status)
	}
	if _, err := o.ApplyBatch(campaign.ID); !errors.Is(err, impl.ErrInvalidTransition) {
		t.Fatalf("applying while paused = %v, want ErrInvalidTransition", err)
	}
	if err := o.Resume(campaign.ID); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	result, err := o.ApplyBatch(campaign.ID)
	if err != nil {
		t.Fatalf("applying after resume failed: %v", err)
	}
	if result.Batch != 0 {
		t.Fatalf("batch after resume = %d, want 0", result.Batch)
	}
}

func TestApplyBatchStopsWhenExhausted(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := o.ApplyBatch(campaign.ID); err != nil {
			t.Fatalf("apply batch %d failed: %v", i, err)
		}
	}
	if _, err := o.ApplyBatch(campaign.ID); !errors.Is(err, impl.ErrNoMoreBatches) {
		t.Fatalf("applying after the last batch = %v, want ErrNoMoreBatches", err)
	}
}

func TestRollbackRevertsReportedBatches(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 0, nil); err != nil {
		t.Fatalf("report failed: %v", err)
	}
	reverted, err := o.Rollback(campaign.ID)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if reverted != 5 {
		t.Fatalf("rollback reverted %d targets, want the 5 applied ones", reverted)
	}
	if status, _ := o.Status(campaign.ID); status != "rolled_back" {
		t.Fatalf("status after rollback = %q, want rolled_back", status)
	}
	if progress, _ := o.Progress(campaign.ID); progress != 0 {
		t.Fatalf("progress after rollback = %d, want 0", progress)
	}
	if _, err := o.Rollback(campaign.ID); !errors.Is(err, impl.ErrInvalidTransition) {
		t.Fatalf("rolling back twice = %v, want ErrInvalidTransition", err)
	}
	if _, err := o.Plan("tenant-a", makeTargets(4), 2); err != nil {
		t.Fatalf("planning after the rollback failed: %v", err)
	}
}

func TestSnapshotRestoreResumesCampaign(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if _, err := o.ReportBatch(campaign.ID, 0, nil); err != nil {
		t.Fatalf("first report failed: %v", err)
	}
	if _, err := o.ApplyBatch(campaign.ID); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	data, err := o.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if status, err := restored.Status(campaign.ID); err != nil || status != "running" {
		t.Fatalf("restored status = (%q, %v), want (running, nil)", status, err)
	}
	if progress, err := restored.Progress(campaign.ID); err != nil || progress != 5 {
		t.Fatalf("restored progress = (%d, %v), want (5, nil)", progress, err)
	}
	result, err := restored.ApplyBatch(campaign.ID)
	if err != nil {
		t.Fatalf("applying after restore failed: %v", err)
	}
	if result.Batch != 2 {
		t.Fatalf("batch dispatched after restore = %d, want 2", result.Batch)
	}
	if _, err := restored.ReportBatch(campaign.ID, 1, nil); err != nil {
		t.Fatalf("reporting the restored pending batch failed: %v", err)
	}
	if _, err := restored.ReportBatch(campaign.ID, 2, nil); err != nil {
		t.Fatalf("reporting the final batch failed: %v", err)
	}
	if status, _ := restored.Status(campaign.ID); status != "completed" {
		t.Fatalf("status after the last report = %q, want completed", status)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restoring garbage = %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentApplyBatchDispatchesDistinctBatches(t *testing.T) {
	o := impl.NewRotationCampaignOrchestrator(impl.OrchestratorOptions{})
	campaign := plannedCampaign(t, o, "tenant-a", 12, 5)
	if err := o.Start(campaign.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	const workers = 8
	batches := make([]int, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := o.ApplyBatch(campaign.ID)
			batches[idx] = result.Batch
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	seen := make(map[int]bool)
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			wins++
			if seen[batches[i]] {
				t.Fatalf("batch %d was dispatched twice", batches[i])
			}
			seen[batches[i]] = true
			continue
		}
		if !errors.Is(errs[i], impl.ErrNoMoreBatches) {
			t.Fatalf("worker %d got %v, want nil or ErrNoMoreBatches", i, errs[i])
		}
	}
	if wins != 3 {
		t.Fatalf("%d concurrent dispatches succeeded, want exactly 3", wins)
	}
	for batch := 0; batch < 3; batch++ {
		if !seen[batch] {
			t.Fatalf("batch %d was never dispatched", batch)
		}
	}
}
