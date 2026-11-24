package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-10"
)

func newGuard() *impl.HostKeyPinGuard {
	return impl.NewHostKeyPinGuard(impl.PinOptions{MaxEntriesPerTenant: 8, OverlapSeconds: 60, MaxHops: 3})
}

func pinRequest(host, address, fingerprint string) impl.PinRequest {
	return impl.PinRequest{
		Tenant:       "tenant-1",
		Hostname:     host,
		Port:         22,
		Addresses:    []string{address},
		Fingerprints: map[string]string{address: fingerprint},
		Trust:        true,
		At:           100,
	}
}

func pinHost(t *testing.T, g *impl.HostKeyPinGuard, host, address, fingerprint string) impl.PinDecision {
	t.Helper()
	decision, err := g.Pin(pinRequest(host, address, fingerprint))
	if err != nil {
		t.Fatalf("pin %s/%s: %v", host, address, err)
	}
	return decision
}

func verifyRequest(host, address, fingerprint string, at int64) impl.VerifyRequest {
	return impl.VerifyRequest{
		Tenant:    "tenant-1",
		Hostname:  host,
		Port:      22,
		Presented: map[string]string{address: fingerprint},
		At:        at,
	}
}

func TestPinRequiresExplicitTrust(t *testing.T) {
	g := newGuard()
	req := pinRequest("host-a", "10.0.0.1", "SHA256:old")
	req.Trust = false
	if _, err := g.Pin(req); !errors.Is(err, impl.ErrTrustRequired) {
		t.Fatalf("pin without trust: want ErrTrustRequired, got %v", err)
	}
	if entries := g.Entries("tenant-1"); len(entries) != 0 {
		t.Fatalf("rejected pin must not write entries, got %v", entries)
	}
}

func TestPinRejectsMalformedRequest(t *testing.T) {
	g := newGuard()
	bad := pinRequest("host-a", "10.0.0.1", "SHA256:old")
	bad.Tenant = ""
	if _, err := g.Pin(bad); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("empty tenant: want ErrPinInput, got %v", err)
	}
	bad = pinRequest("", "10.0.0.1", "SHA256:old")
	if _, err := g.Pin(bad); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("empty host: want ErrPinInput, got %v", err)
	}
	bad = pinRequest("host-a", "10.0.0.1", "SHA256:old")
	bad.Port = 0
	if _, err := g.Pin(bad); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("port 0: want ErrPinInput, got %v", err)
	}
	bad = pinRequest("host-a", "10.0.0.1", "SHA256:old")
	bad.Addresses = nil
	if _, err := g.Pin(bad); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("no addresses: want ErrPinInput, got %v", err)
	}
	bad = pinRequest("host-a", "10.0.0.1", "SHA256:old")
	bad.Addresses = []string{"10.0.0.1", "10.0.0.2"}
	if _, err := g.Pin(bad); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("fingerprint count mismatch: want ErrPinInput, got %v", err)
	}
}

func TestPinWritesTrustedEntry(t *testing.T) {
	g := newGuard()
	decision := pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if !decision.Allowed || !decision.NewHost {
		t.Fatalf("first trust decision = %+v, want allowed new host", decision)
	}
	if decision.Reason != "trusted_first_use" {
		t.Fatalf("reason = %q, want trusted_first_use", decision.Reason)
	}
	entries := g.Entries("tenant-1")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Hostname != "host-a" || entries[0].Address != "10.0.0.1" || entries[0].Fingerprint != "SHA256:old" {
		t.Fatalf("stored entry = %+v", entries[0])
	}
	if got := g.Stats().Pinned; got != 1 {
		t.Fatalf("pinned counter = %d, want 1", got)
	}
}

func TestPinRejectsDuplicateEntry(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if _, err := g.Pin(pinRequest("host-a", "10.0.0.1", "SHA256:old")); !errors.Is(err, impl.ErrPinnedAlready) {
		t.Fatalf("duplicate pin: want ErrPinnedAlready, got %v", err)
	}
}

