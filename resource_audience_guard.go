// Package task narrows token audiences to registered resource indicators,
// rejects cross resource reuse and keeps in use tokens usable after one of
// their resources is retired.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

const (
	tokenPrefix     = "rag1."
	tokenIDPrefix   = "rag-"
	tokenIDWidth    = 16
	snapshotVersion = 1
)

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
//
// A single mutex serialises every registration, issuance, retirement and
// verification. State is replaced atomically on restore so readers never
// observe a half applied update.
type ResourceAudienceGuard struct {
	mu        sync.Mutex
	limits    AudienceLimits
	resources map[string]*Resource
	byURI     map[string]string
	clients   map[string]string
	tokens    map[string]*Token
	seq       int
}

// NewResourceAudienceGuard builds a guard with the supplied limits. A non
// positive limit means the corresponding set is unbounded.
func NewResourceAudienceGuard(limits AudienceLimits) *ResourceAudienceGuard {
	return &ResourceAudienceGuard{
		limits:    limits,
		resources: make(map[string]*Resource),
		byURI:     make(map[string]string),
		clients:   make(map[string]string),
		tokens:    make(map[string]*Token),
	}
}

// NormalizeURI folds a resource indicator into its canonical form. Scheme and
// host are case folded, a trailing slash is collapsed and the result is
// required to be an absolute URI without a fragment, matching RFC 8707.
func (g *ResourceAudienceGuard) NormalizeURI(raw string) (string, error) {
	indicator := strings.TrimSpace(raw)
	if indicator == "" {
		return "", ErrInvalidURI
	}

	parsed, err := url.Parse(indicator)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", ErrInvalidURI
	}
	if parsed.Fragment != "" {
		return "", ErrInvalidURI
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	switch {
	case port != "":
		parsed.Host = net.JoinHostPort(hostname, port)
	case strings.Contains(hostname, ":"):
		parsed.Host = "[" + hostname + "]"
	default:
		parsed.Host = hostname
	}
	if parsed.Path != "" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = ""
	}

	return parsed.String(), nil
}

// RegisterClient records the tenant a client belongs to.
func (g *ResourceAudienceGuard) RegisterClient(clientID, tenantID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrBlankClient
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ErrBlankTenant
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.clients[clientID]; exists {
		return ErrClientExists
	}
	g.clients[clientID] = tenantID
	return nil
}

// RegisterResource adds one resource indicator to the tenant table.
func (g *ResourceAudienceGuard) RegisterResource(tenantID, resourceID, uri string, scopes []string) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ErrBlankTenant
	}
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return ErrBlankResource
	}

	canonicalURI, err := g.NormalizeURI(uri)
	if err != nil {
		return err
	}

	normalizedScopes := normalizeScopes(scopes)
	if len(normalizedScopes) == 0 {
		return ErrNoScope
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.resources[resourceID]; exists {
		return ErrDuplicateResource
	}
	if _, exists := g.byURI[canonicalURI]; exists {
		return ErrDuplicateResource
	}
	if limit := g.limits.MaxResourcesPerTenant; limit > 0 && g.countTenantResourcesLocked(tenantID) >= limit {
		return ErrResourceLimit
	}

	resource := &Resource{
		ResourceID: resourceID,
		TenantID:   tenantID,
		URI:        canonicalURI,
		Scopes:     normalizedScopes,
		Retired:    false,
	}
	g.resources[resourceID] = resource
	g.byURI[canonicalURI] = resourceID
	return nil
}

// Resources lists the registered resources of one tenant ordered by id.
// Retired resources stay listed with Retired set so callers can tell an
// offline indicator from an unknown one.
func (g *ResourceAudienceGuard) Resources(tenantID string) []Resource {
	tenantID = strings.TrimSpace(tenantID)

	g.mu.Lock()
	defer g.mu.Unlock()

	resources := make([]Resource, 0)
	for _, resource := range g.resources {
		if resource.TenantID != tenantID {
			continue
		}
		resources = append(resources, *cloneResource(resource))
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].ResourceID < resources[j].ResourceID
	})
	return resources
}

