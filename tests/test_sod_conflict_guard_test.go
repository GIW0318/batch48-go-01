package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-05"
)

func sodGuard(t *testing.T, now *time.Time, tweak func(*impl.SodConfig)) *impl.SodConflictGuard {
	t.Helper()
	cfg := impl.SodConfig{
		MaxPairsPerTenant:       8,
		MaxExemptionsPerSubject: 4,
		MaxAuditPerTenant:       32,
		MaxRolesPerTenant:       8,
		Now:                     func() time.Time { return *now },
	}
	if tweak != nil {
		tweak(&cfg)
	}
	return impl.NewSodConflictGuard(cfg)
}

func sodPair(t *testing.T, g *impl.SodConflictGuard, tenant string) impl.ConflictPair {
	t.Helper()
	pairs := g.Conflicts(tenant)
	if len(pairs) != 1 {
		t.Fatalf("conflict pairs of %s = %d, want 1", tenant, len(pairs))
	}
	return pairs[0]
}

func TestAddConflictStoresPair(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pairs := g.Conflicts("tenant-1")
	if len(pairs) != 1 {
		t.Fatalf("conflict pairs = %d, want 1", len(pairs))
	}
	pair := pairs[0]
	if pair.ID == "" {
		t.Fatalf("a registered pair must carry an id")
	}
	if pair.TenantID != "tenant-1" {
		t.Fatalf("pair tenant = %q, want tenant-1", pair.TenantID)
	}
	if pair.Mode != impl.ModeBlock {
		t.Fatalf("pair mode = %q, want %q", pair.Mode, impl.ModeBlock)
	}
	sides := map[string]bool{pair.PermA: true, pair.PermB: true}
	if len(sides) != 2 || !sides["pay.approve"] || !sides["pay.pay"] {
		t.Fatalf("pair sides = %q / %q, want pay.approve and pay.pay", pair.PermA, pair.PermB)
	}
	if other := g.Conflicts("tenant-2"); len(other) != 0 {
		t.Fatalf("pairs must stay inside their tenant, got %+v", other)
	}
}

func TestAddConflictRejectsBadInput(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "   ", "pay.pay", impl.ModeBlock); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank permission side must fail with ErrBlankArg, got %v", err)
	}
	if err := g.AddConflict("", "pay.approve", "pay.pay", impl.ModeBlock); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank tenant must fail with ErrBlankArg, got %v", err)
	}
	if err := g.AddConflict("tenant-1", "pay.pay", "pay.pay", impl.ModeBlock); !errors.Is(err, impl.ErrSamePairPerms) {
		t.Fatalf("identical sides must fail with ErrSamePairPerms, got %v", err)
	}
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", "audit"); !errors.Is(err, impl.ErrBadMode) {
		t.Fatalf("unknown mode must fail with ErrBadMode, got %v", err)
	}
	if got := len(g.Conflicts("tenant-1")); got != 0 {
		t.Fatalf("rejected registrations left %d pairs behind", got)
	}
}

func TestConflictPairCap(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, func(cfg *impl.SodConfig) { cfg.MaxPairsPerTenant = 2 })
	if err := g.AddConflict("tenant-1", "a", "b", impl.ModeBlock); err != nil {
		t.Fatalf("first pair: %v", err)
	}
	if err := g.AddConflict("tenant-1", "c", "d", impl.ModeAlert); err != nil {
		t.Fatalf("second pair: %v", err)
	}
	if err := g.AddConflict("tenant-1", "e", "f", impl.ModeBlock); !errors.Is(err, impl.ErrPairLimit) {
		t.Fatalf("third pair must hit the tenant cap, got %v", err)
	}
	if got := len(g.Conflicts("tenant-1")); got != 2 {
		t.Fatalf("pair count = %d, want the cap 2", got)
	}
}

func TestCheckAllowsWhenSingleSided(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	first, err := g.GrantWithCheck("tenant-1", "user-1", "pay.approve")
	if err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	if !first.Applied || first.Perm != "pay.approve" {
		t.Fatalf("first grant = %+v, want an applied pay.approve", first)
	}
	verdict, err := g.Check("tenant-1", "user-1", impl.PermissionSet{"report.read"})
	if err != nil {
		t.Fatalf("check an unrelated permission: %v", err)
	}
	if !verdict.Allowed || verdict.Action != impl.ActionAllow {
		t.Fatalf("unrelated permission got allowed=%v action=%q, want allow", verdict.Allowed, verdict.Action)
	}
	if verdict.Reason != impl.ReasonClear {
		t.Fatalf("reason = %q, want %q", verdict.Reason, impl.ReasonClear)
	}
	if len(verdict.Conflicts) != 0 {
		t.Fatalf("unrelated permission reported conflicts %v", verdict.Conflicts)
	}
	if perms := g.Permissions("tenant-1", "user-1"); len(perms) != 1 || perms[0] != "pay.approve" {
		t.Fatalf("permissions = %v, want only pay.approve", perms)
	}
}