func TestPinEnforcesTenantEntryLimit(t *testing.T) {
	g := impl.NewHostKeyPinGuard(impl.PinOptions{MaxEntriesPerTenant: 1, OverlapSeconds: 60, MaxHops: 3})
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:a")
	if _, err := g.Pin(pinRequest("host-b", "10.0.0.2", "SHA256:b")); !errors.Is(err, impl.ErrEntryLimit) {
		t.Fatalf("second tenant entry: want ErrEntryLimit, got %v", err)
	}
	if entries := g.Entries("tenant-1"); len(entries) != 1 {
		t.Fatalf("entry limit rejection wrote extra entries: %v", entries)
	}
}

func TestVerifyRejectsUnknownHost(t *testing.T) {
	g := newGuard()
	if _, err := g.Verify(verifyRequest("host-missing", "10.0.0.1", "SHA256:x", 110)); !errors.Is(err, impl.ErrHostUnknown) {
		t.Fatalf("unknown host: want ErrHostUnknown, got %v", err)
	}
}

func TestVerifyAcceptsPinnedFingerprint(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	decision, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:old", 110))
	if err != nil {
		t.Fatalf("verify pinned host: %v", err)
	}
	if !decision.Allowed || decision.Reason != "pinned_match" {
		t.Fatalf("decision = %+v, want allowed pinned_match", decision)
	}
	if got := g.Stats().Verified; got != 1 {
		t.Fatalf("verified counter = %d, want 1", got)
	}
}

func TestVerifyRejectsMismatchedFingerprint(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if _, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:forged", 110)); !errors.Is(err, impl.ErrFingerprintMismatch) {
		t.Fatalf("mismatched fingerprint: want ErrFingerprintMismatch, got %v", err)
	}
	if got := g.Stats().Denied; got != 1 {
		t.Fatalf("denied counter = %d, want 1", got)
	}
	entries := g.Entries("tenant-1")
	if len(entries) != 1 || entries[0].Fingerprint != "SHA256:old" {
		t.Fatalf("mismatch must not re-pin the host: %v", entries)
	}
}

func TestBeginRotationRequiresKnownHost(t *testing.T) {
	g := newGuard()
	if err := g.BeginRotation("tenant-1", "host-missing", 22, "SHA256:new", 100); !errors.Is(err, impl.ErrHostUnknown) {
		t.Fatalf("rotate unknown host: want ErrHostUnknown, got %v", err)
	}
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if err := g.BeginRotation("tenant-1", "host-a", 22, "", 100); !errors.Is(err, impl.ErrPinInput) {
		t.Fatalf("empty new fingerprint: want ErrPinInput, got %v", err)
	}
}

func TestRotationOverlapAcceptsOldAndNew(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if err := g.BeginRotation("tenant-1", "host-a", 22, "SHA256:new", 100); err != nil {
		t.Fatalf("begin rotation: %v", err)
	}
	oldDecision, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:old", 110))
	if err != nil {
		t.Fatalf("old fingerprint must still verify during overlap: %v", err)
	}
	if oldDecision.Reason != "rotation_overlap" {
		t.Fatalf("old fingerprint reason = %q, want rotation_overlap", oldDecision.Reason)
	}
	newDecision, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:new", 110))
	if err != nil {
		t.Fatalf("new fingerprint must verify during overlap: %v", err)
	}
	if newDecision.Reason != "rotation_overlap" {
		t.Fatalf("new fingerprint reason = %q, want rotation_overlap", newDecision.Reason)
	}
	if got := g.Stats().Rotations; got != 1 {
		t.Fatalf("rotations counter = %d, want 1", got)
	}
}

func TestRotationCompletionRetiresOldFingerprint(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if err := g.BeginRotation("tenant-1", "host-a", 22, "SHA256:new", 100); err != nil {
		t.Fatalf("begin rotation: %v", err)
	}
	if err := g.CompleteRotation("tenant-1", "host-a", 22, 120); err != nil {
		t.Fatalf("complete rotation: %v", err)
	}
	if _, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:old", 130)); !errors.Is(err, impl.ErrFingerprintMismatch) {
		t.Fatalf("retired fingerprint: want ErrFingerprintMismatch, got %v", err)
	}
	decision, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:new", 130))
	if err != nil {
		t.Fatalf("new fingerprint after completion: %v", err)
	}
	if decision.Reason != "pinned_match" {
		t.Fatalf("reason after completion = %q, want pinned_match", decision.Reason)
	}
	if err := g.CompleteRotation("tenant-1", "host-a", 22, 140); !errors.Is(err, impl.ErrNoRotation) {
		t.Fatalf("second completion: want ErrNoRotation, got %v", err)
	}
}

