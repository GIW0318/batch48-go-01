package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-17"
)

func TestRegisterPropStoresOwnerAndRejectsDuplicates(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	owner, err := a.Owner(1)
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if owner != 10 {
		t.Fatalf("owner = %d, want 10", owner)
	}
	if err := a.RegisterProp(1, 10, 0); !errors.Is(err, impl.ErrEntityExists) {
		t.Fatalf("duplicate prop error = %v, want ErrEntityExists", err)
	}
	if got := a.Stats().Props; got != 1 {
		t.Fatalf("prop count = %d, want 1", got)
	}
}

func TestRequestByNonOwnerRejected(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 11, 12); !errors.Is(err, impl.ErrNotOwner) {
		t.Fatalf("request by non owner error = %v, want ErrNotOwner", err)
	}
	if _, pending := a.Pending(1); pending {
		t.Fatalf("rejected request must not create a pending transfer")
	}
}

func TestRequestRejectsSelfTransfer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 10); !errors.Is(err, impl.ErrSelfTransfer) {
		t.Fatalf("self transfer error = %v, want ErrSelfTransfer", err)
	}
}

func TestRequestReturnsDistinctTransferIDs(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	first, err := a.Request(1, 10, 20)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if first == 0 {
		t.Fatalf("transfer id must not be zero")
	}
	if err := a.Cancel(first); err != nil {
		t.Fatalf("cancel first transfer: %v", err)
	}
	second, err := a.Request(1, 10, 30)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if second == first {
		t.Fatalf("transfer id %d was reused", second)
	}
	if got := a.Stats().PendingTransfers; got != 1 {
		t.Fatalf("pending transfers = %d, want 1", got)
	}
}

func TestOldOwnerInputAppliesDuringTransfer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 10, 1, impl.Command{Name: "dash"}); err != nil {
		t.Fatalf("old owner input during transfer rejected: %v", err)
	}
	if got := a.Buffered(1); got != 0 {
		t.Fatalf("old owner input must not enter the transfer buffer, buffered = %d", got)
	}
}

func TestNewOwnerInputBufferedDuringTransfer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 20, 5, impl.Command{Name: "aim"}); err != nil {
		t.Fatalf("first buffered input rejected: %v", err)
	}
	if err := a.Input(1, 20, 6, impl.Command{Name: "fire"}); err != nil {
		t.Fatalf("second buffered input rejected: %v", err)
	}
	if got := a.Buffered(1); got != 2 {
		t.Fatalf("buffered inputs = %d, want 2", got)
	}
	if got := a.Stats().BufferedInputs; got != 2 {
		t.Fatalf("stats buffered inputs = %d, want 2", got)
	}
}

func TestUnrelatedPlayerInputRejected(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 99, 5, impl.Command{Name: "aim"}); !errors.Is(err, impl.ErrNotOwner) {
		t.Fatalf("unrelated player input error = %v, want ErrNotOwner", err)
	}
}

func TestCommitSwitchesOwnerAndClearsBuffer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 20, 5, impl.Command{Name: "aim"}); err != nil {
		t.Fatalf("buffer input: %v", err)
	}
	if err := a.Commit(1, 6); err != nil {
		t.Fatalf("commit transfer: %v", err)
	}
	owner, err := a.Owner(1)
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if owner != 20 {
		t.Fatalf("owner = %d, want 20 after commit", owner)
	}
	if _, pending := a.Pending(1); pending {
		t.Fatalf("pending transfer survived commit")
	}
	if got := a.Buffered(1); got != 0 {
		t.Fatalf("buffered inputs = %d, want 0 after commit", got)
	}
}

func TestOldOwnerInputAfterCommitRejectedAndCounted(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Commit(1, 1); err != nil {
		t.Fatalf("commit transfer: %v", err)
	}
	if err := a.Input(1, 10, 7, impl.Command{Name: "fire"}); !errors.Is(err, impl.ErrNotOwner) {
		t.Fatalf("old owner input after commit error = %v, want ErrNotOwner", err)
	}
	if got := a.RejectedInputs(1); got != 1 {
		t.Fatalf("rejected inputs = %d, want 1", got)
	}
}

