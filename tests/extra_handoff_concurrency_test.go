package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-b-02"
)

func TestExtraConcurrentAcceptHasOneWinner(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{7, 9}, impl.Meta{Method: "GET", Path: "/once"})

	const contenders = 32
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := g.Accept(nonce, peer(42))
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, impl.ErrNonceConsumed):
		default:
			t.Fatalf("concurrent Accept error = %v, want nil or ErrNonceConsumed", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful Accepts = %d, want 1", successes)
	}
	if got := g.Pending(); got != 0 {
		t.Fatalf("Pending() = %d, want 0", got)
	}
	if got := g.Stats().Accepted; got != 1 {
		t.Fatalf("Stats.Accepted = %d, want 1", got)
	}
}

func TestExtraRevokeAndAcceptRaceHasOneWinner(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	for i := 0; i < 200; i++ {
		nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{Method: "PUT", Path: "/race"})

		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, acceptErr = g.Accept(nonce, peer(42))
		}()
		go func() {
			defer wg.Done()
			<-start
			revokeErr = g.Revoke(nonce)
		}()
		close(start)
		wg.Wait()

		switch {
		case acceptErr == nil:
			if !errors.Is(revokeErr, impl.ErrUnknownNonce) {
				t.Fatalf("revoke after accept error = %v, want ErrUnknownNonce", revokeErr)
			}
		case revokeErr == nil:
			if !errors.Is(acceptErr, impl.ErrUnknownNonce) {
				t.Fatalf("accept after revoke error = %v, want ErrUnknownNonce", acceptErr)
			}
		default:
			t.Fatalf("both race participants failed: accept=%v revoke=%v", acceptErr, revokeErr)
		}
		if got := g.Pending(); got != 0 {
			t.Fatalf("Pending() after race = %d, want 0", got)
		}
		if _, err := g.Accept(nonce, peer(42)); err == nil {
			t.Fatal("nonce was handed out a second time after the race")
		}
	}
}

func TestExtraSnapshotRestartAndPidReuse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	opts := baseOptions()
	opts.Now = func() time.Time { return now }

	g := impl.NewHelperHandoffGuard(opts)
	meta := impl.Meta{Method: "POST", Path: "/restart", Body: []byte("payload")}
	nonce := mustOffer(t, g, peer(42), []int{11, 12}, meta)
	before, err := g.Inspect(nonce)
	if err != nil {
		t.Fatalf("Inspect before snapshot: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restarted := impl.NewHelperHandoffGuard(opts)
	if err := restarted.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := restarted.Restore(snap); err != nil {
		t.Fatalf("repeated Restore: %v", err)
	}
	if got := restarted.Pending(); got != 1 {
		t.Fatalf("Pending() after restore = %d, want 1", got)
	}
	after, err := restarted.Inspect(nonce)
	if err != nil {
		t.Fatalf("Inspect after restore: %v", err)
	}
	if after.Digest != before.Digest || !after.IssuedAt.Equal(before.IssuedAt) {
		t.Fatalf("restored offer changed: before=%+v after=%+v", before, after)
	}

	reusedPID := peer(42)
	reusedPID.StartedAt = reusedPID.StartedAt.Add(time.Second)
	if _, err := restarted.Accept(nonce, reusedPID); !errors.Is(err, impl.ErrPeerMismatch) {
		t.Fatalf("Accept by a reused pid error = %v, want ErrPeerMismatch", err)
	}
	if got := restarted.Pending(); got != 1 {
		t.Fatalf("Pending() after a peer mismatch = %d, want 1", got)
	}
	if _, err := restarted.Accept(nonce, peer(42)); err != nil {
		t.Fatalf("Accept by the original peer after restore: %v", err)
	}
}

func TestExtraSnapshotExpirySurvivesRestore(t *testing.T) {
	now := time.Unix(1700000000, 0)
	opts := baseOptions()
	opts.NonceTTL = time.Second
	opts.Now = func() time.Time { return now }

	g := impl.NewHelperHandoffGuard(opts)
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{Method: "GET", Path: "/ttl"})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	now = now.Add(2 * time.Second)
	restarted := impl.NewHelperHandoffGuard(opts)
	if err := restarted.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := restarted.Accept(nonce, peer(42)); !errors.Is(err, impl.ErrNonceExpired) {
		t.Fatalf("Accept after restored expiry error = %v, want ErrNonceExpired", err)
	}
	if got := restarted.Pending(); got != 0 {
		t.Fatalf("Pending() after restored expiry = %d, want 0", got)
	}
}

func TestExtraBadRestoreLeavesPendingTableIntact(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{Method: "GET", Path: "/keep"})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	corrupt := append([]byte(nil), snap...)
	corrupt[len(corrupt)/2] ^= 0xff

	if err := g.Restore(corrupt); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(corrupt) error = %v, want ErrBadSnapshot", err)
	}
	if got := g.Pending(); got != 1 {
		t.Fatalf("Pending() after bad restore = %d, want 1", got)
	}
	if _, err := g.Accept(nonce, peer(42)); err != nil {
		t.Fatalf("Accept after bad restore: %v", err)
	}
}
