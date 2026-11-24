package task

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-b-02"
)

func peer(pid int) impl.PeerCred {
	return impl.PeerCred{PID: pid, UID: 1000, GID: 1000, StartedAt: time.Unix(1700000000, 0)}
}

func baseOptions() impl.Options {
	return impl.Options{
		AllowedUID:       1000,
		AllowedGID:       1000,
		NonceTTL:         time.Minute,
		MaxFDsPerRequest: 3,
		MaxPending:       8,
	}
}

func mustOffer(t *testing.T, g *impl.HelperHandoffGuard, p impl.PeerCred, fds []int, meta impl.Meta) string {
	t.Helper()
	nonce, err := g.Offer(p, fds, meta)
	if err != nil {
		t.Fatalf("Offer: unexpected error: %v", err)
	}
	if nonce == "" {
		t.Fatal("Offer returned an empty nonce")
	}
	return nonce
}

func TestOfferRejectsIncompletePeer(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	if _, err := g.Offer(impl.PeerCred{PID: 0, UID: 1000, GID: 1000, StartedAt: time.Unix(1, 0)}, []int{3}, impl.Meta{}); !errors.Is(err, impl.ErrPeerInvalid) {
		t.Fatalf("Offer with pid 0 error = %v, want ErrPeerInvalid", err)
	}
	if _, err := g.Offer(impl.PeerCred{PID: 42, UID: 1000, GID: 1000}, []int{3}, impl.Meta{}); !errors.Is(err, impl.ErrPeerInvalid) {
		t.Fatalf("Offer with zero start time error = %v, want ErrPeerInvalid", err)
	}
}

func TestOfferRejectsForeignCredentials(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	badUID := peer(42)
	badUID.UID = 2000
	if _, err := g.Offer(badUID, []int{3}, impl.Meta{}); !errors.Is(err, impl.ErrPeerMismatch) {
		t.Fatalf("Offer with foreign uid error = %v, want ErrPeerMismatch", err)
	}
	badGID := peer(42)
	badGID.GID = 2000
	if _, err := g.Offer(badGID, []int{3}, impl.Meta{}); !errors.Is(err, impl.ErrPeerMismatch) {
		t.Fatalf("Offer with foreign gid error = %v, want ErrPeerMismatch", err)
	}
}

func TestOfferRejectsEmptyAndOversizedFDList(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	if _, err := g.Offer(peer(42), nil, impl.Meta{}); !errors.Is(err, impl.ErrNoFDs) {
		t.Fatalf("Offer with no descriptors error = %v, want ErrNoFDs", err)
	}
	if _, err := g.Offer(peer(42), []int{1, 2, 3, 4}, impl.Meta{}); !errors.Is(err, impl.ErrTooManyFDs) {
		t.Fatalf("Offer above the descriptor cap error = %v, want ErrTooManyFDs", err)
	}
}

func TestOfferRejectsDuplicateAndNegativeFDs(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	if _, err := g.Offer(peer(42), []int{3, 3}, impl.Meta{}); !errors.Is(err, impl.ErrInvalidFD) {
		t.Fatalf("Offer with a repeated descriptor error = %v, want ErrInvalidFD", err)
	}
	if _, err := g.Offer(peer(42), []int{-1}, impl.Meta{}); !errors.Is(err, impl.ErrInvalidFD) {
		t.Fatalf("Offer with a negative descriptor error = %v, want ErrInvalidFD", err)
	}
}

func TestOfferReturnsHexNonceAndParksTheRequest(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{3, 5}, impl.Meta{Method: "GET", Path: "/state"})
	if len(nonce) != 32 {
		t.Fatalf("nonce length = %d, want 32 hex characters", len(nonce))
	}
	if strings.ToLower(nonce) != nonce {
		t.Fatalf("nonce %q is not lowercase hex", nonce)
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		t.Fatalf("nonce %q does not decode as hex: %v", nonce, err)
	}
	if got := g.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1", got)
	}
	info, err := g.Inspect(nonce)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(info.FDs) != 2 || info.FDs[0] != 3 || info.FDs[1] != 5 {
		t.Fatalf("Inspect fds = %v, want [3 5]", info.FDs)
	}
	if info.Peer.PID != 42 {
		t.Fatalf("Inspect peer pid = %d, want 42", info.Peer.PID)
	}
}

func TestOfferRejectsWhenPendingTableIsFull(t *testing.T) {
	opts := baseOptions()
	opts.MaxPending = 1
	g := impl.NewHelperHandoffGuard(opts)
	mustOffer(t, g, peer(42), []int{3}, impl.Meta{})
	if _, err := g.Offer(peer(43), []int{4}, impl.Meta{}); !errors.Is(err, impl.ErrPendingFull) {
		t.Fatalf("Offer above the pending cap error = %v, want ErrPendingFull", err)
	}
	if got := g.Pending(); got != 1 {
		t.Fatalf("Pending() after a rejected offer = %d, want 1", got)
	}
}

