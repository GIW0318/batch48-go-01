package task

import (
	"errors"
	"sort"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-04"
)

func ledgerConfig() impl.LedgerConfig {
	return impl.LedgerConfig{MaxEvents: 64, MaxNodes: 8, MaxRetries: 3, MaxBackfill: 256}
}

func event(kind, subject, reason string) impl.RevocationEvent {
	return impl.RevocationEvent{Kind: kind, Subject: subject, Reason: reason}
}

func fiveEvents() []impl.RevocationEvent {
	return []impl.RevocationEvent{
		event(impl.KindCredential, "user-1/cert", "stolen"),
		event(impl.KindSession, "sess-9", "logout"),
		event(impl.KindCertificate, "cert-7", "expired"),
		event(impl.KindCredential, "user-2/cert", "rotated"),
		event(impl.KindSession, "sess-12", "replayed"),
	}
}

func ackThrough(t *testing.T, l *impl.RevocationFanoutLedger, node string, upTo uint64) {
	t.Helper()
	start := uint64(0)
	if wm, ok := l.Watermark(node); ok {
		start = wm
	}
	for seq := start + 1; seq <= upTo; seq++ {
		if err := l.Ack(node, seq); err != nil {
			t.Fatalf("ack %s seq %d: %v", node, seq, err)
		}
	}
}

func TestAppendAssignsConsecutiveSeq(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	last, err := l.Append(fiveEvents())
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if last != 5 {
		t.Fatalf("append returned last seq %d, want 5", last)
	}
	if got := l.Latest(); got != 5 {
		t.Fatalf("latest seq = %d, want 5", got)
	}
	for seq := uint64(1); seq <= 5; seq++ {
		stored, ok := l.Event(seq)
		if !ok {
			t.Fatalf("event %d must be retained", seq)
		}
		if stored.Seq != seq {
			t.Fatalf("event %d carries seq %d", seq, stored.Seq)
		}
	}
	first, _ := l.Event(1)
	if first.Kind != impl.KindCredential || first.Subject != "user-1/cert" || first.Reason != "stolen" {
		t.Fatalf("event 1 payload mismatch: %+v", first)
	}
	if _, ok := l.Event(6); ok {
		t.Fatalf("seq 6 was never assigned")
	}
}

func TestAppendRejectsEmptyAndInvalidAtomically(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(nil); !errors.Is(err, impl.ErrEmptyBatch) {
		t.Fatalf("empty batch must fail with ErrEmptyBatch, got %v", err)
	}
	if _, err := l.Append([]impl.RevocationEvent{
		event(impl.KindCredential, "user-1/cert", "stolen"),
		event(impl.KindCredential, "  ", "stolen"),
	}); !errors.Is(err, impl.ErrInvalidEvent) {
		t.Fatalf("an event without a subject must fail with ErrInvalidEvent, got %v", err)
	}
	if got := l.Latest(); got != 0 {
		t.Fatalf("a rejected batch consumed sequence numbers, latest = %d", got)
	}
	last, err := l.Append(fiveEvents()[:2])
	if err != nil {
		t.Fatalf("append after a rejected batch: %v", err)
	}
	if last != 2 {
		t.Fatalf("first accepted batch ends at %d, want 2", last)
	}
}

func TestRegisterNodeRejectsDuplicateAndCap(t *testing.T) {
	cfg := ledgerConfig()
	cfg.MaxNodes = 2
	l := impl.NewRevocationFanoutLedger(cfg)
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	if err := l.RegisterNode("node-a"); !errors.Is(err, impl.ErrNodeExists) {
		t.Fatalf("duplicate node must fail with ErrNodeExists, got %v", err)
	}
	if err := l.RegisterNode("  "); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank node id must fail with ErrBlankArg, got %v", err)
	}
	if err := l.RegisterNode("node-b"); err != nil {
		t.Fatalf("second node: %v", err)
	}
	if err := l.RegisterNode("node-c"); !errors.Is(err, impl.ErrNodeLimit) {
		t.Fatalf("third node must hit the node cap, got %v", err)
	}
}

func TestAckAdvancesWatermarkMonotonically(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	for _, seq := range []uint64{1, 2, 3} {
		if err := l.Ack("node-a", seq); err != nil {
			t.Fatalf("ack %d: %v", seq, err)
		}
		if got, ok := l.Watermark("node-a"); !ok || got != seq {
			t.Fatalf("watermark after ack %d = %d ok=%v", seq, got, ok)
		}
	}
	if err := l.Ack("node-a", 2); err != nil {
		t.Fatalf("re-acking an old seq must be idempotent, got %v", err)
	}
	if got, _ := l.Watermark("node-a"); got != 3 {
		t.Fatalf("watermark must never move backwards, got %d", got)
	}
}

