package task

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by PairwiseSubjectGuard.
var (
	ErrInvalidTenant      = errors.New("invalid tenant id")
	ErrInvalidSector      = errors.New("invalid sector")
	ErrDuplicateSectorURI = errors.New("sector uri already registered")
	ErrSectorLimit        = errors.New("tenant sector limit reached")
	ErrUnknownSector      = errors.New("unknown sector")
	ErrUnknownClient      = errors.New("unknown client")
	ErrInvalidClient      = errors.New("invalid client id")
	ErrAlreadyAssigned    = errors.New("client already assigned to another sector")
	ErrClientLimit        = errors.New("sector client limit reached")
	ErrInvalidUser        = errors.New("invalid local user id")
	ErrUnknownSubject     = errors.New("unknown subject")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// Sector is a registered pairwise sector of one tenant.
type Sector struct {
	ID  string `json:"id"`
	URI string `json:"uri"`
}

// SubjectRemap records one re-derived pairwise subject of a moved client.
type SubjectRemap struct {
	LocalUserID string `json:"local_user_id"`
	OldSubject  string `json:"old_subject"`
	NewSubject  string `json:"new_subject"`
}

// Remap is the outcome of moving one client to another sector.
type Remap struct {
	TenantID   string         `json:"tenant_id"`
	ClientID   string         `json:"client_id"`
	FromSector string         `json:"from_sector"`
	ToSector   string         `json:"to_sector"`
	Items      []SubjectRemap `json:"items"`
}

// Options bounds the guard.
type Options struct {
	MaxSectorsPerTenant int `json:"max_sectors_per_tenant"`
	MaxClientsPerSector int `json:"max_clients_per_sector"`
}

type subjectRecord struct {
	LocalUserID string `json:"local_user_id"`
	SectorID    string `json:"sector_id"`
}

type sectorState struct {
	ID    string            `json:"id"`
	URI   string            `json:"uri"`
	Epoch int               `json:"epoch"`
	Users map[string]string `json:"users"`
}

type tenantState struct {
	Sectors     map[string]*sectorState   `json:"sectors"`
	SectorByURI map[string]string         `json:"-"`
	Clients     map[string]string         `json:"clients"`
	Subjects    map[string]*subjectRecord `json:"subjects"`
	Tombstones  map[string]string         `json:"tombstones"`
}

type snapshotState struct {
	Version             int                     `json:"version"`
	MaxSectorsPerTenant int                     `json:"max_sectors_per_tenant"`
	MaxClientsPerSector int                     `json:"max_clients_per_sector"`
	Tenants             map[string]*tenantState `json:"tenants"`
}

const snapshotVersion = 1

// PairwiseSubjectGuard issues and tracks pairwise subject identifiers.
type PairwiseSubjectGuard struct {
	mu         sync.RWMutex
	maxSectors int
	maxClients int
	tenants    map[string]*tenantState
}

// NewPairwiseSubjectGuard builds an empty guard.
func NewPairwiseSubjectGuard(opts Options) *PairwiseSubjectGuard {
	maxSectors := opts.MaxSectorsPerTenant
	if maxSectors < 0 {
		maxSectors = 0
	}
	maxClients := opts.MaxClientsPerSector
	if maxClients < 0 {
		maxClients = 0
	}
	return &PairwiseSubjectGuard{
		maxSectors: maxSectors,
		maxClients: maxClients,
		tenants:    make(map[string]*tenantState),
	}
}

func newTenantState() *tenantState {
	return &tenantState{
		Sectors:     make(map[string]*sectorState),
		SectorByURI: make(map[string]string),
		Clients:     make(map[string]string),
		Subjects:    make(map[string]*subjectRecord),
		Tombstones:  make(map[string]string),
	}
}

// RegisterSector registers the sector identifier URI of one tenant.
func (g *PairwiseSubjectGuard) RegisterSector(tenantID, sectorID, uri string) error {
	if tenantID == "" {
		return ErrInvalidTenant
	}
	if sectorID == "" {
		return ErrInvalidSector
	}
	normalized, err := normalizeSectorURI(uri)
	if err != nil {
		return ErrInvalidSector
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if t != nil {
		if _, exists := t.Sectors[sectorID]; exists {
			return ErrInvalidSector
		}
		if _, exists := t.SectorByURI[normalized]; exists {
			return ErrDuplicateSectorURI
		}
	}
	if g.maxSectors > 0 && t != nil && len(t.Sectors) >= g.maxSectors {
		return ErrSectorLimit
	}
	if t == nil {
		t = newTenantState()
		g.tenants[tenantID] = t
	}

	t.Sectors[sectorID] = &sectorState{
		ID:    sectorID,
		URI:   normalized,
		Epoch: 0,
		Users: make(map[string]string),
	}
	t.SectorByURI[normalized] = sectorID
	return nil
}

// AssignClient binds a client to a sector of the tenant.
func (g *PairwiseSubjectGuard) AssignClient(tenantID, clientID, sectorID string) (Sector, error) {
	if tenantID == "" {
		return Sector{}, ErrInvalidTenant
	}
	if clientID == "" {
		return Sector{}, ErrInvalidClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if sectorID == "" {
		virtualID, virtualURI := independentSectorIdentity(tenantID, clientID)
		if t == nil {
			t = newTenantState()
			g.tenants[tenantID] = t
		}
		if current, exists := t.Clients[clientID]; exists {
			if current == virtualID {
				sec := t.Sectors[current]
				return Sector{ID: sec.ID, URI: sec.URI}, nil
			}
			return Sector{}, ErrAlreadyAssigned
		}
		sec := t.Sectors[virtualID]
		if sec == nil {
			if g.maxSectors > 0 && len(t.Sectors) >= g.maxSectors {
				return Sector{}, ErrSectorLimit
			}
			sec = &sectorState{
				ID:    virtualID,
				URI:   virtualURI,
				Epoch: 0,
				Users: make(map[string]string),
			}
			t.Sectors[virtualID] = sec
			t.SectorByURI[virtualURI] = virtualID
		}
		t.Clients[clientID] = virtualID
		return Sector{ID: sec.ID, URI: sec.URI}, nil
	}

	if t == nil {
		return Sector{}, ErrUnknownSector
	}
	sec := t.Sectors[sectorID]
	if sec == nil {
		return Sector{}, ErrUnknownSector
	}

	if current, exists := t.Clients[clientID]; exists {
		if current == sectorID {
			return Sector{ID: sec.ID, URI: sec.URI}, nil
		}
		return Sector{}, ErrAlreadyAssigned
	}
	if g.maxClients > 0 && clientCount(t, sectorID) >= g.maxClients {
		return Sector{}, ErrClientLimit
	}

	t.Clients[clientID] = sectorID
	return Sector{ID: sec.ID, URI: sec.URI}, nil
}

// SubjectFor returns the pairwise subject of one local user under a client.
func (g *PairwiseSubjectGuard) SubjectFor(tenantID, clientID, localUserID string) (string, error) {
	if tenantID == "" {
		return "", ErrInvalidTenant
	}
	if localUserID == "" {
		return "", ErrInvalidUser
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if t == nil {
		return "", ErrUnknownClient
	}
	sectorID, exists := t.Clients[clientID]
	if !exists {
		return "", ErrUnknownClient
	}
	sec := t.Sectors[sectorID]
	if sec == nil {
		return "", ErrUnknownClient
	}

	if subject, exists := sec.Users[localUserID]; exists {
		if _, retired := t.Tombstones[subject]; !retired {
			return subject, nil
		}
		delete(sec.Users, localUserID)
	}

	subject := g.deriveSubject(tenantID, t, sec, localUserID)
	sec.Users[localUserID] = subject
	return subject, nil
}

// MoveClient moves one client to another registered sector of the same tenant.
func (g *PairwiseSubjectGuard) MoveClient(tenantID, clientID, newSector string) (Remap, error) {
	if tenantID == "" {
		return Remap{}, ErrInvalidTenant
	}
	if clientID == "" {
		return Remap{}, ErrInvalidClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if t == nil {
		return Remap{}, ErrUnknownSector
	}
	target := t.Sectors[newSector]
	if target == nil {
		return Remap{}, ErrUnknownSector
	}
	sourceID, exists := t.Clients[clientID]
	if !exists {
		return Remap{}, ErrUnknownClient
	}

	out := Remap{
		TenantID:   tenantID,
		ClientID:   clientID,
		FromSector: sourceID,
		ToSector:   newSector,
	}
	if sourceID == newSector {
		return out, nil
	}
	if g.maxClients > 0 && clientCount(t, newSector) >= g.maxClients {
		return Remap{}, ErrClientLimit
	}
	source := t.Sectors[sourceID]
	if source == nil {
		return Remap{}, ErrUnknownClient
	}

	users := make([]string, 0, len(source.Users))
	for user := range source.Users {
		users = append(users, user)
	}
	sort.Strings(users)

	for _, user := range users {
		oldSubject := source.Users[user]
		if _, alreadyRetired := t.Tombstones[oldSubject]; alreadyRetired {
			delete(source.Users, user)
			continue
		}

		newSubject := ""
		if existing, ok := target.Users[user]; ok {
			if _, retired := t.Tombstones[existing]; !retired {
				newSubject = existing
			}
		}
		if newSubject == "" {
			newSubject = g.deriveSubject(tenantID, t, target, user)
		}
		target.Users[user] = newSubject
		delete(source.Users, user)
		t.Tombstones[oldSubject] = newSubject
		out.Items = append(out.Items, SubjectRemap{
			LocalUserID: user,
			OldSubject:  oldSubject,
			NewSubject:  newSubject,
		})
	}

	t.Clients[clientID] = newSector
	return out, nil
}

// Tombstone retires one subject and makes it unusable for new sessions.
func (g *PairwiseSubjectGuard) Tombstone(tenantID, subject string) error {
	if tenantID == "" {
		return ErrInvalidTenant
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if t == nil {
		return ErrUnknownSubject
	}
	if _, retired := t.Tombstones[subject]; retired {
		return nil
	}
	if _, known := t.Subjects[subject]; !known {
		return ErrUnknownSubject
	}
	t.Tombstones[subject] = ""
	return nil
}

// Resolve maps a retired subject to the subject that replaced it.
func (g *PairwiseSubjectGuard) Resolve(tenantID, subject string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	t := g.tenants[tenantID]
	if t == nil {
		return "", false
	}

	seen := make(map[string]struct{})
	current := subject
	for {
		if _, loop := seen[current]; loop {
			return "", false
		}
		seen[current] = struct{}{}

		replacement, retired := t.Tombstones[current]
		if !retired || replacement == "" {
			return "", false
		}
		if _, alsoRetired := t.Tombstones[replacement]; !alsoRetired {
			return replacement, true
		}
		current = replacement
	}
}

// RotateSectorKey rotates the derivation key of one sector.
func (g *PairwiseSubjectGuard) RotateSectorKey(tenantID, sectorID string) error {
	if tenantID == "" {
		return ErrInvalidTenant
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	t := g.tenants[tenantID]
	if t == nil {
		return ErrUnknownSector
	}
	sec := t.Sectors[sectorID]
	if sec == nil {
		return ErrUnknownSector
	}
	sec.Epoch++
	return nil
}

// KeyEpoch reports how many times the sector key was rotated.
func (g *PairwiseSubjectGuard) KeyEpoch(tenantID, sectorID string) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	t := g.tenants[tenantID]
	if t == nil {
		return 0, ErrUnknownSector
	}
	sec := t.Sectors[sectorID]
	if sec == nil {
		return 0, ErrUnknownSector
	}
	return sec.Epoch, nil
}

// SectorCount reports the registered sectors of a tenant.
func (g *PairwiseSubjectGuard) SectorCount(tenantID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	t := g.tenants[tenantID]
	if t == nil {
		return 0
	}
	return len(t.Sectors)
}

// ClientCount reports the clients attached to one sector of a tenant.
func (g *PairwiseSubjectGuard) ClientCount(tenantID, sectorID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	t := g.tenants[tenantID]
	if t == nil {
		return 0
	}
	return clientCount(t, sectorID)
}

// TombstoneCount reports how many subjects were retired for a tenant.
func (g *PairwiseSubjectGuard) TombstoneCount(tenantID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	t := g.tenants[tenantID]
	if t == nil {
		return 0
	}
	return len(t.Tombstones)
}

// Snapshot serialises the whole guard state.
func (g *PairwiseSubjectGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := snapshotState{
		Version:             snapshotVersion,
		MaxSectorsPerTenant: g.maxSectors,
		MaxClientsPerSector: g.maxClients,
		Tenants:             g.tenants,
	}
	return json.Marshal(state)
}

// Restore rebuilds the guard state from a snapshot.
func (g *PairwiseSubjectGuard) Restore(data []byte) error {
	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if state.MaxSectorsPerTenant < 0 || state.MaxClientsPerSector < 0 {
		return ErrBadSnapshot
	}
	if state.Tenants == nil {
		state.Tenants = make(map[string]*tenantState)
	}

	for tenantID, t := range state.Tenants {
		if tenantID == "" || t == nil {
			return ErrBadSnapshot
		}
		if t.Sectors == nil {
			t.Sectors = make(map[string]*sectorState)
		}
		if t.Clients == nil {
			t.Clients = make(map[string]string)
		}
		if t.Subjects == nil {
			t.Subjects = make(map[string]*subjectRecord)
		}
		if t.Tombstones == nil {
			t.Tombstones = make(map[string]string)
		}

		t.SectorByURI = make(map[string]string, len(t.Sectors))
		for sectorID, sec := range t.Sectors {
			if sectorID == "" || sec == nil || sec.ID != sectorID || sec.URI == "" || sec.Epoch < 0 {
				return ErrBadSnapshot
			}
			normalized, err := normalizeSectorURI(sec.URI)
			if err != nil || normalized != sec.URI {
				return ErrBadSnapshot
			}
			if _, duplicate := t.SectorByURI[sec.URI]; duplicate {
				return ErrBadSnapshot
			}
			t.SectorByURI[sec.URI] = sectorID
			if sec.Users == nil {
				sec.Users = make(map[string]string)
			}
			for user, subject := range sec.Users {
				if user == "" || subject == "" {
					return ErrBadSnapshot
				}
			}
		}

		for clientID, sectorID := range t.Clients {
			if clientID == "" {
				return ErrBadSnapshot
			}
			if _, exists := t.Sectors[sectorID]; !exists {
				return ErrBadSnapshot
			}
		}
		for subject, record := range t.Subjects {
			if subject == "" || record == nil || record.LocalUserID == "" || record.SectorID == "" {
				return ErrBadSnapshot
			}
			if _, exists := t.Sectors[record.SectorID]; !exists {
				return ErrBadSnapshot
			}
		}
		for subject := range t.Tombstones {
			if subject == "" {
				return ErrBadSnapshot
			}
		}
	}

	g.mu.Lock()
	g.maxSectors = state.MaxSectorsPerTenant
	g.maxClients = state.MaxClientsPerSector
	g.tenants = state.Tenants
	g.mu.Unlock()
	return nil
}

func clientCount(t *tenantState, sectorID string) int {
	if t == nil {
		return 0
	}
	count := 0
	for _, assigned := range t.Clients {
		if assigned == sectorID {
			count++
		}
	}
	return count
}

func (g *PairwiseSubjectGuard) deriveSubject(tenantID string, t *tenantState, sec *sectorState, localUserID string) string {
	key := deriveSectorKey(tenantID, sec)
	for attempt := uint64(0); ; attempt++ {
		subject := derivePairwiseSubject(key, tenantID, sec.ID, localUserID, attempt)
		if record, exists := t.Subjects[subject]; exists {
			if _, retired := t.Tombstones[subject]; retired {
				continue
			}
			if record.LocalUserID == localUserID && record.SectorID == sec.ID {
				return subject
			}
			continue
		}
		if _, retired := t.Tombstones[subject]; retired {
			continue
		}
		t.Subjects[subject] = &subjectRecord{
			LocalUserID: localUserID,
			SectorID:    sec.ID,
		}
		return subject
	}
}

func deriveSectorKey(tenantID string, sec *sectorState) []byte {
	mac := hmac.New(sha256.New, []byte("gsb/pairwise-subject-guard/sector-key/v1"))
	writeField(mac, tenantID)
	writeField(mac, sec.ID)
	writeField(mac, sec.URI)
	writeUint64(mac, uint64(sec.Epoch))
	return mac.Sum(nil)
}

func derivePairwiseSubject(key []byte, tenantID, sectorID, localUserID string, attempt uint64) string {
	mac := hmac.New(sha256.New, key)
	writeField(mac, "pairwise-subject")
	writeField(mac, tenantID)
	writeField(mac, sectorID)
	writeField(mac, localUserID)
	writeUint64(mac, attempt)
	return "ps_" + hex.EncodeToString(mac.Sum(nil))
}

func writeField(w hash.Hash, value string) {
	writeUint64(w, uint64(len(value)))
	_, _ = w.Write([]byte(value))
}

func writeUint64(w hash.Hash, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = w.Write(buf[:])
}

func independentSectorIdentity(tenantID, clientID string) (string, string) {
	sum := sha256.Sum256([]byte("independent-sector\x00" + tenantID + "\x00" + clientID))
	digest := hex.EncodeToString(sum[:])
	return "@client:" + digest[:32], "https://" + digest + ".pairwise.invalid"
}

func normalizeSectorURI(raw string) (string, error) {
	if raw == "" {
		return "", ErrInvalidSector
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalidSector
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.Opaque != "" || u.User != nil {
		return "", ErrInvalidSector
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", ErrInvalidSector
	}
	port := u.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", ErrInvalidSector
		}
		if number != 443 {
			host = net.JoinHostPort(host, strconv.Itoa(number))
		}
	}
	if port == "" && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	path := u.EscapedPath()
	path = strings.TrimRight(path, "/")

	var builder strings.Builder
	builder.WriteString("https://")
	builder.WriteString(host)
	builder.WriteString(path)
	if u.RawQuery != "" {
		builder.WriteByte('?')
		builder.WriteString(u.RawQuery)
	}
	if u.Fragment != "" {
		builder.WriteByte('#')
		builder.WriteString(u.Fragment)
	}
	return builder.String(), nil
}