func TestRevokeBlocksVerification(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if err := g.Revoke("tenant-1", "host-a", 22); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:old", 110)); !errors.Is(err, impl.ErrHostRevoked) {
		t.Fatalf("revoked host: want ErrHostRevoked, got %v", err)
	}
	if got := g.Stats().Revoked; got != 1 {
		t.Fatalf("revoked counter = %d, want 1", got)
	}
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:rebuilt")
	decision, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:rebuilt", 110))
	if err != nil {
		t.Fatalf("re-pinned host must verify: %v", err)
	}
	if decision.Reason != "pinned_match" {
		t.Fatalf("re-pinned reason = %q, want pinned_match", decision.Reason)
	}
}

func TestPropagateEnforcesHopBudget(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:a")
	pinHost(t, g, "host-b", "10.0.0.2", "SHA256:b")
	if err := g.Revoke("tenant-1", "host-b", 22); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := g.Propagate(0); !errors.Is(err, impl.ErrHopBudget) {
		t.Fatalf("zero hops: want ErrHopBudget, got %v", err)
	}
	if _, err := g.Propagate(4); !errors.Is(err, impl.ErrHopBudget) {
		t.Fatalf("hops above MaxHops: want ErrHopBudget, got %v", err)
	}
	count, err := g.Propagate(1)
	if err != nil {
		t.Fatalf("propagate: %v", err)
	}
	if count != 2 {
		t.Fatalf("propagated count = %d, want 2 (one pin and one tombstone)", count)
	}
	if got := g.Stats().Propagated; got != 2 {
		t.Fatalf("propagated counter = %d, want 2", got)
	}
}

func TestAuditLogRecordsDecisionReasons(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if _, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:old", 110)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := g.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:forged", 111)); !errors.Is(err, impl.ErrFingerprintMismatch) {
		t.Fatalf("mismatch verify: %v", err)
	}
	log := g.AuditLog()
	if len(log) != 3 {
		t.Fatalf("audit length = %d, want 3", len(log))
	}
	wantActions := []string{"pin", "verify", "verify"}
	for i, want := range wantActions {
		if log[i].Action != want {
			t.Fatalf("audit[%d].Action = %q, want %q", i, log[i].Action, want)
		}
	}
	if log[1].Reason != "pinned_match" {
		t.Fatalf("accepted verify reason = %q, want pinned_match", log[1].Reason)
	}
	if log[2].Reason != "fingerprint_mismatch" {
		t.Fatalf("denied verify reason = %q, want fingerprint_mismatch", log[2].Reason)
	}
}

func TestSnapshotRestorePreservesRotationAndTombstones(t *testing.T) {
	g := newGuard()
	pinHost(t, g, "host-a", "10.0.0.1", "SHA256:old")
	if err := g.BeginRotation("tenant-1", "host-a", 22, "SHA256:new", 100); err != nil {
		t.Fatalf("begin rotation: %v", err)
	}
	pinHost(t, g, "host-b", "10.0.0.2", "SHA256:b")
	if err := g.Revoke("tenant-1", "host-b", 22); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	blob, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newGuard()
	if err := restored.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if entries := restored.Entries("tenant-1"); len(entries) != 1 {
		t.Fatalf("restored entries = %v, want only host-a", entries)
	}
	if _, err := restored.Verify(verifyRequest("host-b", "10.0.0.2", "SHA256:b", 130)); !errors.Is(err, impl.ErrHostRevoked) {
		t.Fatalf("restored tombstone: want ErrHostRevoked, got %v", err)
	}
	decision, err := restored.Verify(verifyRequest("host-a", "10.0.0.1", "SHA256:new", 130))
	if err != nil {
		t.Fatalf("restored rotation must accept new fingerprint: %v", err)
	}
	if decision.Reason != "rotation_overlap" {
		t.Fatalf("restored rotation reason = %q, want rotation_overlap", decision.Reason)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrPinSnapshot) {
		t.Fatalf("garbage snapshot: want ErrPinSnapshot, got %v", err)
	}
	if entries := restored.Entries("tenant-1"); len(entries) != 1 {
		t.Fatalf("bad snapshot disturbed entries: %v", entries)
	}
}
