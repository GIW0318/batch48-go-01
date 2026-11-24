package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors reported by the IOC normalizer.
var (
	ErrInvalidIndicator = errors.New("ioc_feed_normalizer: invalid indicator")
	ErrTypeConflict     = errors.New("ioc_feed_normalizer: indicator type conflict")
	ErrInvalidFeed      = errors.New("ioc_feed_normalizer: invalid feed")
	ErrFeedTooLarge     = errors.New("ioc_feed_normalizer: feed over indicator limit")
	ErrSetLimit         = errors.New("ioc_feed_normalizer: merged set over limit")
	ErrUnknownSource    = errors.New("ioc_feed_normalizer: unknown source")
	ErrBadSnapshot      = errors.New("ioc_feed_normalizer: bad snapshot")
	ErrInvalidLimit     = errors.New("ioc_feed_normalizer: invalid limit")
)

// Indicator is one raw threat indicator as reported by a feed.
type Indicator struct {
	Value  string
	Kind   string
	TTL    int64
	Source string
}

// Feed is one source batch of indicators.
type Feed struct {
	Name       string
	DefaultTTL int64
	Indicators []Indicator
}

// Source is one feed that contributed to a merged entry.
type Source struct {
	Name string
	TTL  int64
}

// IocEntry is one normalised indicator with every contributing source.
type IocEntry struct {
	Value   string
	Kind    string
	TTL     int64
	Sources []Source
}

// IocSet is the merged, ordered result of a normalisation round.
type IocSet struct {
	Entries []IocEntry
}

// Len reports how many entries the set holds.
func (s *IocSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.Entries)
}

// Values returns the normalised indicator values in stable order.
func (s *IocSet) Values() []string {
	if s == nil || len(s.Entries) == 0 {
		return []string{}
	}
	values := make([]string, len(s.Entries))
	for i := range s.Entries {
		values[i] = s.Entries[i].Value
	}
	return values
}

// NormalizerOptions bounds one feed and the merged set.
type NormalizerOptions struct {
	MaxEntries int
	MaxPerFeed int
}

// NormalizerStats is the accumulated counter view of the normalizer.
type NormalizerStats struct {
	Feeds    int
	Ingested int
	Rejected int
	Entries  int
}

type feedState struct {
	name       string
	defaultTTL int64
	indicators []Indicator
}

type ipLookupEntry struct {
	prefix  netip.Prefix
	sources []Source
}

type wildcardLookupEntry struct {
	suffix  string
	sources []Source
}

type compiledSet struct {
	entries   []IocEntry
	domains   map[string][]Source
	urls      map[string][]Source
	ips       []ipLookupEntry
	wildcards []wildcardLookupEntry
}

// IocNormalizer folds multi-source intelligence into one matchable set.
type IocNormalizer struct {
	opts NormalizerOptions

	mu       sync.RWMutex
	feeds    map[string]feedState
	set      *compiledSet
	rejected int64
}

// NewIocNormalizer builds a normalizer with the given limits.
func NewIocNormalizer(opts NormalizerOptions) *IocNormalizer {
	return &IocNormalizer{
		opts:  opts,
		feeds: make(map[string]feedState),
		set:   emptyCompiledSet(),
	}
}

