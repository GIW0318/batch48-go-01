// Package task narrows token audiences to registered resource indicators,
// rejects cross resource reuse and keeps in use tokens usable after one of
// their resources is retired.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the guard.
var (
	ErrBlankTenant       = errors.New("blank tenant")
	ErrBlankResource     = errors.New("blank resource id")
	ErrBlankClient       = errors.New("blank client")
	ErrBlankSubject      = errors.New("blank subject")
	ErrInvalidURI        = errors.New("invalid resource uri")
	ErrNoScope           = errors.New("no scope")
	ErrDuplicateResource = errors.New("duplicate resource")
	ErrResourceLimit     = errors.New("resource limit")
	ErrUnknownResource   = errors.New("unknown resource")
	ErrUnknownClient     = errors.New("unknown client")
	ErrClientExists      = errors.New("client exists")
	ErrNoAudience        = errors.New("no audience")
	ErrAudienceLimit     = errors.New("audience limit")
	ErrAudienceMismatch  = errors.New("audience mismatch")
	ErrResourceRetired   = errors.New("resource retired")
	ErrMalformedToken    = errors.New("malformed token")
	ErrUnknownToken      = errors.New("unknown token")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Verdict reasons.
const (
	ReasonOK               = "ok"
	ReasonAudienceMismatch = "audience_mismatch"
	ReasonResourceRetired  = "resource_retired"
	ReasonForged           = "token_forged"
)

const tokenPrefix = "rag1."

// AudienceLimits bounds the tenant resource table and the token audience set.
type AudienceLimits struct {
	MaxResourcesPerTenant int
	MaxAudiencesPerToken  int
}

// Resource is one registered resource indicator of a tenant.
type Resource struct {
	ResourceID string
	TenantID   string
	URI        string
	Scopes     []string
	Retired    bool
}

// Token is an access token scoped to a set of registered resources.
type Token struct {
	TokenID   string
	ClientID  string
	TenantID  string
	Subject   string
	Audiences []string
	Scopes    []string
	IssuedAt  time.Time
}

// Verdict is the outcome of one audience check.
type Verdict struct {
	OK                 bool
	TokenID            string
	Reason             string
	RemainingAudiences []string
}

// ResourceAudienceGuard keeps resources, clients and issued tokens.
type ResourceAudienceGuard struct {
	mu        sync.Mutex
	limits    AudienceLimits
	resources map[string]*Resource
	byURI     map[string]string
	clients   map[string]string
	tokens    map[string]*Token
	seq       int
}

// NewResourceAudienceGuard builds a guard with the supplied limits.
func NewResourceAudienceGuard(limits AudienceLimits) *ResourceAudienceGuard {
	return &ResourceAudienceGuard{
		limits:    limits,
		resources: make(map[string]*Resource),
		byURI:     make(map[string]string),
		clients:   make(map[string]string),
		tokens:    make(map[string]*Token),
	}
}

// NormalizeURI folds a resource indicator into its canonical form.
func (g *ResourceAudienceGuard) NormalizeURI(raw string) (string, error) {
	return "", errNotImplemented
}

// RegisterClient records the tenant a client belongs to.
func (g *ResourceAudienceGuard) RegisterClient(clientID, tenantID string) error {
	return errNotImplemented
}

// RegisterResource adds one resource indicator to the tenant table.
func (g *ResourceAudienceGuard) RegisterResource(tenantID, resourceID, uri string, scopes []string) error {
	return errNotImplemented
}

// Resources lists the registered resources of one tenant ordered by id.
func (g *ResourceAudienceGuard) Resources(tenantID string) []Resource {
	return nil
}

// Narrow resolves requested resource ids into the audience values of a client.
func (g *ResourceAudienceGuard) Narrow(clientID string, requested []string) ([]string, error) {
	return nil, errNotImplemented
}

// Issue mints a token whose audiences are registered resources of the client.
func (g *ResourceAudienceGuard) Issue(clientID, subject string, audiences, scopes []string) (Token, error) {
	return Token{}, errNotImplemented
}

// Present builds the opaque token string a client sends on the wire.
func (g *ResourceAudienceGuard) Present(tokenID string) (string, error) {
	return "", errNotImplemented
}

// VerifyAudience checks that a token was issued for the presented resource.
func (g *ResourceAudienceGuard) VerifyAudience(rawToken, resourceURI string) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// RetireResource retires one resource and drops it from every live token.
func (g *ResourceAudienceGuard) RetireResource(resourceID string) (int, error) {
	return 0, errNotImplemented
}

// Tokens lists the live tokens of one client ordered by token id.
func (g *ResourceAudienceGuard) Tokens(clientID string) []Token {
	return nil
}

// Snapshot serialises resources, clients and tokens.
func (g *ResourceAudienceGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces resources, clients and tokens with a snapshot.
func (g *ResourceAudienceGuard) Restore(data []byte) error {
	return errNotImplemented
}
