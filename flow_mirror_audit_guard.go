package task

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Sentinel errors reported by MirrorAuditGuard.
var (
	ErrPolicyConfig   = errors.New("mirror: invalid policy configuration")
	ErrPolicyExists   = errors.New("mirror: policy already exists")
	ErrPolicyUnknown  = errors.New("mirror: policy not found")
	ErrSessionExists  = errors.New("mirror: session already exists")
	ErrSessionUnknown = errors.New("mirror: session not found")
	ErrTargetUnknown  = errors.New("mirror: target not registered")
	ErrTargetConfig   = errors.New("mirror: invalid target configuration")
	ErrFlowInvalid    = errors.New("mirror: invalid flow key")
	ErrMirrorSnapshot = errors.New("mirror: corrupt snapshot")
)

// FlowKey is the five tuple plus tenant that selects a mirror policy.
type FlowKey struct {
	SrcIP   string
	DstIP   string
	SrcPort int
	DstPort int
	Proto   string
	Tenant  string
}

// MirrorPolicy describes deterministic sampling and header projection.
type MirrorPolicy struct {
	ID           string
	Tenant       string
	Priority     int
	SampleRate   int
	Granularity  int
	Fields       []string
	Family       string
	HighPriority bool
}

// MirrorSession points one mirror policy at one target.
type MirrorSession struct {
	ID       string
	PolicyID string
	Target   string
	Tenant   string
	Packets  int
	Mirrored int
}

// MirrorTarget is a collector with a packet rate quota.
type MirrorTarget struct {
	Name    string
	MaxPPS  int
	Used    int
	Healthy bool
}

// MirrorStats summarises guard activity.
type MirrorStats struct {
	Policies    int
	Sessions    int
	Matched     int
	Sampled     int
	Projected   int
	Delivered   int
	Dropped     int
	TargetsDown int
}

type mirrorCounters struct {
	Matched   int `json:"matched"`
	Sampled   int `json:"sampled"`
	Projected int `json:"projected"`
	Delivered int `json:"delivered"`
	Dropped   int `json:"dropped"`
}

const (
	snapshotFormat  = "gsb-mirror-audit-guard"
	snapshotVersion = 1
)

type mirrorSnapshot struct {
	Format   string          `json:"format"`
	Version  int             `json:"version"`
	Policies []MirrorPolicy  `json:"policies"`
	Targets  []MirrorTarget  `json:"targets"`
	Sessions []MirrorSession `json:"sessions"`
	Counters mirrorCounters  `json:"counters"`
}

// MirrorAuditGuard decides which traffic enters mirror sessions.
type MirrorAuditGuard struct {
	mu       sync.RWMutex
	policies map[string]MirrorPolicy
	targets  map[string]MirrorTarget
	sessions map[string]MirrorSession
	counters mirrorCounters
}

// NewMirrorAuditGuard builds an empty guard.
func NewMirrorAuditGuard() *MirrorAuditGuard {
	return &MirrorAuditGuard{
		policies: make(map[string]MirrorPolicy),
		targets:  make(map[string]MirrorTarget),
		sessions: make(map[string]MirrorSession),
	}
}

func validatePolicy(p MirrorPolicy) error {
	if strings.TrimSpace(p.ID) == "" || p.SampleRate < 1 || p.Priority < 0 {
		return ErrPolicyConfig
	}
	switch p.Family {
	case "ipv4":
		if p.Granularity < 0 || p.Granularity > 32 {
			return ErrPolicyConfig
		}
	case "ipv6":
		if p.Granularity < 0 || p.Granularity > 128 {
			return ErrPolicyConfig
		}
	default:
		return ErrPolicyConfig
	}
	return nil
}

func clonePolicy(p MirrorPolicy) MirrorPolicy {
	if p.Fields != nil {
		p.Fields = append([]string(nil), p.Fields...)
	}
	return p
}

// AddPolicy registers a mirror policy.
func (g *MirrorAuditGuard) AddPolicy(p MirrorPolicy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	p = clonePolicy(p)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.policies[p.ID]; ok {
		return ErrPolicyExists
	}
	g.policies[p.ID] = p
	return nil
}

// DeletePolicy removes a mirror policy by id.
func (g *MirrorAuditGuard) DeletePolicy(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.policies[id]; !ok {
		return ErrPolicyUnknown
	}
	delete(g.policies, id)
	return nil
}

// AddSession binds a policy to a mirror target.
func (g *MirrorAuditGuard) AddSession(s MirrorSession) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.sessions[s.ID]; ok {
		return ErrSessionExists
	}
	if _, ok := g.policies[s.PolicyID]; !ok {
		return ErrPolicyUnknown
	}
	if _, ok := g.targets[s.Target]; !ok {
		return ErrTargetUnknown
	}
	g.sessions[s.ID] = s
	return nil
}

