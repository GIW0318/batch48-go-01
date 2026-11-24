package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-10"
)

var grantBaseTime = time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)

type grantClock struct {
	now time.Time
}

func (c *grantClock) Now() time.Time {
	return c.now
}

var (
	permOrdersRead  = impl.Permission{Resource: "orders", Action: "read"}
	permOrdersWrite = impl.Permission{Resource: "orders", Action: "write"}
	permBillingRead = impl.Permission{Resource: "billing", Action: "read"}
)

func permStrings(set impl.PermissionSet) []string {
	out := make([]string, 0, len(set))
	for _, perm := range set {
		out = append(out, perm.Resource+":"+perm.Action)
	}
	return out
}

func seededLedger(t *testing.T, perms impl.PermissionSet) *impl.DelegatedGrantLedger {
	t.Helper()
	ledger := impl.NewDelegatedGrantLedger(impl.LedgerOptions{Now: (&grantClock{now: grantBaseTime}).Now})
	if err := ledger.Seed("root", perms); err != nil {
		t.Fatalf("seeding root failed: %v", err)
	}
	return ledger
}

func TestSeedAndEffective(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersWrite, permOrdersRead})
	effective, err := ledger.Effective("root", grantBaseTime)
	if err != nil {
		t.Fatalf("effective failed: %v", err)
	}
	got := permStrings(effective)
	want := []string{"orders:read", "orders:write"}
	if len(got) != len(want) {
		t.Fatalf("effective set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("effective set = %v, want sorted %v", got, want)
		}
	}
	other, err := ledger.Effective("stranger", grantBaseTime)
	if err != nil {
		t.Fatalf("effective for an unseeded subject failed: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("unseeded subject holds %v, want nothing", permStrings(other))
	}
}

func TestSeedRejectsInvalidInput(t *testing.T) {
	ledger := impl.NewDelegatedGrantLedger(impl.LedgerOptions{})
	if err := ledger.Seed("", impl.PermissionSet{permOrdersRead}); !errors.Is(err, impl.ErrInvalidSubject) {
		t.Fatalf("empty subject = %v, want ErrInvalidSubject", err)
	}
	if err := ledger.Seed("root", nil); !errors.Is(err, impl.ErrEmptyPermission) {
		t.Fatalf("empty permission set = %v, want ErrEmptyPermission", err)
	}
	if err := ledger.Seed("root", impl.PermissionSet{permOrdersRead, permOrdersRead}); !errors.Is(err, impl.ErrInvalidDelegation) {
		t.Fatalf("duplicated permission = %v, want ErrInvalidDelegation", err)
	}
	if err := ledger.Seed("root", impl.PermissionSet{{Resource: "", Action: "read"}}); !errors.Is(err, impl.ErrInvalidDelegation) {
		t.Fatalf("permission without a resource = %v, want ErrInvalidDelegation", err)
	}
}

func TestDelegateReturnsPendingGrant(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	grant, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("delegate failed: %v", err)
	}
	if grant.ID == "" {
		t.Fatalf("grant id must not be empty")
	}
	if grant.FromID != "root" || grant.ToID != "mid" {
		t.Fatalf("grant = %+v, want root -> mid", grant)
	}
	if grant.Depth != 1 {
		t.Fatalf("depth = %d, want 1", grant.Depth)
	}
	if grant.ParentID != "" {
		t.Fatalf("parent = %q, want empty for a root delegation", grant.ParentID)
	}
	if grant.Accepted {
		t.Fatalf("a fresh grant must not be effective before it is accepted")
	}
	if len(grant.Perms) != 1 || grant.Perms[0] != permOrdersRead {
		t.Fatalf("grant permissions = %v, want [orders:read]", permStrings(grant.Perms))
	}
	if count, err := ledger.GrantCount("mid"); err != nil || count != 1 {
		t.Fatalf("grant count = (%d, %v), want (1, nil)", count, err)
	}
	if depth, err := ledger.Depth(grant.ID); err != nil || depth != 1 {
		t.Fatalf("depth lookup = (%d, %v), want (1, nil)", depth, err)
	}
	effective, err := ledger.Effective("mid", grantBaseTime)
	if err != nil {
		t.Fatalf("effective failed: %v", err)
	}
	if len(effective) != 0 {
		t.Fatalf("a pending grant is already effective: %v", permStrings(effective))
	}
}