func TestAckUnknownNodeFails(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.Ack("node-404", 1); !errors.Is(err, impl.ErrUnknownNode) {
		t.Fatalf("ack for an unknown node must fail with ErrUnknownNode, got %v", err)
	}
	if _, ok := l.Watermark("node-404"); ok {
		t.Fatalf("an unknown node must not report a watermark")
	}
	if _, err := l.Pending("node-404"); !errors.Is(err, impl.ErrUnknownNode) {
		t.Fatalf("pending for an unknown node must fail with ErrUnknownNode, got %v", err)
	}
	if _, err := l.Backfill("node-404", 3); !errors.Is(err, impl.ErrUnknownNode) {
		t.Fatalf("backfill for an unknown node must fail with ErrUnknownNode, got %v", err)
	}
}

func TestPendingListsMissingSeqs(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	ackThrough(t, l, "node-a", 2)
	pending, err := l.Pending("node-a")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 3 || pending[0] != 3 || pending[1] != 4 || pending[2] != 5 {
		t.Fatalf("pending = %v, want [3 4 5]", pending)
	}
	ackThrough(t, l, "node-a", 5)
	caughtUp, err := l.Pending("node-a")
	if err != nil {
		t.Fatalf("pending after catch up: %v", err)
	}
	if len(caughtUp) != 0 {
		t.Fatalf("pending after catch up = %v, want empty", caughtUp)
	}
}

func TestBackfillCountsFromWatermark(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	ackThrough(t, l, "node-a", 2)
	count, err := l.Backfill("node-a", 4)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if count != 2 {
		t.Fatalf("backfill count = %d, want 2 events (3 and 4)", count)
	}
	if got, _ := l.Watermark("node-a"); got != 2 {
		t.Fatalf("backfill must not move the watermark, got %d", got)
	}
}

