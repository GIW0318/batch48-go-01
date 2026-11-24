package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-08"
)

func newGuard() *impl.TsigAuthzGuard {
	return impl.NewTsigAuthzGuard(impl.TsigOptions{WindowSeconds: 150, MaxPending: 16, MaxKeys: 8})
}

func bindKey(t *testing.T, g *impl.TsigAuthzGuard, name string) {
	t.Helper()
	spec := impl.TsigKeySpec{Name: name, Algorithm: "hmac-sha256", DigestTag: "tag-" + name}
	if err := g.BindKey(spec); err != nil {
		t.Fatalf("bind key %s: %v", name, err)
	}
}

func grantZone(t *testing.T, g *impl.TsigAuthzGuard) {
	t.Helper()
	grant := impl.ZoneGrant{Zone: "example.com.", Keys: []string{"k1"}, Types: []string{"A", "TXT"}}
	if err := g.SetZoneGrant(grant); err != nil {
		t.Fatalf("set zone grant: %v", err)
	}
}

func update(id string) impl.DnsUpdate {
	return impl.DnsUpdate{
		RequestID:  id,
		KeyName:    "k1",
		Algorithm:  "hmac-sha256",
		Generation: 1,
		Zone:       "example.com.",
		Name:       "www.example.com.",
		Type:       "A",
		At:         1000,
		DigestTag:  "tag-k1",
	}
}

func accept(t *testing.T, g *impl.TsigAuthzGuard, u impl.DnsUpdate) impl.VerifiedUpdate {
	t.Helper()
	v, err := g.Verify(u, u.At+5)
	if err != nil {
		t.Fatalf("verify request %s: %v", u.RequestID, err)
	}
	return v
}

func apply(t *testing.T, g *impl.TsigAuthzGuard, v impl.VerifiedUpdate) int64 {
	t.Helper()
	serial, err := g.ApplyUpdate(v)
	if err != nil {
		t.Fatalf("apply request %s: %v", v.RequestID, err)
	}
	return serial
}

func TestBindKeyRejectsInvalidSpec(t *testing.T) {
	g := newGuard()
	if err := g.BindKey(impl.TsigKeySpec{Name: "", Algorithm: "hmac-sha256", DigestTag: "t"}); !errors.Is(err, impl.ErrKeyConfig) {
		t.Fatalf("empty key name: want ErrKeyConfig, got %v", err)
	}
	if err := g.BindKey(impl.TsigKeySpec{Name: "k1", Algorithm: "hmac-md5", DigestTag: "t"}); !errors.Is(err, impl.ErrKeyConfig) {
		t.Fatalf("hmac-md5: want ErrKeyConfig, got %v", err)
	}
	if err := g.BindKey(impl.TsigKeySpec{Name: "k1", Algorithm: "hmac-sha256", DigestTag: ""}); !errors.Is(err, impl.ErrKeyConfig) {
		t.Fatalf("empty digest tag: want ErrKeyConfig, got %v", err)
	}
}

func TestBindKeyRejectsDuplicateName(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	if err := g.BindKey(impl.TsigKeySpec{Name: "k1", Algorithm: "hmac-sha512", DigestTag: "other"}); !errors.Is(err, impl.ErrKeyExists) {
		t.Fatalf("duplicate key: want ErrKeyExists, got %v", err)
	}
}

func TestBindKeyEnforcesKeyCapacity(t *testing.T) {
	g := impl.NewTsigAuthzGuard(impl.TsigOptions{WindowSeconds: 150, MaxPending: 16, MaxKeys: 1})
	bindKey(t, g, "k1")
	if err := g.BindKey(impl.TsigKeySpec{Name: "k2", Algorithm: "hmac-sha256", DigestTag: "tag-k2"}); !errors.Is(err, impl.ErrKeyCapacity) {
		t.Fatalf("second key with MaxKeys 1: want ErrKeyCapacity, got %v", err)
	}
}

func TestVerifyRejectsUnknownKey(t *testing.T) {
	g := newGuard()
	u := update("req-1")
	u.KeyName = "k-missing"
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrKeyUnknown) {
		t.Fatalf("unknown key: want ErrKeyUnknown, got %v", err)
	}
}

func TestVerifyRejectsRevokedKey(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	if err := g.RevokeKey("k1"); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if _, err := g.Verify(update("req-1"), 1005); !errors.Is(err, impl.ErrKeyRevoked) {
		t.Fatalf("revoked key: want ErrKeyRevoked, got %v", err)
	}
	if err := g.RevokeKey("k-missing"); !errors.Is(err, impl.ErrKeyUnknown) {
		t.Fatalf("revoke unknown key: want ErrKeyUnknown, got %v", err)
	}
}