// Narrow resolves requested resource ids into the audience values of a client.
// Duplicates collapse while the first occurrence order is preserved.
func (g *ResourceAudienceGuard) Narrow(clientID string, requested []string) ([]string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	tenantID, known := g.clients[clientID]
	if !known {
		return nil, ErrUnknownClient
	}

	narrowed := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, rawResourceID := range requested {
		resourceID := strings.TrimSpace(rawResourceID)
		if resourceID == "" {
			return nil, ErrBlankResource
		}
		resource, ok := g.resources[resourceID]
		if !ok || resource.TenantID != tenantID {
			return nil, ErrUnknownResource
		}
		if _, duplicate := seen[resource.URI]; duplicate {
			continue
		}
		seen[resource.URI] = struct{}{}
		narrowed = append(narrowed, resource.URI)
	}

	if limit := g.limits.MaxAudiencesPerToken; limit > 0 && len(narrowed) > limit {
		return nil, ErrAudienceLimit
	}
	return narrowed, nil
}

// Issue mints a token whose audiences are registered resources of the client.
// Every audience must resolve to a resource owned by the client tenant; any
// unknown or cross tenant indicator rejects the whole issuance so no partial
// token can be minted.
func (g *ResourceAudienceGuard) Issue(clientID, subject string, audiences, scopes []string) (Token, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return Token{}, ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	tenantID, known := g.clients[clientID]
	if !known {
		return Token{}, ErrUnknownClient
	}
	if strings.TrimSpace(subject) == "" {
		return Token{}, ErrBlankSubject
	}
	if len(audiences) == 0 {
		return Token{}, ErrNoAudience
	}

	canonicalAudiences := make([]string, 0, len(audiences))
	seen := make(map[string]struct{}, len(audiences))
	for _, rawAudience := range audiences {
		canonicalURI, err := g.NormalizeURI(rawAudience)
		if err != nil {
			return Token{}, err
		}
		resourceID, registered := g.byURI[canonicalURI]
		if !registered {
			return Token{}, ErrUnknownResource
		}
		resource := g.resources[resourceID]
		if resource == nil || resource.TenantID != tenantID {
			return Token{}, ErrUnknownResource
		}
		if _, duplicate := seen[canonicalURI]; duplicate {
			continue
		}
		seen[canonicalURI] = struct{}{}
		canonicalAudiences = append(canonicalAudiences, canonicalURI)
	}

	sort.Strings(canonicalAudiences)
	if limit := g.limits.MaxAudiencesPerToken; limit > 0 && len(canonicalAudiences) > limit {
		return Token{}, ErrAudienceLimit
	}

	g.seq++
	tokenID := tokenIDPrefix + fmt.Sprintf("%0*d", tokenIDWidth, g.seq)
	token := &Token{
		TokenID:   tokenID,
		ClientID:  clientID,
		TenantID:  tenantID,
		Subject:   strings.TrimSpace(subject),
		Audiences: canonicalAudiences,
		Scopes:    normalizeScopes(scopes),
		IssuedAt:  time.Now().UTC(),
	}
	g.tokens[tokenID] = token
	return *cloneToken(token), nil
}

// Present builds the opaque token string a client sends on the wire.
func (g *ResourceAudienceGuard) Present(tokenID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.tokens[tokenID]; !ok {
		return "", ErrUnknownToken
	}
	return tokenPrefix + tokenID, nil
}