func TestAcceptReturnsDescriptorsAndDigest(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	meta := impl.Meta{Method: "PUT", Path: "/transfer/42", Body: []byte("payload")}
	nonce := mustOffer(t, g, peer(42), []int{7, 9}, meta)
	handoff, err := g.Accept(nonce, peer(42))
	if err != nil {
		t.Fatalf("Accept: unexpected error: %v", err)
	}
	if len(handoff.FDs) != 2 || handoff.FDs[0] != 7 || handoff.FDs[1] != 9 {
		t.Fatalf("handoff fds = %v, want [7 9]", handoff.FDs)
	}
	if handoff.Meta.Method != "PUT" || handoff.Meta.Path != "/transfer/42" || string(handoff.Meta.Body) != "payload" {
		t.Fatalf("handoff meta = %+v, want the offered request summary", handoff.Meta)
	}
	sum := sha256.Sum256([]byte("PUT\x00/transfer/42\x00payload"))
	want := hex.EncodeToString(sum[:])
	if handoff.Digest != want {
		t.Fatalf("handoff digest = %q, want %q", handoff.Digest, want)
	}
	if got := g.Pending(); got != 0 {
		t.Fatalf("Pending() after accept = %d, want 0", got)
	}
}

func TestAcceptIsSingleUse(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{})
	if _, err := g.Accept(nonce, peer(42)); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if _, err := g.Accept(nonce, peer(42)); !errors.Is(err, impl.ErrNonceConsumed) {
		t.Fatalf("second Accept error = %v, want ErrNonceConsumed", err)
	}
}

func TestAcceptRejectsUnknownNonce(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	if _, err := g.Accept("0123456789abcdef0123456789abcdef", peer(42)); !errors.Is(err, impl.ErrUnknownNonce) {
		t.Fatalf("Accept with an unknown nonce error = %v, want ErrUnknownNonce", err)
	}
}

func TestAcceptRejectsExpiredNonce(t *testing.T) {
	now := time.Unix(1700000000, 0)
	opts := baseOptions()
	opts.NonceTTL = time.Second
	opts.Now = func() time.Time { return now }
	g := impl.NewHelperHandoffGuard(opts)
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{})
	now = now.Add(2 * time.Second)
	if _, err := g.Accept(nonce, peer(42)); !errors.Is(err, impl.ErrNonceExpired) {
		t.Fatalf("Accept after the ttl error = %v, want ErrNonceExpired", err)
	}
	if got := g.Pending(); got != 0 {
		t.Fatalf("Pending() after expiry = %d, want 0", got)
	}
}

func TestAcceptWrongPeerKeepsOfferPending(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{})
	stranger := peer(99)
	if _, err := g.Accept(nonce, stranger); !errors.Is(err, impl.ErrPeerMismatch) {
		t.Fatalf("Accept by a foreign peer error = %v, want ErrPeerMismatch", err)
	}
	if got := g.Pending(); got != 1 {
		t.Fatalf("Pending() after a peer mismatch = %d, want 1", got)
	}
	if _, err := g.Accept(nonce, peer(42)); err != nil {
		t.Fatalf("Accept by the original peer after mismatch: %v", err)
	}
}

func TestRevokeDropsPendingOffer(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	nonce := mustOffer(t, g, peer(42), []int{7}, impl.Meta{})
	if err := g.Revoke(nonce); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := g.Pending(); got != 0 {
		t.Fatalf("Pending() after revoke = %d, want 0", got)
	}
	if _, err := g.Accept(nonce, peer(42)); !errors.Is(err, impl.ErrUnknownNonce) {
		t.Fatalf("Accept after revoke error = %v, want ErrUnknownNonce", err)
	}
	if err := g.Revoke(nonce); !errors.Is(err, impl.ErrUnknownNonce) {
		t.Fatalf("second Revoke error = %v, want ErrUnknownNonce", err)
	}
}

func TestStatsCountOffersAcceptsAndRevokes(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	first := mustOffer(t, g, peer(42), []int{7}, impl.Meta{})
	second := mustOffer(t, g, peer(43), []int{8}, impl.Meta{})
	if _, err := g.Accept(first, peer(42)); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := g.Revoke(second); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	stats := g.Stats()
	if stats.Offered != 2 {
		t.Fatalf("Stats.Offered = %d, want 2", stats.Offered)
	}
	if stats.Accepted != 1 {
		t.Fatalf("Stats.Accepted = %d, want 1", stats.Accepted)
	}
	if stats.Revoked != 1 {
		t.Fatalf("Stats.Revoked = %d, want 1", stats.Revoked)
	}
}

func TestSnapshotRestoreKeepsPendingAcceptable(t *testing.T) {
	now := time.Unix(1700000000, 0)
	opts := baseOptions()
	opts.Now = func() time.Time { return now }
	g := impl.NewHelperHandoffGuard(opts)
	meta := impl.Meta{Method: "POST", Path: "/job", Body: []byte("go")}
	nonce := mustOffer(t, g, peer(42), []int{11}, meta)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("Snapshot returned an empty payload")
	}

	restored := impl.NewHelperHandoffGuard(opts)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := restored.Pending(); got != 1 {
		t.Fatalf("restored Pending() = %d, want 1", got)
	}
	handoff, err := restored.Accept(nonce, peer(42))
	if err != nil {
		t.Fatalf("Accept after restore: %v", err)
	}
	sum := sha256.Sum256([]byte("POST\x00/job\x00go"))
	if handoff.Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("restored handoff digest = %q", handoff.Digest)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g := impl.NewHelperHandoffGuard(baseOptions())
	if err := g.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot error = %v, want ErrBadSnapshot", err)
	}
	if got := g.Pending(); got != 0 {
		t.Fatalf("Pending() after a rejected snapshot = %d, want 0", got)
	}
}
