package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-15"
)

func wholeDisk(device string) impl.Request {
	return impl.Request{DeviceID: device, Scope: impl.ScopeWholeDisk, Method: "overwrite"}
}

func TestSubmitRejectsUnknownDevice(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	if _, err := o.Submit(wholeDisk("disk-x")); !errors.Is(err, impl.ErrDeviceUnknown) {
		t.Fatalf("expected ErrDeviceUnknown, got %v", err)
	}
	if got := o.Queue(); len(got) != 0 {
		t.Fatalf("rejected submission queued %v, want nothing", got)
	}
}

func TestSubmitRejectsSecondActiveTaskForDevice(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	if err := o.AddDevice("disk-a"); err != nil {
		t.Fatalf("adding a device failed: %v", err)
	}
	if _, err := o.Submit(wholeDisk("disk-a")); err != nil {
		t.Fatalf("first submission failed: %v", err)
	}
	if _, err := o.Submit(wholeDisk("disk-a")); !errors.Is(err, impl.ErrDeviceBusy) {
		t.Fatalf("expected ErrDeviceBusy for a second task, got %v", err)
	}
	if got := o.Queue(); len(got) != 1 {
		t.Fatalf("queue is %v, want one task", got)
	}
}

func TestSubmitRejectsRetiringDevice(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	if err := o.AddDevice("disk-b"); err != nil {
		t.Fatalf("adding a device failed: %v", err)
	}
	if err := o.SetDeviceState("disk-b", impl.DeviceRetiring); err != nil {
		t.Fatalf("moving the device to retiring failed: %v", err)
	}
	if _, err := o.Submit(wholeDisk("disk-b")); !errors.Is(err, impl.ErrDeviceState) {
		t.Fatalf("expected ErrDeviceState for a retiring device, got %v", err)
	}
}

func TestStartRespectsConcurrencyLimit(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 1, MaxRetries: 2})
	o.AddDevice("disk-a")
	o.AddDevice("disk-b")
	first, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("first submission failed: %v", err)
	}
	second, err := o.Submit(wholeDisk("disk-b"))
	if err != nil {
		t.Fatalf("second submission failed: %v", err)
	}
	if _, err := o.Start(first.ID); err != nil {
		t.Fatalf("starting the first task failed: %v", err)
	}
	if _, err := o.Start(second.ID); !errors.Is(err, impl.ErrRunningLimit) {
		t.Fatalf("expected ErrRunningLimit with quota 1, got %v", err)
	}
	queue := o.Queue()
	if len(queue) != 1 || queue[0] != second.ID {
		t.Fatalf("queue after quota rejection is %v, want [%d]", queue, second.ID)
	}
}

func TestStartRejectsTaskAlreadyRunning(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	if _, err := o.Start(task.ID); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if _, err := o.Start(task.ID); !errors.Is(err, impl.ErrBadTransition) {
		t.Fatalf("expected ErrBadTransition on a running task, got %v", err)
	}
}

func TestCancelPendingTaskImmediately(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	cancelled, err := o.Cancel(task.ID)
	if err != nil {
		t.Fatalf("cancelling a pending task failed: %v", err)
	}
	if cancelled.State != impl.TaskCancelled {
		t.Fatalf("cancelled task state is %q, want %q", cancelled.State, impl.TaskCancelled)
	}
	if got := o.Queue(); len(got) != 0 {
		t.Fatalf("cancelled task stayed in the queue: %v", got)
	}
	if _, err := o.Start(task.ID); !errors.Is(err, impl.ErrBadTransition) {
		t.Fatalf("expected ErrBadTransition when starting a cancelled task, got %v", err)
	}
}