// VerifyAudience checks that a token was issued for the presented resource.
// A retired indicator always yields the resource retired reason, while an
// active indicator outside the current audience set yields audience mismatch.
func (g *ResourceAudienceGuard) VerifyAudience(rawToken, resourceURI string) (Verdict, error) {
	tokenID, err := parsePresentedToken(rawToken)
	if err != nil {
		return Verdict{OK: false, Reason: ReasonForged}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	token, ok := g.tokens[tokenID]
	if !ok {
		return Verdict{OK: false, TokenID: tokenID, Reason: ReasonForged}, ErrUnknownToken
	}

	remaining := cloneStrings(token.Audiences)
	canonicalURI, normalizeErr := g.NormalizeURI(resourceURI)

	covered := false
	if normalizeErr == nil {
		for _, audience := range token.Audiences {
			if audience == canonicalURI {
				covered = true
				break
			}
		}
	}

	retired := false
	if normalizeErr == nil {
		if resourceID, registered := g.byURI[canonicalURI]; registered {
			if resource := g.resources[resourceID]; resource != nil {
				retired = resource.Retired
			}
		}
	}

	switch {
	case covered && !retired:
		return Verdict{
			OK:                 true,
			TokenID:            token.TokenID,
			Reason:             ReasonOK,
			RemainingAudiences: remaining,
		}, nil
	case retired:
		return Verdict{
			OK:                 false,
			TokenID:            token.TokenID,
			Reason:             ReasonResourceRetired,
			RemainingAudiences: remaining,
		}, ErrResourceRetired
	default:
		return Verdict{
			OK:                 false,
			TokenID:            token.TokenID,
			Reason:             ReasonAudienceMismatch,
			RemainingAudiences: remaining,
		}, ErrAudienceMismatch
	}
}

// RetireResource retires one resource and drops its audience from every live
// token. Multi audience tokens keep every other audience. It returns the
// number of tokens the audience was removed from.
func (g *ResourceAudienceGuard) RetireResource(resourceID string) (int, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return 0, ErrBlankResource
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	resource, ok := g.resources[resourceID]
	if !ok {
		return 0, ErrUnknownResource
	}

	resource.Retired = true
	audience := resource.URI
	affected := 0
	for _, token := range g.tokens {
		kept := make([]string, 0, len(token.Audiences))
		removed := false
		for _, current := range token.Audiences {
			if current == audience {
				removed = true
				continue
			}
			kept = append(kept, current)
		}
		if removed {
			token.Audiences = kept
			affected++
		}
	}
	return affected, nil
}

// Tokens lists the live tokens of one client ordered by token id.
func (g *ResourceAudienceGuard) Tokens(clientID string) []Token {
	clientID = strings.TrimSpace(clientID)

	g.mu.Lock()
	defer g.mu.Unlock()

	tokens := make([]Token, 0)
	for _, token := range g.tokens {
		if token.ClientID != clientID {
			continue
		}
		tokens = append(tokens, *cloneToken(token))
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].TokenID < tokens[j].TokenID
	})
	return tokens
}

// snapshotDocument is the versioned, JSON encoded image of all guard state.
type snapshotDocument struct {
	Version   int               `json:"version"`
	Seq       int               `json:"seq"`
	Clients   map[string]string `json:"clients"`
	Resources []Resource        `json:"resources"`
	Tokens    []Token           `json:"tokens"`
}

// Snapshot serialises resources, clients and tokens.
func (g *ResourceAudienceGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	document := snapshotDocument{
		Version:   snapshotVersion,
		Seq:       g.seq,
		Clients:   make(map[string]string, len(g.clients)),
		Resources: make([]Resource, 0, len(g.resources)),
		Tokens:    make([]Token, 0, len(g.tokens)),
	}
	for clientID, tenantID := range g.clients {
		document.Clients[clientID] = tenantID
	}
	for _, resource := range g.resources {
		document.Resources = append(document.Resources, *cloneResource(resource))
	}
	sort.Slice(document.Resources, func(i, j int) bool {
		return document.Resources[i].ResourceID < document.Resources[j].ResourceID
	})
	for _, token := range g.tokens {
		document.Tokens = append(document.Tokens, *cloneToken(token))
	}
	sort.Slice(document.Tokens, func(i, j int) bool {
		return document.Tokens[i].TokenID < document.Tokens[j].TokenID
	})

	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return data, nil
}

