package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-a-13"
)

const testTenant = "tenant-acme"

func testCanonRules() impl.CanonRules {
	return impl.CanonRules{
		MaxReservationsPerTenant: 4,
		HistoryDepth:             8,
		MaxKeyLen:                64,
	}
}

func newCanon() *impl.SubjectCanonicalizer {
	return impl.NewSubjectCanonicalizer(testCanonRules())
}

func mustCanonicalize(t *testing.T, c *impl.SubjectCanonicalizer, tenant, kind, raw string) impl.Key {
	t.Helper()
	key, err := c.Canonicalize(tenant, kind, raw)
	if err != nil {
		t.Fatalf("canonicalize(%q) must succeed, got %v", raw, err)
	}
	return key
}

func TestCanonicalizeFoldsFullwidthToASCII(t *testing.T) {
	c := newCanon()
	key := mustCanonicalize(t, c, testTenant, "account", "ＡＣＭＥ")
	if key.Value != "acme" {
		t.Fatalf("canonical value = %q, want acme", key.Value)
	}
	if key.Kind != "account" || key.TenantID != testTenant {
		t.Fatalf("canonical key = %+v, want account/%s", key, testTenant)
	}
	if len(key.Digest) != 16 {
		t.Fatalf("digest length = %d, want 16", len(key.Digest))
	}
}

func TestCanonicalizeStripsInvisibleCharacters(t *testing.T) {
	c := newCanon()
	key := mustCanonicalize(t, c, testTenant, "account", "ac\u200Bme\uFEFF")
	if key.Value != "acme" {
		t.Fatalf("canonical value = %q, want acme with invisible characters removed", key.Value)
	}
}

func TestCanonicalizeCollapsesWhitespaceAndCase(t *testing.T) {
	c := newCanon()
	key := mustCanonicalize(t, c, testTenant, "account", "  Alice   Smith  ")
	if key.Value != "alice smith" {
		t.Fatalf("canonical value = %q, want alice smith", key.Value)
	}
}

func TestCanonicalizeIsIdempotent(t *testing.T) {
	c := newCanon()
	first := mustCanonicalize(t, c, testTenant, "account", "  Ａlice   Smith ")
	second := mustCanonicalize(t, c, testTenant, "account", first.Value)
	if second.Value != first.Value {
		t.Fatalf("canonicalize(%q) = %q, want the already canonical %q", first.Value, second.Value, first.Value)
	}
	if second.Digest != first.Digest {
		t.Fatalf("digest changed across repeated canonicalization: %q vs %q", first.Digest, second.Digest)
	}
}

func TestCanonicalizeRejectsUnknownKind(t *testing.T) {
	c := newCanon()
	if _, err := c.Canonicalize(testTenant, "phone", "13800000000"); !errors.Is(err, impl.ErrUnknownKind) {
		t.Fatalf("unknown subject kind must be rejected with ErrUnknownKind, got %v", err)
	}
}

func TestCanonicalizeRejectsOversizeKey(t *testing.T) {
	c := newCanon()
	raw := strings.Repeat("a", 300)
	if _, err := c.Canonicalize(testTenant, "account", raw); !errors.Is(err, impl.ErrKeyTooLong) {
		t.Fatalf("oversize identifier must be rejected with ErrKeyTooLong, got %v", err)
	}
}

func TestCanonicalizeSeparatesTenants(t *testing.T) {
	c := newCanon()
	first := mustCanonicalize(t, c, "tenant-one", "account", "Alice")
	second := mustCanonicalize(t, c, "tenant-two", "account", "Alice")
	if first.Value != second.Value {
		t.Fatalf("same raw identifier canonicalised to %q and %q", first.Value, second.Value)
	}
	if first.TenantID == second.TenantID {
		t.Fatalf("canonical keys must keep their own tenant id")
	}
	if first.Digest == second.Digest {
		t.Fatalf("tenant must take part in the digest, both tenants produced %q", first.Digest)
	}
}

func TestCanonicalizeEmailLowercaseForm(t *testing.T) {
	c := newCanon()
	key := mustCanonicalize(t, c, testTenant, "email", "alice@example.com")
	if key.Kind != "email" {
		t.Fatalf("canonical kind = %q, want email", key.Kind)
	}
	if key.Value != "alice@example.com" {
		t.Fatalf("canonical value = %q, want alice@example.com", key.Value)
	}
}

func TestConflictsReportDifferencePositions(t *testing.T) {
	c := newCanon()
	conflicts, err := c.Conflicts(testTenant, "account", "Ｂob")
	if err != nil {
		t.Fatalf("conflicts must succeed for a foldable identifier, got %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflict count = %d, want 1 (%+v)", len(conflicts), conflicts)
	}
	first := conflicts[0]
	if first.Position != 0 {
		t.Fatalf("first conflict position = %d, want 0", first.Position)
	}
	if first.Got != 'Ｂ' || first.Want != 'b' {
		t.Fatalf("first conflict runes = %q -> %q, want Ｂ -> b", first.Got, first.Want)
	}
	if first.Reason != "fullwidth" {
		t.Fatalf("first conflict reason = %q, want fullwidth", first.Reason)
	}
}