// Ingest validates and stores one whole feed.
func (n *IocNormalizer) Ingest(f Feed) error {
	name := strings.TrimSpace(f.Name)
	if name == "" {
		n.reject(len(f.Indicators))
		return ErrInvalidFeed
	}
	if n.opts.MaxPerFeed < 0 {
		n.reject(len(f.Indicators))
		return ErrInvalidLimit
	}
	if n.opts.MaxPerFeed > 0 && len(f.Indicators) > n.opts.MaxPerFeed {
		n.reject(len(f.Indicators))
		return ErrFeedTooLarge
	}

	normalized := make([]Indicator, 0, len(f.Indicators))
	seen := make(map[string]int, len(f.Indicators))
	for _, raw := range f.Indicators {
		ind := raw
		if ind.TTL == 0 {
			ind.TTL = f.DefaultTTL
		}
		ind.Source = name
		canonical, err := n.Normalize(ind)
		if err != nil {
			n.reject(len(f.Indicators))
			return err
		}

		key := canonical.Kind + "\x00" + canonical.Value
		if idx, ok := seen[key]; ok {
			if canonical.TTL < normalized[idx].TTL {
				normalized[idx].TTL = canonical.TTL
			}
			continue
		}
		seen[key] = len(normalized)
		normalized = append(normalized, canonical)
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.feeds == nil {
		n.feeds = make(map[string]feedState)
	}
	n.feeds[name] = feedState{
		name:       name,
		defaultTTL: f.DefaultTTL,
		indicators: normalized,
	}
	return nil
}

// Normalize canonicalises one indicator and infers its kind.
func (n *IocNormalizer) Normalize(ind Indicator) (Indicator, error) {
	value := strings.TrimSpace(ind.Value)
	if value == "" || ind.TTL < 0 {
		return Indicator{}, ErrInvalidIndicator
	}

	inferred := inferKind(value)
	kind := strings.ToLower(strings.TrimSpace(ind.Kind))
	if kind == "" {
		kind = inferred
	} else if kind != inferred {
		return Indicator{}, ErrTypeConflict
	}

	switch kind {
	case "ip":
		canonical, err := canonicalIP(value)
		if err != nil {
			return Indicator{}, ErrInvalidIndicator
		}
		value = canonical
	case "domain":
		canonical, err := canonicalDomain(value)
		if err != nil {
			return Indicator{}, ErrInvalidIndicator
		}
		value = canonical
	case "url":
		canonical, err := canonicalURL(value)
		if err != nil {
			return Indicator{}, ErrInvalidIndicator
		}
		value = canonical
	default:
		return Indicator{}, ErrTypeConflict
	}

	ind.Value = value
	ind.Kind = kind
	return ind, nil
}

// Merge folds every ingested feed into one ordered set.
func (n *IocNormalizer) Merge() (*IocSet, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.opts.MaxEntries < 0 {
		return nil, ErrInvalidLimit
	}
	entries, err := mergeFeeds(n.feeds)
	if err != nil {
		return nil, err
	}
	if n.opts.MaxEntries > 0 && len(entries) > n.opts.MaxEntries {
		return nil, ErrSetLimit
	}
	compiled, err := compileEntries(entries)
	if err != nil {
		return nil, err
	}
	n.set = compiled
	return compiled.asSet(), nil
}

// Lookup returns the sources of a value, matching IPs by prefix containment.
func (n *IocNormalizer) Lookup(v string) ([]Source, bool) {
	value := strings.TrimSpace(v)
	if value == "" {
		return nil, false
	}
	kind := inferKind(value)

	switch kind {
	case "ip":
		canonical, err := canonicalIP(value)
		if err != nil {
			return nil, false
		}
		n.mu.RLock()
		defer n.mu.RUnlock()
		return n.lookupIP(canonical)
	case "url":
		canonical, err := canonicalURL(value)
		if err != nil {
			return nil, false
		}
		n.mu.RLock()
		defer n.mu.RUnlock()
		if n.set == nil {
			return nil, false
		}
		sources, ok := n.set.urls[canonical]
		if !ok {
			return nil, false
		}
		return cloneSources(sources), true
	default:
		canonical, err := canonicalDomain(value)
		if err != nil {
			return nil, false
		}
		n.mu.RLock()
		defer n.mu.RUnlock()
		if n.set == nil {
			return nil, false
		}
		if sources, ok := n.set.domains[canonical]; ok {
			return cloneSources(sources), true
		}
		for _, wildcard := range n.set.wildcards {
			if strings.HasSuffix(canonical, "."+wildcard.suffix) {
				return cloneSources(wildcard.sources), true
			}
		}
		return nil, false
	}
}

// Entries returns the entries of the last successful merge.
func (n *IocNormalizer) Entries() []IocEntry {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.set == nil {
		return []IocEntry{}
	}
	return cloneEntries(n.set.entries)
}

// DropSource removes every indicator contributed by a feed.
func (n *IocNormalizer) DropSource(name string) error {
	name = strings.TrimSpace(name)
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.feeds[name]; !ok {
		return ErrUnknownSource
	}
	delete(n.feeds, name)
	return nil
}

// Stats returns a consistent view of the counters.
func (n *IocNormalizer) Stats() NormalizerStats {
	n.mu.RLock()
	defer n.mu.RUnlock()

	ingested := 0
	for _, feed := range n.feeds {
		ingested += len(feed.indicators)
	}
	entries := 0
	if n.set != nil {
		entries = len(n.set.entries)
	}
	return NormalizerStats{
		Feeds:    len(n.feeds),
		Ingested: ingested,
		Rejected: int(n.rejected),
		Entries:  entries,
	}
}

const (
	snapshotMagic   = "gsb-ioc-normalizer"
	snapshotVersion = 1
)

type snapshotFeed struct {
	Name       string      `json:"name"`
	DefaultTTL int64       `json:"default_ttl"`
	Indicators []Indicator `json:"indicators"`
}

type snapshotPayload struct {
	Feeds    []snapshotFeed `json:"feeds"`
	Set      []IocEntry     `json:"set"`
	Rejected int            `json:"rejected"`
}

type snapshotDocument struct {
	Magic    string          `json:"magic"`
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises the whole normalizer state.
func (n *IocNormalizer) Snapshot() ([]byte, error) {
	n.mu.RLock()
	payload := snapshotPayload{
		Feeds:    make([]snapshotFeed, 0, len(n.feeds)),
		Set:      []IocEntry{},
		Rejected: int(n.rejected),
	}
	names := make([]string, 0, len(n.feeds))
	for name := range n.feeds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		feed := n.feeds[name]
		indicators := append([]Indicator(nil), feed.indicators...)
		if indicators == nil {
			indicators = []Indicator{}
		}
		payload.Feeds = append(payload.Feeds, snapshotFeed{
			Name:       feed.name,
			DefaultTTL: feed.defaultTTL,
			Indicators: indicators,
		})
	}
	if n.set != nil {
		payload.Set = cloneEntries(n.set.entries)
	}
	n.mu.RUnlock()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payloadBytes)
	document := snapshotDocument{
		Magic:    snapshotMagic,
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(sum[:]),
		Payload:  payloadBytes,
	}
	return json.Marshal(document)
}