func TestDelegateRejectsInvalidInput(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	if _, err := ledger.Delegate("", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrInvalidDelegation) {
		t.Fatalf("empty delegator = %v, want ErrInvalidDelegation", err)
	}
	if _, err := ledger.Delegate("root", "root", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrInvalidDelegation) {
		t.Fatalf("self delegation = %v, want ErrInvalidDelegation", err)
	}
	if _, err := ledger.Delegate("root", "mid", nil, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrEmptyPermission) {
		t.Fatalf("empty permission set = %v, want ErrEmptyPermission", err)
	}
	if _, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead, permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrInvalidDelegation) {
		t.Fatalf("duplicated permission = %v, want ErrInvalidDelegation", err)
	}
	if _, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime); !errors.Is(err, impl.ErrInvalidExpiry) {
		t.Fatalf("expiry equal to now = %v, want ErrInvalidExpiry", err)
	}
	if _, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(-time.Minute)); !errors.Is(err, impl.ErrInvalidExpiry) {
		t.Fatalf("expiry in the past = %v, want ErrInvalidExpiry", err)
	}
}

func TestDelegateRequiresSubset(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	if _, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersWrite}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrNotSubset) {
		t.Fatalf("delegating a permission the delegator does not hold = %v, want ErrNotSubset", err)
	}
	if _, err := ledger.Delegate("stranger", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrNotSubset) {
		t.Fatalf("delegating from an unseeded subject = %v, want ErrNotSubset", err)
	}
	if count, _ := ledger.GrantCount("mid"); count != 0 {
		t.Fatalf("rejected delegations left %d grants", count)
	}
}

func TestAcceptMakesGrantEffective(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	grant, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("delegate failed: %v", err)
	}
	if err := ledger.Accept("grant-missing"); !errors.Is(err, impl.ErrUnknownGrant) {
		t.Fatalf("accepting an unknown grant = %v, want ErrUnknownGrant", err)
	}
	if err := ledger.Accept(grant.ID); err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	if err := ledger.Accept(grant.ID); !errors.Is(err, impl.ErrAlreadyAccepted) {
		t.Fatalf("accepting twice = %v, want ErrAlreadyAccepted", err)
	}
	effective, err := ledger.Effective("mid", grantBaseTime)
	if err != nil {
		t.Fatalf("effective failed: %v", err)
	}
	if len(effective) != 1 || effective[0] != permOrdersRead {
		t.Fatalf("effective set = %v, want [orders:read]", permStrings(effective))
	}
}

func TestEffectiveHonoursExpiry(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	grant, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("delegate failed: %v", err)
	}
	if err := ledger.Accept(grant.ID); err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	live, err := ledger.Effective("mid", grantBaseTime.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("effective inside the window failed: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("effective inside the window = %v, want one permission", permStrings(live))
	}
	expired, err := ledger.Effective("mid", grantBaseTime.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("effective after expiry failed: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired grant still yields %v", permStrings(expired))
	}
}

func TestEffectiveUnionsIndependentGrants(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead, permBillingRead})
	first, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("first delegation failed: %v", err)
	}
	second, err := ledger.Delegate("root", "mid", impl.PermissionSet{permBillingRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("second delegation failed: %v", err)
	}
	if err := ledger.Accept(first.ID); err != nil {
		t.Fatalf("accepting the first grant failed: %v", err)
	}
	if err := ledger.Accept(second.ID); err != nil {
		t.Fatalf("accepting the second grant failed: %v", err)
	}
	effective, err := ledger.Effective("mid", grantBaseTime)
	if err != nil {
		t.Fatalf("effective failed: %v", err)
	}
	got := permStrings(effective)
	want := []string{"billing:read", "orders:read"}
	if len(got) != len(want) {
		t.Fatalf("effective set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("effective set = %v, want sorted %v", got, want)
		}
	}
	if count, _ := ledger.GrantCount("mid"); count != 2 {
		t.Fatalf("grant count = %d, want 2", count)
	}
}

