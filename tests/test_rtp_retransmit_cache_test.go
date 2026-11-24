package task

import (
	"testing"

	impl "gsb/batch48-go-game-c-10"
)

func cacheLimits() impl.Limits {
	return impl.Limits{
		MaxPackets:     8,
		MaxBytes:       8192,
		MaxPacketBytes: 512,
		MaxRetransmits: 3,
		CoalesceMs:     500,
		MinTTLMs:       1000,
	}
}

func fillPayload(n int, fill byte) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = fill
	}
	return buf
}

func newRetransmitCache(t *testing.T, limits impl.Limits) *impl.RetransmitCache {
	t.Helper()
	c, err := impl.NewRetransmitCache(limits)
	if err != nil {
		t.Fatalf("NewRetransmitCache returned error: %v", err)
	}
	if c == nil {
		t.Fatal("NewRetransmitCache returned nil")
	}
	return c
}

func storePacket(t *testing.T, c *impl.RetransmitCache, ssrc uint32, seq uint16, sentMs int64, size int) {
	t.Helper()
	p := impl.Packet{SSRC: ssrc, Seq: seq, SentMs: sentMs, Payload: fillPayload(size, 0xAB)}
	if err := c.Store(p); err != nil {
		t.Fatalf("Store(seq %d) returned error: %v", seq, err)
	}
}

func TestNewRejectsInvalidLimits(t *testing.T) {
	bad := []impl.Limits{
		{MaxPackets: 0, MaxBytes: 8192, MaxPacketBytes: 512, MaxRetransmits: 3, CoalesceMs: 100, MinTTLMs: 1000},
		{MaxPackets: 8, MaxBytes: 0, MaxPacketBytes: 512, MaxRetransmits: 3, CoalesceMs: 100, MinTTLMs: 1000},
		{MaxPackets: 8, MaxBytes: 8192, MaxPacketBytes: 512, MaxRetransmits: 0, CoalesceMs: 100, MinTTLMs: 1000},
	}
	for i, limits := range bad {
		if _, err := impl.NewRetransmitCache(limits); err != impl.ErrLimitsUnusable {
			t.Fatalf("case %d: expected ErrLimitsUnusable, got %v", i, err)
		}
	}
}

func TestSetLimitsRejectsInvalidLimits(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	bad := cacheLimits()
	bad.MaxPacketBytes = 0
	if err := c.SetLimits(bad); err != impl.ErrLimitsUnusable {
		t.Fatalf("expected ErrLimitsUnusable, got %v", err)
	}
}

func TestStoreRejectsEmptyPayload(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	err := c.Store(impl.Packet{SSRC: 7, Seq: 10, SentMs: 0, Payload: nil})
	if err != impl.ErrPayloadEmpty {
		t.Fatalf("expected ErrPayloadEmpty, got %v", err)
	}
}

func TestStoreRejectsOversizedPacket(t *testing.T) {
	limits := cacheLimits()
	limits.MaxPacketBytes = 32
	c := newRetransmitCache(t, limits)
	err := c.Store(impl.Packet{SSRC: 7, Seq: 10, SentMs: 0, Payload: fillPayload(33, 1)})
	if err != impl.ErrPacketTooLarge {
		t.Fatalf("expected ErrPacketTooLarge, got %v", err)
	}
}

func TestStoreRejectsDuplicatePacket(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 16)
	err := c.Store(impl.Packet{SSRC: 7, Seq: 10, SentMs: 5, Payload: fillPayload(16, 2)})
	if err != impl.ErrDuplicatePacket {
		t.Fatalf("expected ErrDuplicatePacket, got %v", err)
	}
	if got := c.Stats().Duplicates; got != 1 {
		t.Fatalf("duplicates = %d, want 1", got)
	}
}

func TestLookupFindsCachedPacket(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 16)
	got, ok := c.Lookup(7, 10)
	if !ok {
		t.Fatal("Lookup did not find the stored packet")
	}
	if len(got.Payload) != 16 || got.Seq != 10 || got.SSRC != 7 {
		t.Fatalf("lookup result = %+v, want ssrc 7 seq 10 with 16 bytes", got)
	}
	if _, ok := c.Lookup(7, 11); ok {
		t.Fatal("Lookup reported a packet that was never stored")
	}
}

func TestRequestServesPacketAndCountsRetransmit(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 24)
	pkt, err := c.Request(7, 10, 1000)
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	if len(pkt.Payload) != 24 {
		t.Fatalf("retransmitted payload length = %d, want 24", len(pkt.Payload))
	}
	if got := c.Stats().Retransmits; got != 1 {
		t.Fatalf("retransmits = %d, want 1", got)
	}
}