// Restore replaces the whole normalizer state from a snapshot.
func (n *IocNormalizer) Restore(data []byte) error {
	var document snapshotDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return ErrBadSnapshot
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrBadSnapshot
	}
	if document.Magic != snapshotMagic || document.Version != snapshotVersion ||
		len(document.Payload) == 0 || document.Checksum == "" {
		return ErrBadSnapshot
	}
	sum := sha256.Sum256(document.Payload)
	if !strings.EqualFold(document.Checksum, hex.EncodeToString(sum[:])) {
		return ErrBadSnapshot
	}

	payloadDecoder := json.NewDecoder(bytes.NewReader(document.Payload))
	payloadDecoder.DisallowUnknownFields()
	var payload snapshotPayload
	if err := payloadDecoder.Decode(&payload); err != nil {
		return ErrBadSnapshot
	}
	if err := payloadDecoder.Decode(&trailing); err != io.EOF {
		return ErrBadSnapshot
	}
	if payload.Rejected < 0 || payload.Feeds == nil || payload.Set == nil {
		return ErrBadSnapshot
	}

	feeds, err := validateSnapshotFeeds(payload.Feeds)
	if err != nil {
		return ErrBadSnapshot
	}
	entries, err := validateSnapshotEntries(payload.Set)
	if err != nil {
		return ErrBadSnapshot
	}
	compiled, err := compileEntries(entries)
	if err != nil {
		return ErrBadSnapshot
	}

	n.mu.Lock()
	n.feeds = feeds
	n.set = compiled
	n.rejected = int64(payload.Rejected)
	n.mu.Unlock()
	return nil
}