func TestRotateKeyAdvancesGenerationAndRetiresOld(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	if _, err := g.RotateKey("k-missing", "hmac-sha512"); !errors.Is(err, impl.ErrKeyUnknown) {
		t.Fatalf("rotate unknown key: want ErrKeyUnknown, got %v", err)
	}
	spec, err := g.RotateKey("k1", "hmac-sha512")
	if err != nil {
		t.Fatalf("rotate k1: %v", err)
	}
	if spec.Generation != 2 || spec.Algorithm != "hmac-sha512" {
		t.Fatalf("rotated spec = %+v, want generation 2 hmac-sha512", spec)
	}
	stale := update("req-old")
	if _, err := g.Verify(stale, stale.At+5); !errors.Is(err, impl.ErrGenerationStale) {
		t.Fatalf("stale generation: want ErrGenerationStale, got %v", err)
	}
	fresh := update("req-new")
	fresh.Generation = 2
	fresh.Algorithm = "hmac-sha512"
	if _, err := g.Verify(fresh, fresh.At+5); err != nil {
		t.Fatalf("current generation must verify: %v", err)
	}
}

func TestVerifyRejectsAlgorithmMismatch(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	u.Algorithm = "hmac-sha512"
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrAlgorithmMismatch) {
		t.Fatalf("algorithm mismatch: want ErrAlgorithmMismatch, got %v", err)
	}
}

func TestVerifyRejectsWrongDigestAndCountsFailure(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	u.DigestTag = "tag-tampered"
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrDigestMismatch) {
		t.Fatalf("digest mismatch: want ErrDigestMismatch, got %v", err)
	}
	if got := g.KeyFailures("k1"); got != 1 {
		t.Fatalf("key failures = %d, want 1", got)
	}
	if got := g.Stats().Rejected; got != 1 {
		t.Fatalf("rejected = %d, want 1", got)
	}
}

func TestVerifyRejectsZoneNotGranted(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	u.Zone = "other.example."
	u.Name = "www.other.example."
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrZoneNotGranted) {
		t.Fatalf("zone not granted: want ErrZoneNotGranted, got %v", err)
	}
}

func TestVerifyRejectsTypeNotGranted(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	u.Type = "MX"
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrTypeNotGranted) {
		t.Fatalf("type not granted: want ErrTypeNotGranted, got %v", err)
	}
}

func TestVerifyRejectsNameOutsideZone(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	u.Name = "www.other.net."
	if _, err := g.Verify(u, u.At+5); !errors.Is(err, impl.ErrNameNotOwned) {
		t.Fatalf("name outside zone: want ErrNameNotOwned, got %v", err)
	}
}

func TestVerifyChecksExactPrerequisite(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	first := update("req-1")
	apply(t, g, accept(t, g, first))
	present := update("req-2")
	present.ExpectExists = true
	if _, err := g.Verify(present, present.At+5); err != nil {
		t.Fatalf("prerequisite on an applied exact name must pass: %v", err)
	}
	absent := update("req-3")
	absent.Name = "api.example.com."
	absent.ExpectExists = true
	if _, err := g.Verify(absent, absent.At+5); !errors.Is(err, impl.ErrConditionUnmet) {
		t.Fatalf("missing prerequisite name: want ErrConditionUnmet, got %v", err)
	}
}

func TestApplyUpdateIncrementsSerialMonotonically(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	if serial := apply(t, g, accept(t, g, update("req-1"))); serial != 1 {
		t.Fatalf("first serial = %d, want 1", serial)
	}
	second := update("req-2")
	second.Name = "api.example.com."
	if serial := apply(t, g, accept(t, g, second)); serial != 2 {
		t.Fatalf("second serial = %d, want 2", serial)
	}
	if got := g.Serial("example.com."); got != 2 {
		t.Fatalf("zone serial = %d, want 2", got)
	}
	if got := g.Serial("untouched.example."); got != 0 {
		t.Fatalf("untouched zone serial = %d, want 0", got)
	}
}

func TestDuplicateRequestIDRejected(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	u := update("req-1")
	accept(t, g, u)
	if _, err := g.Verify(u, u.At+6); !errors.Is(err, impl.ErrRequestReplay) {
		t.Fatalf("replayed request: want ErrRequestReplay, got %v", err)
	}
	if got := g.Pending(); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsSerialAndDedupWindow(t *testing.T) {
	g := newGuard()
	bindKey(t, g, "k1")
	grantZone(t, g)
	apply(t, g, accept(t, g, update("req-1")))
	second := update("req-2")
	second.Name = "api.example.com."
	apply(t, g, accept(t, g, second))
	blob, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newGuard()
	if err := restored.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Serial("example.com."); got != 2 {
		t.Fatalf("restored serial = %d, want 2", got)
	}
	if _, err := restored.Verify(update("req-1"), 1005); !errors.Is(err, impl.ErrRequestReplay) {
		t.Fatalf("dedup window lost after restore: want ErrRequestReplay, got %v", err)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrTsigSnapshot) {
		t.Fatalf("garbage snapshot: want ErrTsigSnapshot, got %v", err)
	}
	if got := restored.Serial("example.com."); got != 2 {
		t.Fatalf("bad snapshot disturbed serial, got %d", got)
	}
}
