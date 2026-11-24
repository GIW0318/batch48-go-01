package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-c-12"
)

func entryByValue(entries []impl.IocEntry, value string) *impl.IocEntry {
	for i := range entries {
		if entries[i].Value == value {
			return &entries[i]
		}
	}
	return nil
}

func TestNormalizeInfersKinds(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	addr, err := n.Normalize(impl.Indicator{Value: "10.7.3.9"})
	if err != nil {
		t.Fatalf("normalize address: %v", err)
	}
	if addr.Kind != "ip" || addr.Value != "10.7.3.9" {
		t.Fatalf("address: got kind=%q value=%q", addr.Kind, addr.Value)
	}
	domain, err := n.Normalize(impl.Indicator{Value: "mail.example.org"})
	if err != nil {
		t.Fatalf("normalize domain: %v", err)
	}
	if domain.Kind != "domain" || domain.Value != "mail.example.org" {
		t.Fatalf("domain: got kind=%q value=%q", domain.Kind, domain.Value)
	}
	url, err := n.Normalize(impl.Indicator{Value: "https://intel.example.org/drop"})
	if err != nil {
		t.Fatalf("normalize url: %v", err)
	}
	if url.Kind != "url" || url.Value != "https://intel.example.org/drop" {
		t.Fatalf("url: got kind=%q value=%q", url.Kind, url.Value)
	}
}

func TestNormalizeCanonicalizesDomainAndURL(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	domain, err := n.Normalize(impl.Indicator{Value: "  Example.COM.  "})
	if err != nil {
		t.Fatalf("normalize domain: %v", err)
	}
	if domain.Value != "example.com" || domain.Kind != "domain" {
		t.Fatalf("domain canonicalisation: got kind=%q value=%q", domain.Kind, domain.Value)
	}
	url, err := n.Normalize(impl.Indicator{Value: "HTTPS://Example.COM/Login"})
	if err != nil {
		t.Fatalf("normalize url: %v", err)
	}
	if url.Value != "https://example.com/Login" {
		t.Fatalf("url canonicalisation: got %q", url.Value)
	}
}

func TestNormalizeMasksCIDRHostBits(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	got, err := n.Normalize(impl.Indicator{Value: "10.7.3.129/24"})
	if err != nil {
		t.Fatalf("normalize cidr: %v", err)
	}
	if got.Kind != "ip" {
		t.Fatalf("cidr kind: want ip, got %q", got.Kind)
	}
	if got.Value != "10.7.3.0/24" {
		t.Fatalf("cidr canonicalisation: want 10.7.3.0/24, got %q", got.Value)
	}
}

func TestNormalizeRejectsTypeConflict(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if _, err := n.Normalize(impl.Indicator{Value: "10.0.0.0/24", Kind: "domain"}); !errors.Is(err, impl.ErrTypeConflict) {
		t.Fatalf("declared domain for a prefix: want ErrTypeConflict, got %v", err)
	}
}

func TestNormalizeRejectsNegativeTTL(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if _, err := n.Normalize(impl.Indicator{Value: "evil.example.net", TTL: -1}); !errors.Is(err, impl.ErrInvalidIndicator) {
		t.Fatalf("negative ttl: want ErrInvalidIndicator, got %v", err)
	}
}

func TestIngestRejectsFeedOverPerFeedLimit(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 2})
	if err := n.Ingest(impl.Feed{Name: "", Indicators: []impl.Indicator{{Value: "a.example"}}}); !errors.Is(err, impl.ErrInvalidFeed) {
		t.Fatalf("empty feed name: want ErrInvalidFeed, got %v", err)
	}
	feed := impl.Feed{Name: "feed-a", DefaultTTL: 100, Indicators: []impl.Indicator{
		{Value: "a.example"},
		{Value: "b.example"},
		{Value: "c.example"},
	}}
	if err := n.Ingest(feed); !errors.Is(err, impl.ErrFeedTooLarge) {
		t.Fatalf("three indicators with MaxPerFeed 2: want ErrFeedTooLarge, got %v", err)
	}
	stats := n.Stats()
	if stats.Feeds != 0 || stats.Ingested != 0 {
		t.Fatalf("rejected feed must not be stored: feeds=%d ingested=%d", stats.Feeds, stats.Ingested)
	}
}