func ipFamily(ip net.IP) string {
	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}

func validFlow(f FlowKey) (string, bool) {
	if strings.TrimSpace(f.Tenant) == "" || strings.TrimSpace(f.Proto) == "" {
		return "", false
	}
	if f.SrcPort <= 0 || f.SrcPort > 65535 || f.DstPort <= 0 || f.DstPort > 65535 {
		return "", false
	}
	src := net.ParseIP(f.SrcIP)
	dst := net.ParseIP(f.DstIP)
	if src == nil || dst == nil {
		return "", false
	}
	srcFamily := ipFamily(src)
	if srcFamily != ipFamily(dst) {
		return "", false
	}
	return srcFamily, true
}

// Match selects the policy for a flow.
func (g *MirrorAuditGuard) Match(f FlowKey) (MirrorPolicy, bool) {
	family, ok := validFlow(f)
	if !ok {
		return MirrorPolicy{}, false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	var best MirrorPolicy
	found := false
	for _, p := range g.policies {
		if p.Tenant != f.Tenant || p.Family != family {
			continue
		}
		if !found || p.Priority > best.Priority || (p.Priority == best.Priority && p.ID < best.ID) {
			best = p
			found = true
		}
	}
	if !found {
		return MirrorPolicy{}, false
	}
	g.counters.Matched++
	return clonePolicy(best), true
}

func flowHash(f FlowKey) uint64 {
	h := fnv.New64a()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	canonicalIP := func(s string) string {
		if ip := net.ParseIP(s); ip != nil {
			return ip.String()
		}
		return s
	}
	write(canonicalIP(f.SrcIP))
	write(canonicalIP(f.DstIP))
	write(strconv.Itoa(f.SrcPort))
	write(strconv.Itoa(f.DstPort))
	write(strings.ToLower(f.Proto))
	return h.Sum64()
}

// Sample reports whether one packet of a flow is sampled deterministically.
func (g *MirrorAuditGuard) Sample(f FlowKey, p MirrorPolicy, seq int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if p.SampleRate < 1 {
		return false
	}
	hit := true
	if p.SampleRate > 1 {
		rate := int64(p.SampleRate)
		offset := int64(flowHash(f) % uint64(p.SampleRate))
		mod := seq % rate
		if mod < 0 {
			mod += rate
		}
		hit = mod == offset
	}
	if hit {
		g.counters.Sampled++
	}
	return hit
}

func maskIP(value string, bits int) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return value
	}
	if v4 := ip.To4(); v4 != nil {
		if bits < 0 || bits > 32 {
			return value
		}
		mask := net.CIDRMask(bits, 32)
		out := make(net.IP, net.IPv4len)
		for i := 0; i < net.IPv4len; i++ {
			out[i] = v4[i] & mask[i]
		}
		return out.String()
	}
	if bits < 0 || bits > 128 {
		return value
	}
	mask := net.CIDRMask(bits, 128)
	out := make(net.IP, net.IPv6len)
	for i := 0; i < net.IPv6len; i++ {
		out[i] = ip[i] & mask[i]
	}
	return out.String()
}

// Project keeps only whitelisted header fields and masks IP fields.
func (g *MirrorAuditGuard) Project(f FlowKey, p MirrorPolicy, header map[string]string) map[string]string {
	g.mu.Lock()
	g.counters.Projected++
	g.mu.Unlock()

	projected := make(map[string]string, len(p.Fields))
	for _, field := range p.Fields {
		value, ok := header[field]
		if !ok {
			continue
		}
		switch strings.ToLower(field) {
		case "src_ip", "dst_ip":
			value = maskIP(value, p.Granularity)
		}
		projected[field] = value
	}
	return projected
}