func TestRequestUnknownSequenceMisses(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	if _, err := c.Request(7, 99, 1000); err != impl.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if got := c.Stats().Misses; got != 1 {
		t.Fatalf("misses = %d, want 1", got)
	}
}

func TestDuplicateRequestIsCoalesced(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 16)
	if _, err := c.Request(7, 10, 1000); err != nil {
		t.Fatalf("first Request returned error: %v", err)
	}
	if _, err := c.Request(7, 10, 1200); err != impl.ErrDuplicateRequest {
		t.Fatalf("expected ErrDuplicateRequest inside the coalescing window, got %v", err)
	}
	stats := c.Stats()
	if stats.Retransmits != 1 {
		t.Fatalf("retransmits = %d, want the coalesced request not to count", stats.Retransmits)
	}
	if stats.Coalesced != 1 {
		t.Fatalf("coalesced = %d, want 1", stats.Coalesced)
	}
}

func TestRequestStopsAtRetransmitLimit(t *testing.T) {
	limits := cacheLimits()
	limits.MaxRetransmits = 2
	limits.CoalesceMs = 0
	c := newRetransmitCache(t, limits)
	storePacket(t, c, 7, 10, 0, 16)
	if _, err := c.Request(7, 10, 1000); err != nil {
		t.Fatalf("first Request returned error: %v", err)
	}
	if _, err := c.Request(7, 10, 1100); err != nil {
		t.Fatalf("second Request returned error: %v", err)
	}
	if _, err := c.Request(7, 10, 1200); err != impl.ErrRetransmitLimit {
		t.Fatalf("expected ErrRetransmitLimit, got %v", err)
	}
	if got := c.Stats().Retransmits; got != 2 {
		t.Fatalf("retransmits = %d, want 2", got)
	}
}

func TestPurgeExpiresOldPackets(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 16)
	if got := c.Purge(900); got != 0 {
		t.Fatalf("purged %d packets at 900ms, want 0 inside the retention window", got)
	}
	if got := c.Purge(1500); got != 1 {
		t.Fatalf("purged %d packets at 1500ms, want 1", got)
	}
	if got := c.Pending(); got != 0 {
		t.Fatalf("pending after purge = %d, want 0", got)
	}
	if got := c.Stats().Expired; got != 1 {
		t.Fatalf("expired counter = %d, want 1", got)
	}
}

func TestPacketCapEvictsOldest(t *testing.T) {
	limits := cacheLimits()
	limits.MaxPackets = 3
	c := newRetransmitCache(t, limits)
	for seq := uint16(1); seq <= 5; seq++ {
		storePacket(t, c, 7, seq, int64(seq), 10)
	}
	if got := c.Pending(); got != 3 {
		t.Fatalf("pending = %d, want 3", got)
	}
	if got := c.Stats().Evicted; got != 2 {
		t.Fatalf("evicted = %d, want 2", got)
	}
}

func TestByteCapEvictsOldest(t *testing.T) {
	limits := cacheLimits()
	limits.MaxBytes = 1000
	limits.MaxPacketBytes = 512
	c := newRetransmitCache(t, limits)
	for seq := uint16(1); seq <= 3; seq++ {
		storePacket(t, c, 7, seq, int64(seq), 400)
	}
	if got := c.Pending(); got != 2 {
		t.Fatalf("pending = %d, want 2 after the byte cap evicted the oldest", got)
	}
	if got := c.Stats().Bytes; got != 800 {
		t.Fatalf("cached bytes = %d, want 800", got)
	}
}

func TestSnapshotRestoreKeepsPacketsAndCounters(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 16)
	storePacket(t, c, 7, 11, 10, 16)
	if _, err := c.Request(7, 11, 1000); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	st, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	r := newRetransmitCache(t, cacheLimits())
	if err := r.Restore(st); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if _, ok := r.Lookup(7, 11); !ok {
		t.Fatal("packet 7/11 missing after Restore")
	}
	if got := r.Stats().Retransmits; got != 1 {
		t.Fatalf("restored retransmits = %d, want 1", got)
	}
	if got := r.Pending(); got != 2 {
		t.Fatalf("restored pending = %d, want 2", got)
	}
}

func TestBytesReportsPayloadTotal(t *testing.T) {
	c := newRetransmitCache(t, cacheLimits())
	storePacket(t, c, 7, 10, 0, 100)
	storePacket(t, c, 7, 11, 10, 100)
	if got := c.Bytes(); got != 200 {
		t.Fatalf("bytes = %d, want 200", got)
	}
	if got := c.Stats().Bytes; got != 200 {
		t.Fatalf("stats bytes = %d, want 200", got)
	}
}