// Reset returns the normalizer to its initial state.
func (n *IocNormalizer) Reset() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.feeds = make(map[string]feedState)
	n.set = emptyCompiledSet()
	n.rejected = 0
	return nil
}

func (n *IocNormalizer) reject(count int) {
	n.mu.Lock()
	n.rejected += int64(count)
	n.mu.Unlock()
}

func (n *IocNormalizer) lookupIP(value string) ([]Source, bool) {
	if n.set == nil {
		return nil, false
	}

	var (
		query      netip.Prefix
		queryIsNet bool
	)
	if strings.Contains(value, "/") {
		parsed, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, false
		}
		query = parsed.Masked()
		queryIsNet = true
	} else {
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return nil, false
		}
		query = netip.PrefixFrom(addr, addr.BitLen())
	}

	var (
		best     *ipLookupEntry
		bestBits = -1
	)
	for i := range n.set.ips {
		entry := &n.set.ips[i]
		if !queryIsNet {
			if entry.prefix.Contains(query.Addr()) && entry.prefix.Bits() > bestBits {
				best = entry
				bestBits = entry.prefix.Bits()
			}
			continue
		}
		if entry.prefix.Bits() <= query.Bits() && entry.prefix.Contains(query.Addr()) &&
			entry.prefix.Bits() > bestBits {
			best = entry
			bestBits = entry.prefix.Bits()
		}
	}
	if best == nil {
		return nil, false
	}
	return cloneSources(best.sources), true
}

func inferKind(value string) string {
	if _, err := netip.ParseAddr(value); err == nil {
		return "ip"
	}
	if _, err := netip.ParsePrefix(value); err == nil {
		return "ip"
	}
	if strings.Contains(value, "://") {
		return "url"
	}
	return "domain"
}

func canonicalIP(value string) (string, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", err
		}
		return prefix.Masked().String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

func canonicalDomain(value string) (string, error) {
	domain := strings.ToLower(value)
	if strings.HasSuffix(domain, ".") {
		domain = domain[:len(domain)-1]
	}
	if domain == "" {
		return "", ErrInvalidIndicator
	}

	body := domain
	if strings.HasPrefix(body, "*.") {
		body = body[2:]
		if body == "" {
			return "", ErrInvalidIndicator
		}
	}
	if strings.Contains(body, "*") || len(body) > 253 {
		return "", ErrInvalidIndicator
	}
	for _, label := range strings.Split(body, ".") {
		if !validDomainLabel(label) {
			return "", ErrInvalidIndicator
		}
	}
	return domain, nil
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		b := label[i]
		if (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' {
			continue
		}
		return false
	}
	return true
}

func canonicalURL(value string) (string, error) {
	separator := strings.Index(value, "://")
	if separator <= 0 {
		return "", ErrInvalidIndicator
	}
	scheme := value[:separator]
	if !validScheme(scheme) {
		return "", ErrInvalidIndicator
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", ErrInvalidIndicator
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", ErrInvalidIndicator
	}
	if _, err := netip.ParseAddr(hostname); err != nil {
		domain := strings.TrimSuffix(strings.ToLower(hostname), ".")
		if _, err := canonicalBareDomain(domain); err != nil {
			return "", ErrInvalidIndicator
		}
	}

	rest := value[separator+3:]
	authorityEnd := len(rest)
	if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
		authorityEnd = idx
	}
	authority := rest[:authorityEnd]
	if authority == "" {
		return "", ErrInvalidIndicator
	}
	userInfo := ""
	hostPart := authority
	if idx := strings.LastIndex(authority, "@"); idx >= 0 {
		userInfo = authority[:idx+1]
		hostPart = authority[idx+1:]
	}
	if hostPart == "" {
		return "", ErrInvalidIndicator
	}

	return strings.ToLower(scheme) + "://" + userInfo +
		strings.ToLower(hostPart) + rest[authorityEnd:], nil
}