func TestBackfillClampsToLatest(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	if err := l.Ack("node-a", 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	count, err := l.Backfill("node-a", 99)
	if err != nil {
		t.Fatalf("backfill past the head: %v", err)
	}
	if count != 4 {
		t.Fatalf("backfill count = %d, want the remaining 4 events", count)
	}
	behind, err := l.Backfill("node-a", 1)
	if err != nil {
		t.Fatalf("backfill with nothing to send: %v", err)
	}
	if behind != 0 {
		t.Fatalf("backfill below the watermark sent %d events, want 0", behind)
	}
}

func TestBackfillRetryBudgetDegradesNode(t *testing.T) {
	cfg := ledgerConfig()
	cfg.MaxRetries = 2
	l := impl.NewRevocationFanoutLedger(cfg)
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	for i := 0; i < cfg.MaxRetries; i++ {
		if _, err := l.Backfill("node-a", 5); err != nil {
			t.Fatalf("backfill attempt %d: %v", i, err)
		}
	}
	if _, err := l.Backfill("node-a", 5); !errors.Is(err, impl.ErrNodeDegraded) {
		t.Fatalf("backfill past the retry budget must fail with ErrNodeDegraded, got %v", err)
	}
	status, ok := l.NodeStatus("node-a")
	if !ok {
		t.Fatalf("node status must stay readable")
	}
	if status.State != impl.NodeDegraded {
		t.Fatalf("node state = %q, want %q", status.State, impl.NodeDegraded)
	}
	if status.Retries <= cfg.MaxRetries {
		t.Fatalf("node retries = %d, want more than the budget %d", status.Retries, cfg.MaxRetries)
	}
}

func TestDegradedNodeRejectsBackfillUntilReinstated(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	if err := l.Degrade("node-a", "link down"); err != nil {
		t.Fatalf("degrade: %v", err)
	}
	if _, err := l.Backfill("node-a", 5); !errors.Is(err, impl.ErrNodeDegraded) {
		t.Fatalf("degraded node must reject backfill with ErrNodeDegraded, got %v", err)
	}
	if err := l.Reinstate("node-a"); err != nil {
		t.Fatalf("reinstate: %v", err)
	}
	status, _ := l.NodeStatus("node-a")
	if status.State != impl.NodeActive {
		t.Fatalf("node state after reinstate = %q, want %q", status.State, impl.NodeActive)
	}
	count, err := l.Backfill("node-a", 5)
	if err != nil {
		t.Fatalf("backfill after reinstate: %v", err)
	}
	if count != 5 {
		t.Fatalf("backfill after reinstate sent %d events, want 5", count)
	}
}

func TestEventWindowTrimsOldest(t *testing.T) {
	cfg := ledgerConfig()
	cfg.MaxEvents = 3
	l := impl.NewRevocationFanoutLedger(cfg)
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := l.Latest(); got != 5 {
		t.Fatalf("latest = %d, want 5 even after trimming", got)
	}
	if _, ok := l.Event(2); ok {
		t.Fatalf("event 2 must be outside the retention window")
	}
	for seq := uint64(3); seq <= 5; seq++ {
		if _, ok := l.Event(seq); !ok {
			t.Fatalf("event %d must stay inside the retention window", seq)
		}
	}
	if _, err := l.Append(fiveEvents()[:1]); err != nil {
		t.Fatalf("append after trimming: %v", err)
	}
	if _, ok := l.Event(3); ok {
		t.Fatalf("event 3 must be trimmed once a newer event arrives")
	}
}

func TestConcurrentAppendAssignsUniqueSeqs(t *testing.T) {
	cfg := ledgerConfig()
	cfg.MaxEvents = 64
	l := impl.NewRevocationFanoutLedger(cfg)
	var wg sync.WaitGroup
	var mu sync.Mutex
	lastSeqs := make([]uint64, 0, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			batch := []impl.RevocationEvent{
				event(impl.KindCredential, "user-a/cert", "batch"),
				event(impl.KindSession, "sess-a", "batch"),
				event(impl.KindCertificate, "cert-a", "batch"),
				event(impl.KindCredential, "user-b/cert", "batch"),
			}
			last, err := l.Append(batch)
			if err != nil {
				t.Errorf("concurrent append: %v", err)
				return
			}
			mu.Lock()
			lastSeqs = append(lastSeqs, last)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(lastSeqs) != 8 {
		t.Fatalf("concurrent appends recorded %d results, want 8", len(lastSeqs))
	}
	sort.Slice(lastSeqs, func(i, j int) bool { return lastSeqs[i] < lastSeqs[j] })
	for i, want := range []uint64{4, 8, 12, 16, 20, 24, 28, 32} {
		if lastSeqs[i] != want {
			t.Fatalf("sorted batch ends = %v, want every fourth sequence up to 32", lastSeqs)
		}
	}
	if got := l.Latest(); got != 32 {
		t.Fatalf("latest after the race = %d, want 32", got)
	}
	for seq := uint64(1); seq <= 32; seq++ {
		if _, ok := l.Event(seq); !ok {
			t.Fatalf("seq %d is missing after the race", seq)
		}
	}
}

func TestSnapshotRestoreKeepsWatermarksAndRetries(t *testing.T) {
	cfg := ledgerConfig()
	l := impl.NewRevocationFanoutLedger(cfg)
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	for _, node := range []string{"node-a", "node-b"} {
		if err := l.RegisterNode(node); err != nil {
			t.Fatalf("register %s: %v", node, err)
		}
	}
	ackThrough(t, l, "node-a", 3)
	for i := 0; i < 2; i++ {
		if _, err := l.Backfill("node-b", 5); err != nil {
			t.Fatalf("backfill attempt %d: %v", i, err)
		}
	}
	data, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewRevocationFanoutLedger(cfg)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Latest(); got != 5 {
		t.Fatalf("restored latest = %d, want 5", got)
	}
	if got, ok := restored.Watermark("node-a"); !ok || got != 3 {
		t.Fatalf("restored watermark = %d ok=%v, want 3", got, ok)
	}
	status, ok := restored.NodeStatus("node-b")
	if !ok {
		t.Fatalf("restored node-b is missing")
	}
	if status.Retries != 2 {
		t.Fatalf("restored retry counter = %d, want 2", status.Retries)
	}
	if err := restored.Ack("node-a", 4); err != nil {
		t.Fatalf("ack after restore: %v", err)
	}
	pending, err := restored.Pending("node-b")
	if err != nil {
		t.Fatalf("pending after restore: %v", err)
	}
	if len(pending) != 5 {
		t.Fatalf("pending after restore = %v, want all 5 events", pending)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	if err := l.Restore([]byte(`{"version":1,"latest":"nan"}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	if got := l.Latest(); got != 5 {
		t.Fatalf("a rejected restore must not touch live state, latest = %d", got)
	}
	if err := l.Ack("node-a", 1); err != nil {
		t.Fatalf("live ledger must keep working after a rejected restore, got %v", err)
	}
	last, err := l.Append(fiveEvents()[:1])
	if err != nil {
		t.Fatalf("append after a rejected restore: %v", err)
	}
	if last != 6 {
		t.Fatalf("sequence must continue at 6, got %d", last)
	}
}

func TestNodeStatusReportsLag(t *testing.T) {
	l := impl.NewRevocationFanoutLedger(ledgerConfig())
	if _, err := l.Append(fiveEvents()); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.RegisterNode("node-a"); err != nil {
		t.Fatalf("register node: %v", err)
	}
	ackThrough(t, l, "node-a", 2)
	status, ok := l.NodeStatus("node-a")
	if !ok {
		t.Fatalf("node status must be readable")
	}
	if status.NodeID != "node-a" || status.Watermark != 2 || status.Latest != 5 {
		t.Fatalf("node status = %+v, want node-a watermark 2 latest 5", status)
	}
	if status.Lag != 3 {
		t.Fatalf("node lag = %d, want 3", status.Lag)
	}
	if status.State != impl.NodeActive {
		t.Fatalf("node state = %q, want %q", status.State, impl.NodeActive)
	}
	if _, ok := l.NodeStatus("node-404"); ok {
		t.Fatalf("an unknown node must not report a status")
	}
}