func TestIngestIsAtomicOnInvalidIndicator(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	feed := impl.Feed{Name: "feed-a", DefaultTTL: 60, Indicators: []impl.Indicator{
		{Value: "good.example"},
		{Value: "bad..example"},
	}}
	if err := n.Ingest(feed); !errors.Is(err, impl.ErrInvalidIndicator) {
		t.Fatalf("feed with one invalid indicator: want ErrInvalidIndicator, got %v", err)
	}
	if got := n.Stats().Ingested; got != 0 {
		t.Fatalf("no indicator may survive a rejected feed, got %d ingested", got)
	}
	set, err := n.Merge()
	if err != nil {
		t.Fatalf("merge empty normalizer: %v", err)
	}
	if set.Len() != 0 {
		t.Fatalf("empty normalizer must merge to an empty set, got %d entries", set.Len())
	}
}

func TestMergeCombinesContainedCIDRs(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 500, Indicators: []impl.Indicator{{Value: "10.0.0.0/24"}}}); err != nil {
		t.Fatalf("ingest feed-a: %v", err)
	}
	if err := n.Ingest(impl.Feed{Name: "feed-b", DefaultTTL: 500, Indicators: []impl.Indicator{{Value: "10.0.0.0/25"}}}); err != nil {
		t.Fatalf("ingest feed-b: %v", err)
	}
	set, err := n.Merge()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("contained prefixes must fold into one entry, got %d", set.Len())
	}
	entry := entryByValue(set.Entries, "10.0.0.0/24")
	if entry == nil {
		t.Fatalf("merged entry 10.0.0.0/24 missing, got %+v", set.Entries)
	}
	if len(entry.Sources) != 2 || entry.Sources[0].Name != "feed-a" || entry.Sources[1].Name != "feed-b" {
		t.Fatalf("merged sources: want [feed-a feed-b], got %+v", entry.Sources)
	}
}

func TestMergeCombinesAdjacentCIDRs(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 500, Indicators: []impl.Indicator{{Value: "10.0.0.0/25"}}}); err != nil {
		t.Fatalf("ingest feed-a: %v", err)
	}
	if err := n.Ingest(impl.Feed{Name: "feed-b", DefaultTTL: 500, Indicators: []impl.Indicator{{Value: "10.0.0.128/25"}}}); err != nil {
		t.Fatalf("ingest feed-b: %v", err)
	}
	set, err := n.Merge()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("adjacent prefixes must fold into one entry, got %d", set.Len())
	}
	if set.Entries[0].Value != "10.0.0.0/24" {
		t.Fatalf("adjacent merge: want 10.0.0.0/24, got %q", set.Entries[0].Value)
	}
}

func TestMergeKeepsMinTTLAndSortedSources(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 300, Indicators: []impl.Indicator{{Value: "example.com"}}}); err != nil {
		t.Fatalf("ingest feed-a: %v", err)
	}
	if err := n.Ingest(impl.Feed{Name: "feed-b", DefaultTTL: 100, Indicators: []impl.Indicator{{Value: "Example.COM."}}}); err != nil {
		t.Fatalf("ingest feed-b: %v", err)
	}
	set, err := n.Merge()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	entry := entryByValue(set.Entries, "example.com")
	if entry == nil {
		t.Fatalf("merged domain entry missing, got %+v", set.Entries)
	}
	if entry.TTL != 100 {
		t.Fatalf("merged ttl: want minimum 100, got %d", entry.TTL)
	}
	if len(entry.Sources) != 2 {
		t.Fatalf("merged sources: want 2, got %d", len(entry.Sources))
	}
	if entry.Sources[0].Name != "feed-a" || entry.Sources[0].TTL != 300 {
		t.Fatalf("first source: got %+v", entry.Sources[0])
	}
	if entry.Sources[1].Name != "feed-b" || entry.Sources[1].TTL != 100 {
		t.Fatalf("second source: got %+v", entry.Sources[1])
	}
}