func TestCheckBlocksConflictingAddition(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if _, err := g.GrantWithCheck("tenant-1", "user-2", "pay.approve"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	verdict, err := g.Check("tenant-1", "user-2", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check the other side: %v", err)
	}
	if verdict.Allowed {
		t.Fatalf("holding both sides must be rejected")
	}
	if verdict.Action != impl.ActionBlock {
		t.Fatalf("action = %q, want %q", verdict.Action, impl.ActionBlock)
	}
	if verdict.Reason != impl.ReasonSodBlock {
		t.Fatalf("reason = %q, want %q", verdict.Reason, impl.ReasonSodBlock)
	}
	if len(verdict.Conflicts) != 1 || verdict.Conflicts[0] != pair.ID {
		t.Fatalf("conflicts = %v, want the pair %s", verdict.Conflicts, pair.ID)
	}
	if perms := g.Permissions("tenant-1", "user-2"); len(perms) != 1 || perms[0] != "pay.approve" {
		t.Fatalf("check must not apply the addition, got %v", perms)
	}
	batch, err := g.Check("tenant-1", "user-3", impl.PermissionSet{"pay.approve", "pay.pay"})
	if err != nil {
		t.Fatalf("check a batch holding both sides: %v", err)
	}
	if batch.Allowed || batch.Action != impl.ActionBlock {
		t.Fatalf("a batch holding both sides got allowed=%v action=%q, want block", batch.Allowed, batch.Action)
	}
}

func TestCheckAlertModeKeepsAllowed(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "audit.read", "audit.write", impl.ModeAlert); err != nil {
		t.Fatalf("add alert pair: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if _, err := g.GrantWithCheck("tenant-1", "user-4", "audit.read"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	verdict, err := g.Check("tenant-1", "user-4", impl.PermissionSet{"audit.write"})
	if err != nil {
		t.Fatalf("check the other side: %v", err)
	}
	if !verdict.Allowed {
		t.Fatalf("alert mode must keep the addition allowed")
	}
	if verdict.Action != impl.ActionAlert {
		t.Fatalf("action = %q, want %q", verdict.Action, impl.ActionAlert)
	}
	if verdict.Reason != impl.ReasonSodAlert {
		t.Fatalf("reason = %q, want %q", verdict.Reason, impl.ReasonSodAlert)
	}
	if len(verdict.Conflicts) != 1 || verdict.Conflicts[0] != pair.ID {
		t.Fatalf("conflicts = %v, want the pair %s", verdict.Conflicts, pair.ID)
	}
	grant, err := g.GrantWithCheck("tenant-1", "user-4", "audit.write")
	if err != nil {
		t.Fatalf("alert mode grant: %v", err)
	}
	if !grant.Applied {
		t.Fatalf("alert mode grant must be applied")
	}
	if perms := g.Permissions("tenant-1", "user-4"); len(perms) != 2 {
		t.Fatalf("permissions = %v, want both sides under alert mode", perms)
	}
}

func TestGrantWithCheckBlocksAndReports(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	if _, err := g.GrantWithCheck("tenant-1", "user-5", "pay.approve"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	grant, err := g.GrantWithCheck("tenant-1", "user-5", "pay.pay")
	if !errors.Is(err, impl.ErrGrantBlocked) {
		t.Fatalf("conflicting grant must fail with ErrGrantBlocked, got %v", err)
	}
	if grant.Applied {
		t.Fatalf("blocked grant must not be applied: %+v", grant)
	}
	if grant.Perm != "pay.pay" {
		t.Fatalf("blocked grant perm = %q, want pay.pay", grant.Perm)
	}
	if perms := g.Permissions("tenant-1", "user-5"); len(perms) != 1 || perms[0] != "pay.approve" {
		t.Fatalf("permissions after the blocked grant = %v, want only pay.approve", perms)
	}
	other, err := g.GrantWithCheck("tenant-1", "user-6", "pay.pay")
	if err != nil {
		t.Fatalf("the other side must be grantable to a different subject: %v", err)
	}
	if !other.Applied {
		t.Fatalf("a clean subject must receive the grant")
	}
}

func TestConcurrentGrantSingleWinner(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners, blocked := 0, 0
	var unexpected []error
	for i := 0; i < 8; i++ {
		subject := fmt.Sprintf("racer-%d", i)
		for _, perm := range []string{"pay.approve", "pay.pay"} {
			wg.Add(1)
			go func(subject, perm string) {
				defer wg.Done()
				grant, err := g.GrantWithCheck("tenant-1", subject, perm)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil && grant.Applied:
					winners++
				case errors.Is(err, impl.ErrGrantBlocked):
					blocked++
				default:
					unexpected = append(unexpected, err)
				}
			}(subject, perm)
		}
	}
	wg.Wait()
	if len(unexpected) != 0 {
		t.Fatalf("concurrent grants returned unexpected errors: %v", unexpected)
	}
	if winners != 8 {
		t.Fatalf("concurrent grants applied %d times, want exactly one per subject (8)", winners)
	}
	if blocked != 8 {
		t.Fatalf("concurrent grants blocked %d times, want the other half (8)", blocked)
	}
	for i := 0; i < 8; i++ {
		subject := fmt.Sprintf("racer-%d", i)
		if perms := g.Permissions("tenant-1", subject); len(perms) != 1 {
			t.Fatalf("subject %s holds %v, want exactly one side of the pair", subject, perms)
		}
	}
}

func TestExemptAllowsTemporaryGrant(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if _, err := g.GrantWithCheck("tenant-1", "user-7", "pay.approve"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	until := now.Add(time.Hour)
	exemption, err := g.Exempt("tenant-1", "user-7", pair.ID, until, "quarter end close")
	if err != nil {
		t.Fatalf("exempt: %v", err)
	}
	if exemption.ID == "" {
		t.Fatalf("exemption must carry an id")
	}
	if exemption.TenantID != "tenant-1" || exemption.SubjectID != "user-7" || exemption.PairID != pair.ID {
		t.Fatalf("exemption = %+v, want it bound to tenant-1 / user-7 / %s", exemption, pair.ID)
	}
	if exemption.Until != until.UnixNano() {
		t.Fatalf("exemption until = %d, want %d", exemption.Until, until.UnixNano())
	}
	if exemption.Reason != "quarter end close" {
		t.Fatalf("exemption reason = %q", exemption.Reason)
	}
	verdict, err := g.Check("tenant-1", "user-7", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check under exemption: %v", err)
	}
	if !verdict.Allowed || verdict.Reason != impl.ReasonExempted {
		t.Fatalf("exemption check got allowed=%v reason=%q, want allowed and %q", verdict.Allowed, verdict.Reason, impl.ReasonExempted)
	}
	grant, err := g.GrantWithCheck("tenant-1", "user-7", "pay.pay")
	if err != nil {
		t.Fatalf("exempted grant: %v", err)
	}
	if !grant.Applied {
		t.Fatalf("exempted grant must be applied")
	}
	if got := len(g.Exemptions("tenant-1", "user-7")); got != 1 {
		t.Fatalf("exemption list = %d records, want 1", got)
	}
}

func TestExemptRequiresValidUntilAndReason(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if _, err := g.Exempt("tenant-1", "user-8", pair.ID, now.Add(-time.Minute), "too late"); !errors.Is(err, impl.ErrInvalidExemption) {
		t.Fatalf("a closed window must fail with ErrInvalidExemption, got %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-8", pair.ID, now, "zero window"); !errors.Is(err, impl.ErrInvalidExemption) {
		t.Fatalf("an already elapsed window must fail with ErrInvalidExemption, got %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-8", pair.ID, now.Add(time.Hour), "   "); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank reason must fail with ErrBlankArg, got %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-8", "pair-404", now.Add(time.Hour), "reason"); !errors.Is(err, impl.ErrPairNotFound) {
		t.Fatalf("unknown pair must fail with ErrPairNotFound, got %v", err)
	}
	if _, err := g.Exempt("tenant-1", "", pair.ID, now.Add(time.Hour), "reason"); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank subject must fail with ErrBlankArg, got %v", err)
	}
	if got := len(g.Exemptions("tenant-1", "user-8")); got != 0 {
		t.Fatalf("rejected exemptions left %d records behind", got)
	}
}

func TestExemptionCapPerSubject(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, func(cfg *impl.SodConfig) { cfg.MaxExemptionsPerSubject = 1 })
	if err := g.AddConflict("tenant-1", "a", "b", impl.ModeBlock); err != nil {
		t.Fatalf("first pair: %v", err)
	}
	if err := g.AddConflict("tenant-1", "c", "d", impl.ModeBlock); err != nil {
		t.Fatalf("second pair: %v", err)
	}
	pairs := g.Conflicts("tenant-1")
	if len(pairs) != 2 {
		t.Fatalf("pair count = %d, want 2", len(pairs))
	}
	if _, err := g.Exempt("tenant-1", "user-9", pairs[0].ID, time.Unix(2000, 0), "first"); err != nil {
		t.Fatalf("first exemption: %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-9", pairs[1].ID, time.Unix(2000, 0), "second"); !errors.Is(err, impl.ErrExemptionLimit) {
		t.Fatalf("second exemption must hit the subject cap, got %v", err)
	}
	if got := len(g.Exemptions("tenant-1", "user-9")); got != 1 {
		t.Fatalf("exemption list = %d records, want the cap 1", got)
	}
}

func TestExpireRestoresBlocking(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if _, err := g.GrantWithCheck("tenant-1", "user-10", "pay.approve"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-10", pair.ID, now.Add(time.Hour), "close window"); err != nil {
		t.Fatalf("exempt: %v", err)
	}
	before, err := g.Check("tenant-1", "user-10", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check before expiry: %v", err)
	}
	if !before.Allowed {
		t.Fatalf("the live exemption must allow the addition")
	}
	now = now.Add(2 * time.Hour)
	if removed := g.Expire(now); removed != 1 {
		t.Fatalf("expire removed %d exemptions, want 1", removed)
	}
	if got := len(g.Exemptions("tenant-1", "user-10")); got != 0 {
		t.Fatalf("expired exemption still listed: %d", got)
	}
	after, err := g.Check("tenant-1", "user-10", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check after expiry: %v", err)
	}
	if after.Allowed || after.Action != impl.ActionBlock {
		t.Fatalf("after expiry got allowed=%v action=%q, want block", after.Allowed, after.Action)
	}
	if _, err := g.GrantWithCheck("tenant-1", "user-10", "pay.pay"); !errors.Is(err, impl.ErrGrantBlocked) {
		t.Fatalf("a fresh grant after expiry must fail with ErrGrantBlocked, got %v", err)
	}
}

func TestRolesGiveEffectivePermissions(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.DefineRole("tenant-1", "approver", []string{"pay.approve", "report.read"}); err != nil {
		t.Fatalf("define role: %v", err)
	}
	if err := g.DefineRole("tenant-1", "approver", []string{"report.read"}); !errors.Is(err, impl.ErrRoleExists) {
		t.Fatalf("redefining a role must fail with ErrRoleExists, got %v", err)
	}
	if err := g.DefineRole("", "empty", []string{"report.read"}); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank tenant must fail with ErrBlankArg, got %v", err)
	}
	if err := g.DefineRole("tenant-1", "", []string{"report.read"}); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank role must fail with ErrBlankArg, got %v", err)
	}
	if err := g.AssignRole("tenant-1", "user-11", "cashier"); !errors.Is(err, impl.ErrUnknownRole) {
		t.Fatalf("assigning an unknown role must fail with ErrUnknownRole, got %v", err)
	}
	if err := g.AssignRole("tenant-1", "user-11", "approver"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if err := g.AssignRole("tenant-1", "user-11", "approver"); err != nil {
		t.Fatalf("assigning the same role twice must stay idempotent: %v", err)
	}
	if _, err := g.GrantWithCheck("tenant-1", "user-11", "report.write"); err != nil {
		t.Fatalf("direct grant: %v", err)
	}
	perms := g.Permissions("tenant-1", "user-11")
	want := []string{"pay.approve", "report.read", "report.write"}
	if len(perms) != len(want) {
		t.Fatalf("effective permissions = %v, want %v", perms, want)
	}
	for i := range want {
		if perms[i] != want[i] {
			t.Fatalf("effective permissions = %v, want %v", perms, want)
		}
	}
	if roles := g.Roles("tenant-1", "user-11"); len(roles) != 1 || roles[0] != "approver" {
		t.Fatalf("roles = %v, want [approver]", roles)
	}
	if err := g.Revoke("tenant-1", "user-11", "pay.approve"); !errors.Is(err, impl.ErrInheritedPerm) {
		t.Fatalf("revoking an inherited permission must fail with ErrInheritedPerm, got %v", err)
	}
	if err := g.Revoke("tenant-1", "user-11", "report.write"); err != nil {
		t.Fatalf("revoking a direct grant: %v", err)
	}
	if perms := g.Permissions("tenant-1", "user-11"); len(perms) != 2 {
		t.Fatalf("permissions after revoke = %v, want the two inherited ones", perms)
	}
	if perms := g.Permissions("tenant-1", "user-12"); len(perms) != 0 {
		t.Fatalf("unknown subject must hold nothing, got %v", perms)
	}
}

func TestAuditRingBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, func(cfg *impl.SodConfig) { cfg.MaxAuditPerTenant = 3 })
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	for i := 0; i < 5; i++ {
		subject := fmt.Sprintf("audit-%d", i)
		if _, err := g.GrantWithCheck("tenant-1", subject, "pay.approve"); err != nil {
			t.Fatalf("side grant %d: %v", i, err)
		}
		if _, err := g.GrantWithCheck("tenant-1", subject, "pay.pay"); !errors.Is(err, impl.ErrGrantBlocked) {
			t.Fatalf("blocked grant %d must fail with ErrGrantBlocked, got %v", i, err)
		}
	}
	records := g.Audit("tenant-1")
	if len(records) != 3 {
		t.Fatalf("audit records = %d, want the cap 3", len(records))
	}
	if records[0].Seq != 3 || records[2].Seq != 5 {
		t.Fatalf("retained sequences = %d..%d, want 3..5", records[0].Seq, records[2].Seq)
	}
	for _, record := range records {
		if record.TenantID != "tenant-1" || record.Action != impl.ActionBlock {
			t.Fatalf("audit record = %+v, want a block inside tenant-1", record)
		}
		if len(record.PairIDs) != 1 || record.PairIDs[0] != pair.ID {
			t.Fatalf("audit record pair ids = %v, want [%s]", record.PairIDs, pair.ID)
		}
	}
	if got := len(g.Audit("tenant-2")); got != 0 {
		t.Fatalf("audit must stay inside its tenant, got %d records", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	now := time.Unix(1000, 0)
	g := sodGuard(t, &now, nil)
	if err := g.AddConflict("tenant-1", "pay.approve", "pay.pay", impl.ModeBlock); err != nil {
		t.Fatalf("add conflict: %v", err)
	}
	pair := sodPair(t, g, "tenant-1")
	if err := g.DefineRole("tenant-1", "approver", []string{"report.read"}); err != nil {
		t.Fatalf("define role: %v", err)
	}
	if err := g.AssignRole("tenant-1", "user-13", "approver"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if _, err := g.GrantWithCheck("tenant-1", "user-13", "pay.approve"); err != nil {
		t.Fatalf("grant one side: %v", err)
	}
	if _, err := g.Exempt("tenant-1", "user-13", pair.ID, now.Add(time.Hour), "close"); err != nil {
		t.Fatalf("exempt: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := sodGuard(t, &now, nil)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restored.Conflicts("tenant-1")); got != 1 {
		t.Fatalf("restored pair count = %d, want 1", got)
	}
	perms := restored.Permissions("tenant-1", "user-13")
	if len(perms) != 2 {
		t.Fatalf("restored permissions = %v, want pay.approve plus the inherited report.read", perms)
	}
	if got := len(restored.Exemptions("tenant-1", "user-13")); got != 1 {
		t.Fatalf("restored exemptions = %d, want 1", got)
	}
	allowed, err := restored.Check("tenant-1", "user-13", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check after restore: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("the exemption must survive the restart")
	}
	if removed := restored.Expire(now.Add(2 * time.Hour)); removed != 1 {
		t.Fatalf("expire after restore removed %d, want 1", removed)
	}
	blocked, err := restored.Check("tenant-1", "user-13", impl.PermissionSet{"pay.pay"})
	if err != nil {
		t.Fatalf("check after expiry: %v", err)
	}
	if blocked.Allowed {
		t.Fatalf("the restored exemption must still expire with its window")
	}
	if err := restored.Restore([]byte(`{"version":7}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a foreign snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	if err := restored.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage must fail with ErrBadSnapshot, got %v", err)
	}
	if got := len(restored.Conflicts("tenant-1")); got != 1 {
		t.Fatalf("a rejected restore must not touch the live pairs, got %d", got)
	}
	if perms := restored.Permissions("tenant-1", "user-13"); len(perms) != 2 {
		t.Fatalf("a rejected restore must not touch the live grants, got %v", perms)
	}
}