func TestVerifyRejectsMismatchedDigest(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	if _, err := o.Start(task.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.Verify(task.ID, "deadbeef"); !errors.Is(err, impl.ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for a bare digest, got %v", err)
	}
	receipt, err := o.Verify(task.ID, "sha256:deadbeef")
	if err != nil {
		t.Fatalf("verify with a well formed digest failed: %v", err)
	}
	if receipt.TaskID != task.ID {
		t.Fatalf("receipt points at task %d, want %d", receipt.TaskID, task.ID)
	}
	state, err := o.Task(task.ID)
	if err != nil {
		t.Fatalf("reading the task failed: %v", err)
	}
	if state.State != impl.TaskVerified {
		t.Fatalf("task state after verify is %q, want %q", state.State, impl.TaskVerified)
	}
}

func TestCompleteRequiresVerified(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	if _, err := o.Start(task.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.Complete(task.ID); !errors.Is(err, impl.ErrBadTransition) {
		t.Fatalf("expected ErrBadTransition completing a running task, got %v", err)
	}
	if got := o.Receipts(); len(got) != 0 {
		t.Fatalf("failed completion wrote %d receipts, want 0", len(got))
	}
}

func TestCompleteIssuesExactlyOneReceipt(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	if _, err := o.Start(task.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.Verify(task.ID, "sha256:0011"); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	receipt, err := o.Complete(task.ID)
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if receipt.Seq != 1 || receipt.TaskID != task.ID || receipt.Digest != "sha256:0011" {
		t.Fatalf("receipt is %+v, want seq 1 task %d digest sha256:0011", receipt, task.ID)
	}
	if _, err := o.Complete(task.ID); !errors.Is(err, impl.ErrReceiptExists) {
		t.Fatalf("expected ErrReceiptExists on the second completion, got %v", err)
	}
	if got := o.Receipts(); len(got) != 1 {
		t.Fatalf("receipt ledger holds %d entries, want 1", len(got))
	}
}

func TestFailRetriesThenRejects(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	task, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission failed: %v", err)
	}
	for round := 1; round <= 2; round++ {
		if _, err := o.Start(task.ID); err != nil {
			t.Fatalf("start round %d failed: %v", round, err)
		}
		failed, err := o.Fail(task.ID)
		if err != nil {
			t.Fatalf("fail round %d returned %v, want a requeue", round, err)
		}
		if failed.Attempts != round {
			t.Fatalf("attempts after round %d is %d, want %d", round, failed.Attempts, round)
		}
		if failed.State != impl.TaskPending {
			t.Fatalf("task state after round %d is %q, want pending", round, failed.State)
		}
	}
	if _, err := o.Start(task.ID); err != nil {
		t.Fatalf("final start failed: %v", err)
	}
	if _, err := o.Fail(task.ID); !errors.Is(err, impl.ErrRetryLimit) {
		t.Fatalf("expected ErrRetryLimit after 2 retries, got %v", err)
	}
	final, err := o.Task(task.ID)
	if err != nil {
		t.Fatalf("reading the task failed: %v", err)
	}
	if final.State != impl.TaskCancelled {
		t.Fatalf("exhausted task state is %q, want cancelled", final.State)
	}
}

func TestSetDeviceStateRejectsUnknownDeviceAndState(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	if err := o.SetDeviceState("disk-x", impl.DeviceRetiring); !errors.Is(err, impl.ErrDeviceUnknown) {
		t.Fatalf("expected ErrDeviceUnknown, got %v", err)
	}
	o.AddDevice("disk-a")
	if err := o.SetDeviceState("disk-a", impl.DeviceState("napping")); !errors.Is(err, impl.ErrDeviceState) {
		t.Fatalf("expected ErrDeviceState for an unknown state, got %v", err)
	}
	info := o.Device("disk-a")
	if info.State != impl.DeviceOnline {
		t.Fatalf("rejected state change moved the device to %q, want online", info.State)
	}
}

func TestQueueKeepsSubmissionOrder(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	o.AddDevice("disk-b")
	o.AddDevice("disk-c")
	first, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission a failed: %v", err)
	}
	second, err := o.Submit(wholeDisk("disk-b"))
	if err != nil {
		t.Fatalf("submission b failed: %v", err)
	}
	third, err := o.Submit(wholeDisk("disk-c"))
	if err != nil {
		t.Fatalf("submission c failed: %v", err)
	}
	queue := o.Queue()
	want := []int{first.ID, second.ID, third.ID}
	if len(queue) != len(want) {
		t.Fatalf("queue is %v, want %v", queue, want)
	}
	for i := range want {
		if queue[i] != want[i] {
			t.Fatalf("queue position %d is %d, want %d", i, queue[i], want[i])
		}
	}
	if _, err := o.Start(first.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	queue = o.Queue()
	if len(queue) != 2 || queue[0] != second.ID || queue[1] != third.ID {
		t.Fatalf("queue after start is %v, want [%d %d]", queue, second.ID, third.ID)
	}
}

func TestSnapshotRestoreKeepsQueueOrder(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	o.AddDevice("disk-b")
	o.AddDevice("disk-c")
	var ids []int
	for _, device := range []string{"disk-a", "disk-b", "disk-c"} {
		task, err := o.Submit(wholeDisk(device))
		if err != nil {
			t.Fatalf("submission for %s failed: %v", device, err)
		}
		ids = append(ids, task.ID)
	}
	snap, err := o.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	queue := other.Queue()
	if len(queue) != 3 {
		t.Fatalf("restored queue is %v, want three tasks", queue)
	}
	for i := range ids {
		if queue[i] != ids[i] {
			t.Fatalf("restored queue position %d is %d, want %d", i, queue[i], ids[i])
		}
	}
	if _, err := other.Start(ids[0]); err != nil {
		t.Fatalf("starting the first restored task failed: %v", err)
	}
}

func TestSnapshotRestoreKeepsRunningSlots(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 1, MaxRetries: 2})
	o.AddDevice("disk-a")
	o.AddDevice("disk-b")
	first, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission a failed: %v", err)
	}
	second, err := o.Submit(wholeDisk("disk-b"))
	if err != nil {
		t.Fatalf("submission b failed: %v", err)
	}
	if _, err := o.Start(first.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	snap, err := o.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 1, MaxRetries: 2})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if _, err := other.Start(second.ID); !errors.Is(err, impl.ErrRunningLimit) {
		t.Fatalf("restored orchestrator must keep the running slot, got %v", err)
	}
	queue := other.Queue()
	if len(queue) != 1 || queue[0] != second.ID {
		t.Fatalf("restored queue is %v, want [%d]", queue, second.ID)
	}
}

func TestStatsCountsLifecycle(t *testing.T) {
	o := impl.NewEraseOrchestrator(impl.Limits{MaxRunning: 2, MaxRetries: 2})
	o.AddDevice("disk-a")
	o.AddDevice("disk-b")
	first, err := o.Submit(wholeDisk("disk-a"))
	if err != nil {
		t.Fatalf("submission a failed: %v", err)
	}
	second, err := o.Submit(wholeDisk("disk-b"))
	if err != nil {
		t.Fatalf("submission b failed: %v", err)
	}
	if _, err := o.Start(first.ID); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := o.Verify(first.ID, "sha256:99aa"); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if _, err := o.Complete(first.ID); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if _, err := o.Cancel(second.ID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	stats := o.Stats()
	if stats.Submitted != 2 || stats.Started != 1 || stats.Completed != 1 || stats.Cancelled != 1 {
		t.Fatalf("stats are %+v, want submitted=2 started=1 completed=1 cancelled=1", stats)
	}
}