func TestMergeOrderIsStable(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 100, Indicators: []impl.Indicator{
		{Value: "zeta.example"},
		{Value: "10.0.0.0/24"},
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	first, err := n.Merge()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	second, err := n.Merge()
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if first.Len() != 2 {
		t.Fatalf("entries: want 2, got %d", first.Len())
	}
	if first.Entries[0].Kind != "domain" || first.Entries[0].Value != "zeta.example" {
		t.Fatalf("first entry: want the domain, got %+v", first.Entries[0])
	}
	if first.Entries[1].Kind != "ip" || first.Entries[1].Value != "10.0.0.0/24" {
		t.Fatalf("second entry: want the prefix, got %+v", first.Entries[1])
	}
	values := first.Values()
	want := []string{"zeta.example", "10.0.0.0/24"}
	if len(values) != len(want) || values[0] != want[0] || values[1] != want[1] {
		t.Fatalf("values order: want %v, got %v", want, values)
	}
	secondValues := second.Values()
	if len(secondValues) != len(values) || secondValues[0] != values[0] || secondValues[1] != values[1] {
		t.Fatalf("repeated merge must be stable: %v then %v", values, secondValues)
	}
	if second.Entries[0].Value != first.Entries[0].Value || second.Entries[1].Value != first.Entries[1].Value {
		t.Fatalf("repeated merge entries changed: %+v then %+v", first.Entries, second.Entries)
	}
}

func TestMergeRejectsSetOverLimit(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 2, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 100, Indicators: []impl.Indicator{
		{Value: "a.example"},
		{Value: "b.example"},
		{Value: "10.0.0.0/24"},
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := n.Merge(); !errors.Is(err, impl.ErrSetLimit) {
		t.Fatalf("merge over MaxEntries: want ErrSetLimit, got %v", err)
	}
	if len(n.Entries()) != 0 {
		t.Fatalf("rejected merge must keep the previous (empty) set, got %d entries", len(n.Entries()))
	}
	if _, ok := n.Lookup("a.example"); ok {
		t.Fatalf("lookup must not answer from a rejected merge")
	}
}

func TestLookupIPByContainment(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 400, Indicators: []impl.Indicator{{Value: "10.0.0.0/24"}}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := n.Merge(); err != nil {
		t.Fatalf("merge: %v", err)
	}
	sources, ok := n.Lookup("10.0.0.77")
	if !ok {
		t.Fatalf("lookup 10.0.0.77: want a hit inside 10.0.0.0/24")
	}
	if len(sources) != 1 || sources[0].Name != "feed-a" || sources[0].TTL != 400 {
		t.Fatalf("lookup sources: got %+v", sources)
	}
	if _, ok := n.Lookup("10.0.1.1"); ok {
		t.Fatalf("lookup 10.0.1.1: must miss 10.0.0.0/24")
	}
}

func TestLookupDomainAndURLAndDropSource(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 200, Indicators: []impl.Indicator{{Value: "example.com"}}}); err != nil {
		t.Fatalf("ingest feed-a: %v", err)
	}
	if err := n.Ingest(impl.Feed{Name: "feed-b", DefaultTTL: 200, Indicators: []impl.Indicator{{Value: "https://example.com/Login"}}}); err != nil {
		t.Fatalf("ingest feed-b: %v", err)
	}
	if _, err := n.Merge(); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if sources, ok := n.Lookup("Example.COM."); !ok || len(sources) != 1 || sources[0].Name != "feed-a" {
		t.Fatalf("domain lookup: ok=%v sources=%+v", ok, sources)
	}
	if sources, ok := n.Lookup("HTTPS://Example.COM/Login"); !ok || len(sources) != 1 || sources[0].Name != "feed-b" {
		t.Fatalf("url lookup: ok=%v sources=%+v", ok, sources)
	}
	if _, ok := n.Lookup("https://example.com/other"); ok {
		t.Fatalf("path mismatch must not match the url indicator")
	}
	if err := n.DropSource("feed-a"); err != nil {
		t.Fatalf("drop feed-a: %v", err)
	}
	set, err := n.Merge()
	if err != nil {
		t.Fatalf("merge after drop: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("after dropping feed-a: want 1 entry, got %d", set.Len())
	}
	if _, ok := n.Lookup("example.com"); ok {
		t.Fatalf("dropped domain must disappear from the set")
	}
	if err := n.DropSource("feed-ghost"); !errors.Is(err, impl.ErrUnknownSource) {
		t.Fatalf("unknown source: want ErrUnknownSource, got %v", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	n := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := n.Ingest(impl.Feed{Name: "feed-a", DefaultTTL: 300, Indicators: []impl.Indicator{
		{Value: "a.example"},
		{Value: "10.0.0.0/24"},
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := n.Merge(); err != nil {
		t.Fatalf("merge: %v", err)
	}
	data, err := n.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewIocNormalizer(impl.NormalizerOptions{MaxEntries: 8, MaxPerFeed: 8})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restored.Entries()); got != 2 {
		t.Fatalf("entries after restore: want 2, got %d", got)
	}
	if sources, ok := restored.Lookup("10.0.0.42"); !ok || sources[0].Name != "feed-a" {
		t.Fatalf("lookup after restore: ok=%v sources=%+v", ok, sources)
	}
	want, got := n.Stats(), restored.Stats()
	if want != got {
		t.Fatalf("stats after restore: want %+v, got %+v", want, got)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot: want ErrBadSnapshot, got %v", err)
	}
}