// Restore replaces resources, clients and tokens with a snapshot. A malformed
// or unsupported snapshot reports ErrBadSnapshot and leaves the current state
// untouched.
func (g *ResourceAudienceGuard) Restore(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return ErrBadSnapshot
	}

	var document snapshotDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if document.Version != snapshotVersion || document.Seq < 0 {
		return ErrBadSnapshot
	}

	resources := make(map[string]*Resource, len(document.Resources))
	byURI := make(map[string]string, len(document.Resources))
	for _, recorded := range document.Resources {
		resourceID := strings.TrimSpace(recorded.ResourceID)
		tenantID := strings.TrimSpace(recorded.TenantID)
		if resourceID == "" || tenantID == "" {
			return ErrBadSnapshot
		}
		canonicalURI, err := g.NormalizeURI(recorded.URI)
		if err != nil {
			return ErrBadSnapshot
		}
		scopes := normalizeScopes(recorded.Scopes)
		if len(scopes) == 0 {
			return ErrBadSnapshot
		}
		if _, duplicate := resources[resourceID]; duplicate {
			return ErrBadSnapshot
		}
		if existingID, duplicate := byURI[canonicalURI]; duplicate && existingID != resourceID {
			return ErrBadSnapshot
		}

		resource := &Resource{
			ResourceID: resourceID,
			TenantID:   tenantID,
			URI:        canonicalURI,
			Scopes:     scopes,
			Retired:    recorded.Retired,
		}
		resources[resourceID] = resource
		byURI[canonicalURI] = resourceID
	}

	clients := make(map[string]string, len(document.Clients))
	for rawClientID, rawTenantID := range document.Clients {
		clientID := strings.TrimSpace(rawClientID)
		tenantID := strings.TrimSpace(rawTenantID)
		if clientID == "" || tenantID == "" {
			return ErrBadSnapshot
		}
		clients[clientID] = tenantID
	}

	tokens := make(map[string]*Token, len(document.Tokens))
	maxSequence := 0
	for _, recorded := range document.Tokens {
		tokenID := strings.TrimSpace(recorded.TokenID)
		clientID := strings.TrimSpace(recorded.ClientID)
		if tokenID == "" || clientID == "" {
			return ErrBadSnapshot
		}
		if _, duplicate := tokens[tokenID]; duplicate {
			return ErrBadSnapshot
		}
		tenantID := strings.TrimSpace(recorded.TenantID)
		if tenantID == "" {
			tenantID = clients[clientID]
		}
		if tenantID == "" {
			return ErrBadSnapshot
		}

		audiences := make([]string, 0, len(recorded.Audiences))
		seenAudience := make(map[string]struct{}, len(recorded.Audiences))
		for _, rawAudience := range recorded.Audiences {
			canonicalURI, err := g.NormalizeURI(rawAudience)
			if err != nil {
				return ErrBadSnapshot
			}
			if _, duplicate := seenAudience[canonicalURI]; duplicate {
				continue
			}
			seenAudience[canonicalURI] = struct{}{}
			audiences = append(audiences, canonicalURI)
		}
		sort.Strings(audiences)

		issuedAt := recorded.IssuedAt
		if issuedAt.IsZero() {
			issuedAt = time.Now().UTC()
		}

		token := &Token{
			TokenID:   tokenID,
			ClientID:  clientID,
			TenantID:  tenantID,
			Subject:   strings.TrimSpace(recorded.Subject),
			Audiences: audiences,
			Scopes:    normalizeScopes(recorded.Scopes),
			IssuedAt:  issuedAt,
		}
		tokens[tokenID] = token
		if sequence, ok := tokenSequence(tokenID); ok && sequence > maxSequence {
			maxSequence = sequence
		}
	}

	sequence := document.Seq
	if maxSequence > sequence {
		sequence = maxSequence
	}

	g.mu.Lock()
	g.resources = resources
	g.byURI = byURI
	g.clients = clients
	g.tokens = tokens
	g.seq = sequence
	g.mu.Unlock()
	return nil
}

func (g *ResourceAudienceGuard) countTenantResourcesLocked(tenantID string) int {
	count := 0
	for _, resource := range g.resources {
		if resource.TenantID == tenantID {
			count++
		}
	}
	return count
}

func normalizeScopes(scopes []string) []string {
	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, rawScope := range scopes {
		scope := strings.TrimSpace(rawScope)
		if scope == "" {
			continue
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	sort.Strings(normalized)
	return normalized
}

func parsePresentedToken(rawToken string) (string, error) {
	if !strings.HasPrefix(rawToken, tokenPrefix) {
		return "", ErrMalformedToken
	}
	tokenID := strings.TrimPrefix(rawToken, tokenPrefix)
	if strings.TrimSpace(tokenID) == "" {
		return "", ErrMalformedToken
	}
	return tokenID, nil
}

func tokenSequence(tokenID string) (int, bool) {
	if !strings.HasPrefix(tokenID, tokenIDPrefix) {
		return 0, false
	}
	sequence, err := strconv.Atoi(strings.TrimPrefix(tokenID, tokenIDPrefix))
	if err != nil || sequence <= 0 {
		return 0, false
	}
	return sequence, true
}

func cloneStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func cloneResource(resource *Resource) *Resource {
	copy := *resource
	copy.Scopes = cloneStrings(resource.Scopes)
	return &copy
}

func cloneToken(token *Token) *Token {
	copy := *token
	copy.Audiences = cloneStrings(token.Audiences)
	copy.Scopes = cloneStrings(token.Scopes)
	return &copy
}