func canonicalBareDomain(value string) (string, error) {
	if value == "" || len(value) > 253 {
		return "", ErrInvalidIndicator
	}
	for _, label := range strings.Split(value, ".") {
		if !validDomainLabel(label) {
			return "", ErrInvalidIndicator
		}
	}
	return value, nil
}

func validScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for i := 0; i < len(scheme); i++ {
		b := scheme[i]
		if i == 0 {
			if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')) {
				return false
			}
			continue
		}
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '+' || b == '-' || b == '.' {
			continue
		}
		return false
	}
	return true
}

type mergeGroup struct {
	kind    string
	value   string
	sources map[string]Source
}

type mergeIPItem struct {
	prefix  netip.Prefix
	single  bool
	sources map[string]Source
}

func mergeFeeds(feeds map[string]feedState) ([]IocEntry, error) {
	groups := make(map[string]*mergeGroup)
	for _, feed := range feeds {
		for _, indicator := range feed.indicators {
			sourceName := indicator.Source
			if sourceName == "" {
				sourceName = feed.name
			}
			key := indicator.Kind + "\x00" + indicator.Value
			group := groups[key]
			if group == nil {
				group = &mergeGroup{
					kind:    indicator.Kind,
					value:   indicator.Value,
					sources: make(map[string]Source),
				}
				groups[key] = group
			}
			addSource(group.sources, Source{Name: sourceName, TTL: indicator.TTL})
		}
	}

	var ipGroups []*mergeGroup
	var otherGroups []*mergeGroup
	for _, group := range groups {
		if group.kind == "ip" {
			ipGroups = append(ipGroups, group)
		} else {
			otherGroups = append(otherGroups, group)
		}
	}

	mergedIPGroups, err := mergeIPGroups(ipGroups)
	if err != nil {
		return nil, err
	}
	otherGroups = append(otherGroups, mergedIPGroups...)

	entries := make([]IocEntry, 0, len(otherGroups))
	for _, group := range otherGroups {
		sources := sortedSources(group.sources)
		entries = append(entries, IocEntry{
			Value:   group.value,
			Kind:    group.kind,
			TTL:     minimumTTL(sources),
			Sources: sources,
		})
	}
	sortEntries(entries)
	return entries, nil
}

func mergeIPGroups(groups []*mergeGroup) ([]*mergeGroup, error) {
	items := make(map[string]*mergeIPItem)
	for _, group := range groups {
		item, err := parseMergeIPItem(group)
		if err != nil {
			return nil, err
		}
		prefixKey := item.prefix.String()
		if existing, ok := items[prefixKey]; ok {
			combineSources(existing.sources, item.sources)
			existing.single = existing.single && item.single
			continue
		}
		items[prefixKey] = item
	}

	removed := make(map[string]bool)
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	for _, key := range keys {
		item := items[key]
		var (
			container     *mergeIPItem
			containerBits = item.prefix.Bits()
		)
		for bits := item.prefix.Bits() - 1; bits >= 0; bits-- {
			candidate := netip.PrefixFrom(item.prefix.Addr(), bits).Masked()
			if other, ok := items[candidate.String()]; ok && other != item {
				if bits < containerBits {
					container = other
					containerBits = bits
				}
			}
		}
		if container != nil {
			combineSources(container.sources, item.sources)
			if !item.single {
				container.single = false
			}
			removed[key] = true
		}
	}

	live := make(map[string]*mergeIPItem, len(items)-len(removed))
	for key, item := range items {
		if !removed[key] {
			live[key] = item
		}
	}

	maxBits := 0
	for _, item := range live {
		if item.prefix.Addr().BitLen() > maxBits {
			maxBits = item.prefix.Addr().BitLen()
		}
	}
	for bits := maxBits; bits >= 1; bits-- {
		siblings := make(map[string][]*mergeIPItem)
		var order []string
		for _, item := range live {
			if item.prefix.Bits() != bits || item.single {
				continue
			}
			parent := netip.PrefixFrom(item.prefix.Addr(), bits-1).Masked()
			parentKey := parent.String()
			if _, ok := siblings[parentKey]; !ok {
				order = append(order, parentKey)
			}
			siblings[parentKey] = append(siblings[parentKey], item)
		}
		for _, parentKey := range order {
			pair := siblings[parentKey]
			if len(pair) != 2 {
				continue
			}
			if pair[0].prefix.Bits() != pair[1].prefix.Bits() {
				continue
			}
			parent, err := netip.ParsePrefix(parentKey)
			if err != nil {
				return nil, err
			}
			combined := &mergeIPItem{
				prefix:  parent,
				single:  false,
				sources: make(map[string]Source),
			}
			combineSources(combined.sources, pair[0].sources)
			combineSources(combined.sources, pair[1].sources)
			delete(live, pair[0].prefix.String())
			delete(live, pair[1].prefix.String())
			live[parentKey] = combined
		}
	}

	result := make([]*mergeGroup, 0, len(live))
	for _, item := range live {
		value := item.prefix.String()
		if item.single {
			value = item.prefix.Addr().String()
		}
		result = append(result, &mergeGroup{
			kind:    "ip",
			value:   value,
			sources: item.sources,
		})
	}
	return result, nil
}