// SetTarget registers or updates a mirror target.
func (g *MirrorAuditGuard) SetTarget(t MirrorTarget) error {
	if strings.TrimSpace(t.Name) == "" || t.MaxPPS <= 0 {
		return ErrTargetConfig
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	used := t.Used
	if used < 0 {
		used = 0
	}
	if old, ok := g.targets[t.Name]; ok && old.Used > used {
		used = old.Used
	}
	if used > t.MaxPPS {
		used = t.MaxPPS
	}
	t.Used = used
	g.targets[t.Name] = t
	return nil
}

// Distribute delivers packets for a session within the target quota.
func (g *MirrorAuditGuard) Distribute(sessionID string, packets int) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	session, ok := g.sessions[sessionID]
	if !ok {
		return 0, ErrSessionUnknown
	}
	policy, ok := g.policies[session.PolicyID]
	if !ok {
		return 0, ErrPolicyUnknown
	}
	target, ok := g.targets[session.Target]
	if !ok {
		return 0, ErrTargetUnknown
	}
	if packets <= 0 {
		return 0, nil
	}

	session.Packets += packets
	delivered := 0
	if target.Healthy || policy.HighPriority {
		available := target.MaxPPS - target.Used
		if available > 0 {
			delivered = packets
			if delivered > available {
				delivered = available
			}
			target.Used += delivered
			session.Mirrored += delivered
		}
	}

	dropped := packets - delivered
	g.sessions[sessionID] = session
	g.targets[target.Name] = target
	g.counters.Delivered += delivered
	g.counters.Dropped += dropped
	return delivered, nil
}

// SessionStats returns a session by id.
func (g *MirrorAuditGuard) SessionStats(id string) (MirrorSession, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	session, ok := g.sessions[id]
	return session, ok
}

// TargetUsed returns the used quota of a target.
func (g *MirrorAuditGuard) TargetUsed(name string) (int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	target, ok := g.targets[name]
	return target.Used, ok
}

// Snapshot serialises policies, targets and sessions.
func (g *MirrorAuditGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	policies := make([]MirrorPolicy, 0, len(g.policies))
	for _, p := range g.policies {
		policies = append(policies, clonePolicy(p))
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].ID < policies[j].ID })

	targets := make([]MirrorTarget, 0, len(g.targets))
	for _, t := range g.targets {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	sessions := make([]MirrorSession, 0, len(g.sessions))
	for _, s := range g.sessions {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })

	snapshot := mirrorSnapshot{
		Format:   snapshotFormat,
		Version:  snapshotVersion,
		Policies: policies,
		Targets:  targets,
		Sessions: sessions,
		Counters: g.counters,
	}
	return json.Marshal(snapshot)
}

// Restore rebuilds the guard from a snapshot.
func (g *MirrorAuditGuard) Restore(data []byte) error {
	var snapshot mirrorSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrMirrorSnapshot
	}
	if snapshot.Format != snapshotFormat || snapshot.Version != snapshotVersion {
		return ErrMirrorSnapshot
	}
	if snapshot.Counters.Matched < 0 || snapshot.Counters.Sampled < 0 ||
		snapshot.Counters.Projected < 0 || snapshot.Counters.Delivered < 0 ||
		snapshot.Counters.Dropped < 0 {
		return ErrMirrorSnapshot
	}

	policies := make(map[string]MirrorPolicy, len(snapshot.Policies))
	for _, p := range snapshot.Policies {
		if err := validatePolicy(p); err != nil {
			return ErrMirrorSnapshot
		}
		if _, exists := policies[p.ID]; exists {
			return ErrMirrorSnapshot
		}
		policies[p.ID] = clonePolicy(p)
	}

	targets := make(map[string]MirrorTarget, len(snapshot.Targets))
	for _, t := range snapshot.Targets {
		if strings.TrimSpace(t.Name) == "" || t.MaxPPS <= 0 || t.Used < 0 || t.Used > t.MaxPPS {
			return ErrMirrorSnapshot
		}
		if _, exists := targets[t.Name]; exists {
			return ErrMirrorSnapshot
		}
		targets[t.Name] = t
	}

	sessions := make(map[string]MirrorSession, len(snapshot.Sessions))
	for _, s := range snapshot.Sessions {
		if s.Packets < 0 || s.Mirrored < 0 {
			return ErrMirrorSnapshot
		}
		if _, exists := sessions[s.ID]; exists {
			return ErrMirrorSnapshot
		}
		if _, exists := policies[s.PolicyID]; !exists {
			return ErrMirrorSnapshot
		}
		if _, exists := targets[s.Target]; !exists {
			return ErrMirrorSnapshot
		}
		sessions[s.ID] = s
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.policies = policies
	g.targets = targets
	g.sessions = sessions
	g.counters = snapshot.Counters
	return nil
}

// Stats returns the activity counters.
func (g *MirrorAuditGuard) Stats() MirrorStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := MirrorStats{
		Policies:  len(g.policies),
		Sessions:  len(g.sessions),
		Matched:   g.counters.Matched,
		Sampled:   g.counters.Sampled,
		Projected: g.counters.Projected,
		Delivered: g.counters.Delivered,
		Dropped:   g.counters.Dropped,
	}
	for _, target := range g.targets {
		if !target.Healthy {
			stats.TargetsDown++
		}
	}
	return stats
}
