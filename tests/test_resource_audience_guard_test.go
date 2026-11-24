package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-a-15"
)

const (
	tenantOne  = "tenant-one"
	testClient = "client-web"
	uriAPI     = "https://api.example.com/v1"
	uriFiles   = "https://files.example.com/v1"
	uriAdmin   = "https://admin.example.com/v1"
)

func newAudienceGuard() *impl.ResourceAudienceGuard {
	return impl.NewResourceAudienceGuard(impl.AudienceLimits{
		MaxResourcesPerTenant: 4,
		MaxAudiencesPerToken:  3,
	})
}

// seedTenant registers two resources and one client for the shared tenant.
func seedTenant(t *testing.T, g *impl.ResourceAudienceGuard) {
	t.Helper()
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, []string{"read"}); err != nil {
		t.Fatalf("register res-api failed: %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-files", uriFiles, []string{"read", "write"}); err != nil {
		t.Fatalf("register res-files failed: %v", err)
	}
	if err := g.RegisterClient(testClient, tenantOne); err != nil {
		t.Fatalf("register client failed: %v", err)
	}
}

func TestRegisterResourceRejectsBlankInputs(t *testing.T) {
	g := newAudienceGuard()
	if err := g.RegisterResource("  ", "res-api", uriAPI, []string{"read"}); !errors.Is(err, impl.ErrBlankTenant) {
		t.Fatalf("blank tenant must be rejected with ErrBlankTenant, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "  ", uriAPI, []string{"read"}); !errors.Is(err, impl.ErrBlankResource) {
		t.Fatalf("blank resource id must be rejected with ErrBlankResource, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-api", "not-a-uri", []string{"read"}); !errors.Is(err, impl.ErrInvalidURI) {
		t.Fatalf("bad resource uri must be rejected with ErrInvalidURI, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, nil); !errors.Is(err, impl.ErrNoScope) {
		t.Fatalf("resource without scopes must be rejected with ErrNoScope, got %v", err)
	}
}

func TestRegisterResourceStoresCanonicalResource(t *testing.T) {
	g := newAudienceGuard()
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, []string{"write", "read", "read"}); err != nil {
		t.Fatalf("register must succeed, got %v", err)
	}
	resources := g.Resources(tenantOne)
	if len(resources) != 1 {
		t.Fatalf("tenant resource count = %d, want 1", len(resources))
	}
	resource := resources[0]
	if resource.ResourceID != "res-api" || resource.TenantID != tenantOne || resource.URI != uriAPI {
		t.Fatalf("stored resource = %+v, want res-api/%s/%s", resource, tenantOne, uriAPI)
	}
	if len(resource.Scopes) != 2 || resource.Scopes[0] != "read" || resource.Scopes[1] != "write" {
		t.Fatalf("stored scopes = %v, want sorted and deduplicated [read write]", resource.Scopes)
	}
	if resource.Retired {
		t.Fatalf("freshly registered resource must not be retired")
	}
}

func TestRegisterResourceRejectsDuplicateURI(t *testing.T) {
	g := newAudienceGuard()
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, []string{"read"}); err != nil {
		t.Fatalf("first registration must succeed, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-api-second", uriAPI, []string{"read"}); !errors.Is(err, impl.ErrDuplicateResource) {
		t.Fatalf("second resource with the same uri must report ErrDuplicateResource, got %v", err)
	}
	if got := g.Resources(tenantOne); len(got) != 1 {
		t.Fatalf("duplicate registration must not grow the table, got %d entries", len(got))
	}
}

func TestRegisterResourceEnforcesTenantCap(t *testing.T) {
	g := impl.NewResourceAudienceGuard(impl.AudienceLimits{
		MaxResourcesPerTenant: 2,
		MaxAudiencesPerToken:  3,
	})
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, []string{"read"}); err != nil {
		t.Fatalf("first registration must succeed, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-files", uriFiles, []string{"read"}); err != nil {
		t.Fatalf("second registration must succeed, got %v", err)
	}
	if err := g.RegisterResource(tenantOne, "res-admin", uriAdmin, []string{"read"}); !errors.Is(err, impl.ErrResourceLimit) {
		t.Fatalf("registration past the tenant cap must report ErrResourceLimit, got %v", err)
	}
	if got := g.Resources(tenantOne); len(got) != 2 {
		t.Fatalf("tenant resource count after the cap = %d, want 2", len(got))
	}
}

func TestRegisterResourcesAreTenantScoped(t *testing.T) {
	g := newAudienceGuard()
	if err := g.RegisterResource(tenantOne, "res-api", uriAPI, []string{"read"}); err != nil {
		t.Fatalf("registration in tenant-one failed: %v", err)
	}
	if err := g.RegisterResource("tenant-two", "res-admin", uriAdmin, []string{"read"}); err != nil {
		t.Fatalf("registration in tenant-two failed: %v", err)
	}
	if got := g.Resources(tenantOne); len(got) != 1 || got[0].ResourceID != "res-api" {
		t.Fatalf("tenant-one resources = %+v, want only res-api", got)
	}
	if got := g.Resources("tenant-two"); len(got) != 1 || got[0].ResourceID != "res-admin" {
		t.Fatalf("tenant-two resources = %+v, want only res-admin", got)
	}
}

func TestRegisterClientBindsTenant(t *testing.T) {
	g := newAudienceGuard()
	if err := g.RegisterClient(testClient, tenantOne); err != nil {
		t.Fatalf("client registration must succeed, got %v", err)
	}
	if err := g.RegisterClient(testClient, tenantOne); !errors.Is(err, impl.ErrClientExists) {
		t.Fatalf("duplicate client registration must report ErrClientExists, got %v", err)
	}
	if err := g.RegisterClient("  ", tenantOne); !errors.Is(err, impl.ErrBlankClient) {
		t.Fatalf("blank client must be rejected with ErrBlankClient, got %v", err)
	}
	if _, err := g.Issue("ghost-client", "user-1", []string{uriAPI}, []string{"read"}); !errors.Is(err, impl.ErrUnknownClient) {
		t.Fatalf("issuing for an unknown client must report ErrUnknownClient, got %v", err)
	}
}

func TestNarrowMapsAndDedupesRequests(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	got, err := g.Narrow(testClient, []string{"res-files", "res-api", "res-files"})
	if err != nil {
		t.Fatalf("narrow must succeed, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("narrowed audiences = %v, want 2 entries", got)
	}
	if got[0] != uriFiles || got[1] != uriAPI {
		t.Fatalf("narrowed audiences = %v, want [%s %s] in request order", got, uriFiles, uriAPI)
	}
}

func TestNarrowEnforcesAudienceCap(t *testing.T) {
	g := impl.NewResourceAudienceGuard(impl.AudienceLimits{
		MaxResourcesPerTenant: 4,
		MaxAudiencesPerToken:  2,
	})
	seedTenant(t, g)
	if err := g.RegisterResource(tenantOne, "res-admin", uriAdmin, []string{"read"}); err != nil {
		t.Fatalf("third registration failed: %v", err)
	}
	if _, err := g.Narrow(testClient, []string{"res-api", "res-files", "res-admin"}); !errors.Is(err, impl.ErrAudienceLimit) {
		t.Fatalf("narrowing past the audience cap must report ErrAudienceLimit, got %v", err)
	}
}

func TestIssueRejectsBlankSubject(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	if _, err := g.Issue(testClient, "   ", []string{uriAPI}, []string{"read"}); !errors.Is(err, impl.ErrBlankSubject) {
		t.Fatalf("blank subject must be rejected with ErrBlankSubject, got %v", err)
	}
	if _, err := g.Issue(testClient, "user-1", nil, []string{"read"}); !errors.Is(err, impl.ErrNoAudience) {
		t.Fatalf("empty audience set must be rejected with ErrNoAudience, got %v", err)
	}
}

func TestIssueEnforcesAudienceCap(t *testing.T) {
	g := impl.NewResourceAudienceGuard(impl.AudienceLimits{
		MaxResourcesPerTenant: 4,
		MaxAudiencesPerToken:  2,
	})
	seedTenant(t, g)
	if err := g.RegisterResource(tenantOne, "res-admin", uriAdmin, []string{"read"}); err != nil {
		t.Fatalf("third registration failed: %v", err)
	}
	if _, err := g.Issue(testClient, "user-1", []string{uriAPI, uriFiles, uriAdmin}, []string{"read"}); !errors.Is(err, impl.ErrAudienceLimit) {
		t.Fatalf("issuing past the audience cap must report ErrAudienceLimit, got %v", err)
	}
}

func TestIssueBuildsTokenWithSortedAudiences(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	token, err := g.Issue(testClient, "user-1", []string{uriFiles, uriAPI}, []string{"write", "read", "write"})
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if len(token.Audiences) != 2 || token.Audiences[0] != uriAPI || token.Audiences[1] != uriFiles {
		t.Fatalf("token audiences = %v, want sorted [%s %s]", token.Audiences, uriAPI, uriFiles)
	}
	if len(token.Scopes) != 2 || token.Scopes[0] != "read" || token.Scopes[1] != "write" {
		t.Fatalf("token scopes = %v, want sorted and deduplicated [read write]", token.Scopes)
	}
	if token.TenantID != tenantOne || token.ClientID != testClient || token.Subject != "user-1" {
		t.Fatalf("token identity = %+v, want %s/%s/user-1", token, tenantOne, testClient)
	}
}

func TestVerifyAudienceAcceptsCoveredResource(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	token, err := g.Issue(testClient, "user-1", []string{uriAPI}, []string{"read"})
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	verdict, err := g.VerifyAudience(raw, uriAPI)
	if err != nil {
		t.Fatalf("covered resource must be accepted, got %v", err)
	}
	if !verdict.OK || verdict.Reason != "ok" || verdict.TokenID != token.TokenID {
		t.Fatalf("verdict = %+v, want ok for %s", verdict, token.TokenID)
	}
}

func TestVerifyAudienceRejectsUncoveredResource(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	token, err := g.Issue(testClient, "user-1", []string{uriAPI}, []string{"read"})
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	verdict, err := g.VerifyAudience(raw, uriFiles)
	if !errors.Is(err, impl.ErrAudienceMismatch) {
		t.Fatalf("resource outside the token audience must report ErrAudienceMismatch, got %v", err)
	}
	if verdict.OK || verdict.Reason != "audience_mismatch" {
		t.Fatalf("verdict = %+v, want a non ok audience_mismatch verdict", verdict)
	}
}

func TestRetireResourceDropsSingleAudience(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	token, err := g.Issue(testClient, "user-1", []string{uriAPI}, []string{"read"})
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	affected, err := g.RetireResource("res-api")
	if err != nil {
		t.Fatalf("retirement must succeed, got %v", err)
	}
	if affected != 1 {
		t.Fatalf("retirement affected %d tokens, want 1", affected)
	}
	verdict, err := g.VerifyAudience(raw, uriAPI)
	if !errors.Is(err, impl.ErrResourceRetired) {
		t.Fatalf("retired resource must report ErrResourceRetired, got %v", err)
	}
	if verdict.OK || verdict.Reason != "resource_retired" {
		t.Fatalf("verdict = %+v, want a non ok resource_retired verdict", verdict)
	}
}

func TestSnapshotRestoreKeepsResourcesAndTokens(t *testing.T) {
	g := newAudienceGuard()
	seedTenant(t, g)
	token, err := g.Issue(testClient, "user-1", []string{uriAPI, uriFiles}, []string{"read"})
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}

	restored := newAudienceGuard()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Resources(tenantOne); len(got) != 2 {
		t.Fatalf("restored tenant resources = %d, want 2", len(got))
	}
	if got, err := restored.Narrow(testClient, []string{"res-api"}); err != nil || len(got) != 1 || got[0] != uriAPI {
		t.Fatalf("narrow after restore = %v/%v, want [%s]", got, err, uriAPI)
	}
	verdict, err := restored.VerifyAudience(raw, uriAPI)
	if err != nil {
		t.Fatalf("restored token must verify, got %v", err)
	}
	if !verdict.OK || verdict.TokenID != token.TokenID {
		t.Fatalf("restored verdict = %+v, want ok for %s", verdict, token.TokenID)
	}
}