func parseMergeIPItem(group *mergeGroup) (*mergeIPItem, error) {
	item := &mergeIPItem{
		single:  true,
		sources: make(map[string]Source),
	}
	combineSources(item.sources, group.sources)
	if strings.Contains(group.value, "/") {
		prefix, err := netip.ParsePrefix(group.value)
		if err != nil {
			return nil, err
		}
		item.prefix = prefix.Masked()
		item.single = false
		return item, nil
	}
	addr, err := netip.ParseAddr(group.value)
	if err != nil {
		return nil, err
	}
	item.prefix = netip.PrefixFrom(addr, addr.BitLen())
	return item, nil
}

func addSource(target map[string]Source, source Source) {
	existing, ok := target[source.Name]
	if !ok || source.TTL < existing.TTL {
		target[source.Name] = source
	}
}

func combineSources(target, source map[string]Source) {
	for _, item := range source {
		addSource(target, item)
	}
}

func sortedSources(sources map[string]Source) []Source {
	result := make([]Source, 0, len(sources))
	for _, source := range sources {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func minimumTTL(sources []Source) int64 {
	if len(sources) == 0 {
		return 0
	}
	minimum := sources[0].TTL
	for _, source := range sources[1:] {
		if source.TTL < minimum {
			minimum = source.TTL
		}
	}
	return minimum
}

func sortEntries(entries []IocEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Value < entries[j].Value
	})
}

func compileEntries(entries []IocEntry) (*compiledSet, error) {
	cloned := cloneEntries(entries)
	sortEntries(cloned)
	compiled := &compiledSet{
		entries: cloned,
		domains: make(map[string][]Source),
		urls:    make(map[string][]Source),
	}
	for _, entry := range cloned {
		sources := cloneSources(entry.Sources)
		switch entry.Kind {
		case "ip":
			prefix, err := parseIPValue(entry.Value)
			if err != nil {
				return nil, err
			}
			compiled.ips = append(compiled.ips, ipLookupEntry{
				prefix:  prefix,
				sources: sources,
			})
		case "domain":
			if strings.HasPrefix(entry.Value, "*.") {
				compiled.wildcards = append(compiled.wildcards, wildcardLookupEntry{
					suffix:  entry.Value[2:],
					sources: sources,
				})
			} else {
				compiled.domains[entry.Value] = sources
			}
		case "url":
			compiled.urls[entry.Value] = sources
		default:
			return nil, ErrInvalidIndicator
		}
	}
	sort.Slice(compiled.wildcards, func(i, j int) bool {
		if len(compiled.wildcards[i].suffix) != len(compiled.wildcards[j].suffix) {
			return len(compiled.wildcards[i].suffix) > len(compiled.wildcards[j].suffix)
		}
		return compiled.wildcards[i].suffix < compiled.wildcards[j].suffix
	})
	return compiled, nil
}