func TestSourcesReportsDelegationChain(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	first, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("first delegation failed: %v", err)
	}
	if err := ledger.Accept(first.ID); err != nil {
		t.Fatalf("accepting the first grant failed: %v", err)
	}
	second, err := ledger.Delegate("mid", "leaf", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("second delegation failed: %v", err)
	}
	if err := ledger.Accept(second.ID); err != nil {
		t.Fatalf("accepting the second grant failed: %v", err)
	}
	if second.ParentID != first.ID {
		t.Fatalf("second grant parent = %q, want %q", second.ParentID, first.ID)
	}
	if second.Depth != 2 {
		t.Fatalf("second grant depth = %d, want 2", second.Depth)
	}
	sources, err := ledger.Sources("leaf", grantBaseTime)
	if err != nil {
		t.Fatalf("sources failed: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %d entries, want 1", len(sources))
	}
	if sources[0].GrantID != second.ID {
		t.Fatalf("source grant = %q, want the leaf grant %q", sources[0].GrantID, second.ID)
	}
	if sources[0].Permission != permOrdersRead {
		t.Fatalf("source permission = %+v, want orders:read", sources[0].Permission)
	}
	if len(sources[0].Chain) != 2 || sources[0].Chain[0] != first.ID || sources[0].Chain[1] != second.ID {
		t.Fatalf("source chain = %v, want [%s %s]", sources[0].Chain, first.ID, second.ID)
	}
}

func TestDepthLimitEnforced(t *testing.T) {
	ledger := impl.NewDelegatedGrantLedger(impl.LedgerOptions{MaxDepth: 2, Now: (&grantClock{now: grantBaseTime}).Now})
	if err := ledger.Seed("root", impl.PermissionSet{permOrdersRead}); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}
	first, err := ledger.Delegate("root", "m1", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("first delegation failed: %v", err)
	}
	if err := ledger.Accept(first.ID); err != nil {
		t.Fatalf("accepting the first grant failed: %v", err)
	}
	second, err := ledger.Delegate("m1", "m2", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("second delegation failed: %v", err)
	}
	if err := ledger.Accept(second.ID); err != nil {
		t.Fatalf("accepting the second grant failed: %v", err)
	}
	if second.Depth != 2 {
		t.Fatalf("second grant depth = %d, want 2", second.Depth)
	}
	if _, err := ledger.Delegate("m2", "leaf", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrDepthLimit) {
		t.Fatalf("third hop over the depth limit = %v, want ErrDepthLimit", err)
	}
}

func TestGrantLimitPerSubject(t *testing.T) {
	ledger := impl.NewDelegatedGrantLedger(impl.LedgerOptions{MaxGrantsPerSubject: 2, Now: (&grantClock{now: grantBaseTime}).Now})
	if err := ledger.Seed("root", impl.PermissionSet{permOrdersRead, permOrdersWrite}); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}
	for _, perm := range []impl.Permission{permOrdersRead, permOrdersWrite} {
		if _, err := ledger.Delegate("root", "leaf", impl.PermissionSet{perm}, grantBaseTime.Add(time.Hour)); err != nil {
			t.Fatalf("delegating %v failed: %v", perm, err)
		}
	}
	if _, err := ledger.Delegate("root", "leaf", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour)); !errors.Is(err, impl.ErrGrantLimit) {
		t.Fatalf("third grant over the limit = %v, want ErrGrantLimit", err)
	}
	if count, _ := ledger.GrantCount("leaf"); count != 2 {
		t.Fatalf("grant count = %d, want 2", count)
	}
}

func TestRevokeCascadesToDownstream(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	first, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("first delegation failed: %v", err)
	}
	if err := ledger.Accept(first.ID); err != nil {
		t.Fatalf("accepting the first grant failed: %v", err)
	}
	second, err := ledger.Delegate("mid", "leaf", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("second delegation failed: %v", err)
	}
	if err := ledger.Accept(second.ID); err != nil {
		t.Fatalf("accepting the second grant failed: %v", err)
	}
	if effective, err := ledger.Effective("leaf", grantBaseTime); err != nil || len(effective) != 1 {
		t.Fatalf("leaf effective before revoke = (%v, %v), want one permission", permStrings(effective), err)
	}
	revoked, err := ledger.Revoke(first.ID)
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("revoke reported %d grants, want 2", revoked)
	}
	if effective, err := ledger.Effective("leaf", grantBaseTime); err != nil || len(effective) != 0 {
		t.Fatalf("leaf effective after revoke = (%v, %v), want empty", permStrings(effective), err)
	}
	if effective, err := ledger.Effective("mid", grantBaseTime); err != nil || len(effective) != 0 {
		t.Fatalf("mid effective after revoke = (%v, %v), want empty", permStrings(effective), err)
	}
	if _, err := ledger.Revoke(first.ID); !errors.Is(err, impl.ErrUnknownGrant) {
		t.Fatalf("revoking twice = %v, want ErrUnknownGrant", err)
	}
	if _, err := ledger.Revoke("grant-999"); !errors.Is(err, impl.ErrUnknownGrant) {
		t.Fatalf("revoking an unknown grant = %v, want ErrUnknownGrant", err)
	}
}