func TestConflictsEmptyForCanonicalInput(t *testing.T) {
	c := newCanon()
	conflicts, err := c.Conflicts(testTenant, "account", "bob")
	if err != nil {
		t.Fatalf("conflicts must succeed for a clean identifier, got %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("canonical input reported %d conflicts, want none", len(conflicts))
	}
}

func TestReserveIsUniquePerTenant(t *testing.T) {
	c := newCanon()
	ok, err := c.Reserve(testTenant, "acme", "acct-1")
	if err != nil {
		t.Fatalf("first reservation must succeed, got %v", err)
	}
	if !ok {
		t.Fatalf("first reservation must report a new claim")
	}
	ok, err = c.Reserve(testTenant, "acme", "acct-2")
	if err != nil {
		t.Fatalf("second reservation must not fail, got %v", err)
	}
	if ok {
		t.Fatalf("second reservation of the same key must report a conflict")
	}
	owner, found := c.Lookup(testTenant, "acme")
	if !found || owner != "acct-1" {
		t.Fatalf("lookup = %q/%v, want the first owner acct-1", owner, found)
	}
	if got := c.ReserveCount(testTenant); got != 1 {
		t.Fatalf("reservation count = %d, want 1", got)
	}
}

func TestReserveIsolatedPerTenant(t *testing.T) {
	c := newCanon()
	if ok, err := c.Reserve("tenant-one", "acme", "acct-1"); err != nil || !ok {
		t.Fatalf("reservation in tenant-one failed: ok=%v err=%v", ok, err)
	}
	if ok, err := c.Reserve("tenant-two", "acme", "acct-2"); err != nil || !ok {
		t.Fatalf("reservation in tenant-two failed: ok=%v err=%v", ok, err)
	}
	first, found := c.Lookup("tenant-one", "acme")
	if !found || first != "acct-1" {
		t.Fatalf("tenant-one owner = %q/%v, want acct-1", first, found)
	}
	second, found := c.Lookup("tenant-two", "acme")
	if !found || second != "acct-2" {
		t.Fatalf("tenant-two owner = %q/%v, want acct-2", second, found)
	}
}

func TestReserveEnforcesTenantCap(t *testing.T) {
	rules := testCanonRules()
	rules.MaxReservationsPerTenant = 2
	c := impl.NewSubjectCanonicalizer(rules)
	for _, key := range []string{"alpha", "beta"} {
		if ok, err := c.Reserve(testTenant, key, "acct-1"); err != nil || !ok {
			t.Fatalf("reservation of %q failed: ok=%v err=%v", key, ok, err)
		}
	}
	if _, err := c.Reserve(testTenant, "gamma", "acct-1"); !errors.Is(err, impl.ErrReservationLimit) {
		t.Fatalf("reservation past the tenant cap must report ErrReservationLimit, got %v", err)
	}
	if got := c.ReserveCount(testTenant); got != 2 {
		t.Fatalf("reservation count after the cap = %d, want 2", got)
	}
}

func TestReleaseAllowsReuseAndIsIdempotent(t *testing.T) {
	c := newCanon()
	if ok, err := c.Reserve(testTenant, "acme", "acct-1"); err != nil || !ok {
		t.Fatalf("reservation failed: ok=%v err=%v", ok, err)
	}
	if err := c.Release(testTenant, "acme"); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
	if _, found := c.Lookup(testTenant, "acme"); found {
		t.Fatalf("released key must no longer resolve to an owner")
	}
	if err := c.Release(testTenant, "acme"); err != nil {
		t.Fatalf("releasing an absent key must stay idempotent, got %v", err)
	}
	ok, err := c.Reserve(testTenant, "acme", "acct-2")
	if err != nil {
		t.Fatalf("re-reservation after release must succeed, got %v", err)
	}
	if !ok {
		t.Fatalf("re-reservation after release must report a new claim")
	}
	owner, found := c.Lookup(testTenant, "acme")
	if !found || owner != "acct-2" {
		t.Fatalf("owner after re-reservation = %q/%v, want acct-2", owner, found)
	}
}

func TestSnapshotRestoreKeepsReservations(t *testing.T) {
	c := newCanon()
	if ok, err := c.Reserve(testTenant, "acme", "acct-1"); err != nil || !ok {
		t.Fatalf("first reservation failed: ok=%v err=%v", ok, err)
	}
	if ok, err := c.Reserve(testTenant, "globex", "acct-2"); err != nil || !ok {
		t.Fatalf("second reservation failed: ok=%v err=%v", ok, err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}

	restored := newCanon()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.ReserveCount(testTenant); got != 2 {
		t.Fatalf("tenant reservation count after restore = %d, want 2", got)
	}
	owner, found := restored.Lookup(testTenant, "globex")
	if !found || owner != "acct-2" {
		t.Fatalf("restored owner = %q/%v, want acct-2", owner, found)
	}
}