func TestStaleSequenceRejected(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if err := a.Input(1, 10, 3, impl.Command{Name: "fire"}); err != nil {
		t.Fatalf("first input: %v", err)
	}
	if err := a.Input(1, 10, 3, impl.Command{Name: "fire"}); !errors.Is(err, impl.ErrStaleInput) {
		t.Fatalf("repeated sequence error = %v, want ErrStaleInput", err)
	}
	if err := a.Input(1, 10, 2, impl.Command{Name: "fire"}); !errors.Is(err, impl.ErrStaleInput) {
		t.Fatalf("older sequence error = %v, want ErrStaleInput", err)
	}
}

func TestCommitWithoutPendingTransferFails(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if err := a.Commit(1, 1); !errors.Is(err, impl.ErrNoPendingTransfer) {
		t.Fatalf("commit without pending error = %v, want ErrNoPendingTransfer", err)
	}
}

func TestCommitBehindBufferedSequenceFails(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 20, 9, impl.Command{Name: "aim"}); err != nil {
		t.Fatalf("buffer input: %v", err)
	}
	if err := a.Commit(1, 8); !errors.Is(err, impl.ErrCommitSeqInvalid) {
		t.Fatalf("commit behind buffer error = %v, want ErrCommitSeqInvalid", err)
	}
	if _, pending := a.Pending(1); !pending {
		t.Fatalf("invalid commit must keep the transfer pending")
	}
	if owner, err := a.Owner(1); err != nil || owner != 10 {
		t.Fatalf("owner after invalid commit = %d err=%v, want 10", owner, err)
	}
	if got := a.Buffered(1); got != 1 {
		t.Fatalf("buffered inputs = %d, want 1 after invalid commit", got)
	}
}

func TestCancelKeepsOriginalOwner(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	id, err := a.Request(1, 10, 20)
	if err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 20, 5, impl.Command{Name: "aim"}); err != nil {
		t.Fatalf("buffer input: %v", err)
	}
	if err := a.Cancel(id); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	owner, err := a.Owner(1)
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if owner != 10 {
		t.Fatalf("owner = %d, want 10 after cancel", owner)
	}
	if _, pending := a.Pending(1); pending {
		t.Fatalf("pending transfer survived cancel")
	}
	if got := a.Buffered(1); got != 0 {
		t.Fatalf("buffered inputs = %d, want 0 after cancel", got)
	}
	if err := a.Commit(1, 10); !errors.Is(err, impl.ErrNoPendingTransfer) {
		t.Fatalf("commit after cancel error = %v, want ErrNoPendingTransfer", err)
	}
}

func TestSnapshotRestoreDropsUnfinishedTransfer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	if _, err := a.Request(1, 10, 20); err != nil {
		t.Fatalf("request transfer: %v", err)
	}
	if err := a.Input(1, 20, 5, impl.Command{Name: "aim"}); err != nil {
		t.Fatalf("buffer input: %v", err)
	}
	payload, err := a.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewAuthorityTransfer()
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	owner, err := restored.Owner(1)
	if err != nil {
		t.Fatalf("owner after restore: %v", err)
	}
	if owner != 10 {
		t.Fatalf("owner after restore = %d, want 10", owner)
	}
	if _, pending := restored.Pending(1); pending {
		t.Fatalf("unfinished transfer must not survive restore")
	}
	if got := restored.Buffered(1); got != 0 {
		t.Fatalf("buffered inputs after restore = %d, want 0", got)
	}
	if err := restored.Commit(1, 6); !errors.Is(err, impl.ErrNoPendingTransfer) {
		t.Fatalf("commit after restore error = %v, want ErrNoPendingTransfer", err)
	}
	if err := restored.Input(1, 20, 6, impl.Command{Name: "aim"}); !errors.Is(err, impl.ErrNotOwner) {
		t.Fatalf("new owner input after restore error = %v, want ErrNotOwner", err)
	}
}

func TestConcurrentRequestsAllowSingleTransfer(t *testing.T) {
	a := impl.NewAuthorityTransfer()
	if err := a.RegisterProp(1, 10, 0); err != nil {
		t.Fatalf("register prop: %v", err)
	}
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		successes int
		ids       = make(map[impl.TransferID]bool)
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id, err := a.Request(1, 10, impl.PlayerID(20+n))
			if err != nil {
				if !errors.Is(err, impl.ErrTransferPending) {
					t.Errorf("request %d error = %v, want ErrTransferPending", n, err)
				}
				return
			}
			mu.Lock()
			successes++
			ids[id] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful requests = %d, want exactly 1", successes)
	}
	if len(ids) != 1 {
		t.Fatalf("distinct transfer ids = %d, want 1", len(ids))
	}
}