func parseIPValue(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func emptyCompiledSet() *compiledSet {
	return &compiledSet{
		entries: []IocEntry{},
		domains: make(map[string][]Source),
		urls:    make(map[string][]Source),
	}
}

func (s *compiledSet) asSet() *IocSet {
	if s == nil {
		return &IocSet{Entries: []IocEntry{}}
	}
	return &IocSet{Entries: cloneEntries(s.entries)}
}

func cloneEntries(entries []IocEntry) []IocEntry {
	if len(entries) == 0 {
		return []IocEntry{}
	}
	result := make([]IocEntry, len(entries))
	for i, entry := range entries {
		result[i] = IocEntry{
			Value:   entry.Value,
			Kind:    entry.Kind,
			TTL:     entry.TTL,
			Sources: cloneSources(entry.Sources),
		}
	}
	return result
}

func cloneSources(sources []Source) []Source {
	if len(sources) == 0 {
		return []Source{}
	}
	result := make([]Source, len(sources))
	copy(result, sources)
	return result
}

func validateSnapshotFeeds(raw []snapshotFeed) (map[string]feedState, error) {
	feeds := make(map[string]feedState, len(raw))
	validator := &IocNormalizer{}
	for _, feed := range raw {
		if feed.Name == "" {
			return nil, ErrBadSnapshot
		}
		if _, exists := feeds[feed.Name]; exists {
			return nil, ErrBadSnapshot
		}
		indicators := make([]Indicator, 0, len(feed.Indicators))
		seen := make(map[string]bool, len(feed.Indicators))
		for _, indicator := range feed.Indicators {
			if indicator.TTL < 0 {
				return nil, ErrBadSnapshot
			}
			canonical, err := validator.Normalize(indicator)
			if err != nil || canonical.Kind != indicator.Kind ||
				canonical.Value != indicator.Value || canonical.TTL != indicator.TTL {
				return nil, ErrBadSnapshot
			}
			if indicator.Source != feed.Name {
				return nil, ErrBadSnapshot
			}
			key := indicator.Kind + "\x00" + indicator.Value
			if seen[key] {
				return nil, ErrBadSnapshot
			}
			seen[key] = true
			indicators = append(indicators, indicator)
		}
		feeds[feed.Name] = feedState{
			name:       feed.Name,
			defaultTTL: feed.DefaultTTL,
			indicators: indicators,
		}
	}
	return feeds, nil
}

func validateSnapshotEntries(raw []IocEntry) ([]IocEntry, error) {
	entries := make([]IocEntry, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	validator := &IocNormalizer{}
	for _, entry := range raw {
		if entry.TTL < 0 || len(entry.Sources) == 0 {
			return nil, ErrBadSnapshot
		}
		canonical, err := validator.Normalize(Indicator{
			Value: entry.Value,
			Kind:  entry.Kind,
			TTL:   entry.TTL,
		})
		if err != nil || canonical.Value != entry.Value || canonical.Kind != entry.Kind {
			return nil, ErrBadSnapshot
		}

		sources := cloneSources(entry.Sources)
		sort.Slice(sources, func(i, j int) bool {
			return sources[i].Name < sources[j].Name
		})
		for i, source := range sources {
			if source.Name == "" || source.TTL < 0 {
				return nil, ErrBadSnapshot
			}
			if i > 0 && sources[i-1].Name == source.Name {
				return nil, ErrBadSnapshot
			}
		}
		if minimumTTL(sources) != entry.TTL {
			return nil, ErrBadSnapshot
		}

		key := entry.Kind + "\x00" + entry.Value
		if seen[key] {
			return nil, ErrBadSnapshot
		}
		seen[key] = true
		entries = append(entries, IocEntry{
			Value:   entry.Value,
			Kind:    entry.Kind,
			TTL:     entry.TTL,
			Sources: sources,
		})
	}
	sortEntries(entries)
	return entries, nil
}