func TestCascadeRevokesIssuedGrants(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead, permBillingRead})
	rootGrant, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead, permBillingRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("root delegation failed: %v", err)
	}
	if err := ledger.Accept(rootGrant.ID); err != nil {
		t.Fatalf("accepting the root grant failed: %v", err)
	}
	for _, leaf := range []string{"leaf-1", "leaf-2"} {
		grant, err := ledger.Delegate("mid", leaf, impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
		if err != nil {
			t.Fatalf("delegating to %s failed: %v", leaf, err)
		}
		if err := ledger.Accept(grant.ID); err != nil {
			t.Fatalf("accepting the grant of %s failed: %v", leaf, err)
		}
	}
	revoked, err := ledger.Cascade("mid")
	if err != nil {
		t.Fatalf("cascade failed: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("cascade revoked %d grants, want 2", revoked)
	}
	for _, leaf := range []string{"leaf-1", "leaf-2"} {
		effective, err := ledger.Effective(leaf, grantBaseTime)
		if err != nil {
			t.Fatalf("effective for %s failed: %v", leaf, err)
		}
		if len(effective) != 0 {
			t.Fatalf("%s still holds %v after the cascade", leaf, permStrings(effective))
		}
	}
	if effective, err := ledger.Effective("mid", grantBaseTime); err != nil || len(effective) != 2 {
		t.Fatalf("mid effective after the cascade = (%v, %v), want its own two permissions", permStrings(effective), err)
	}
	if revoked, err := ledger.Cascade("nobody"); err != nil || revoked != 0 {
		t.Fatalf("cascade on a subject without grants = (%d, %v), want (0, nil)", revoked, err)
	}
}

func TestSnapshotRestoreKeepsGrants(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	first, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("first delegation failed: %v", err)
	}
	if err := ledger.Accept(first.ID); err != nil {
		t.Fatalf("accepting the first grant failed: %v", err)
	}
	second, err := ledger.Delegate("mid", "leaf", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("second delegation failed: %v", err)
	}
	if err := ledger.Accept(second.ID); err != nil {
		t.Fatalf("accepting the second grant failed: %v", err)
	}
	data, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewDelegatedGrantLedger(impl.LedgerOptions{Now: (&grantClock{now: grantBaseTime}).Now})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	effective, err := restored.Effective("leaf", grantBaseTime)
	if err != nil {
		t.Fatalf("effective after restore failed: %v", err)
	}
	if len(effective) != 1 || effective[0] != permOrdersRead {
		t.Fatalf("restored effective set = %v, want [orders:read]", permStrings(effective))
	}
	sources, err := restored.Sources("leaf", grantBaseTime)
	if err != nil {
		t.Fatalf("sources after restore failed: %v", err)
	}
	if len(sources) != 1 || len(sources[0].Chain) != 2 {
		t.Fatalf("restored sources = %+v, want a two hop chain", sources)
	}
	if count, err := restored.GrantCount("mid"); err != nil || count != 1 {
		t.Fatalf("restored grant count = (%d, %v), want (1, nil)", count, err)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restoring garbage = %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentRevokeHasSingleWinner(t *testing.T) {
	ledger := seededLedger(t, impl.PermissionSet{permOrdersRead})
	grant, err := ledger.Delegate("root", "mid", impl.PermissionSet{permOrdersRead}, grantBaseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("delegate failed: %v", err)
	}
	if err := ledger.Accept(grant.ID); err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	const workers = 8
	errs := make([]error, workers)
	counts := make([]int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			counts[idx], errs[idx] = ledger.Revoke(grant.ID)
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			wins++
			if counts[i] != 1 {
				t.Fatalf("winner %d reported %d revoked grants, want 1", i, counts[i])
			}
			continue
		}
		if !errors.Is(errs[i], impl.ErrUnknownGrant) {
			t.Fatalf("worker %d got %v, want nil or ErrUnknownGrant", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent revokes succeeded, want exactly 1", wins)
	}
	if effective, err := ledger.Effective("mid", grantBaseTime); err != nil || len(effective) != 0 {
		t.Fatalf("effective after the revoke race = (%v, %v), want empty", permStrings(effective), err)
	}
}
