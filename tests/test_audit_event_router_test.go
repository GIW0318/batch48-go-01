package task

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-d-03"
)

func openEventChannel(t *testing.T, r *impl.AuditRouter, name string, capacity int) {
	t.Helper()
	if err := r.OpenChannel(name, capacity); err != nil {
		t.Fatalf("open channel %s: %v", name, err)
	}
}

func TestSubmitRejectsEmptyEventID(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 4)
	_, err := r.Submit(impl.RawEvent{Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
	if !errors.Is(err, impl.ErrInvalidEvent) {
		t.Fatalf("empty event id error = %v, want %v", err, impl.ErrInvalidEvent)
	}
}

func TestSubmitRejectsUnknownChannel(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	_, err := r.Submit(impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "ghost", Severity: impl.SeverityInfo})
	if !errors.Is(err, impl.ErrUnknownChannel) {
		t.Fatalf("unknown channel error = %v, want %v", err, impl.ErrUnknownChannel)
	}
}

func TestSubmitDeduplicatesEventID(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	evt := impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo, Fields: map[string]string{"actor": "u1"}}
	first, err := r.Submit(evt)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if !first.Accepted || first.Sequence != 1 {
		t.Fatalf("first submit = %+v, want accepted at sequence 1", first)
	}
	second, err := r.Submit(evt)
	if err != nil {
		t.Fatalf("duplicate submit: %v", err)
	}
	if !second.Duplicate || second.Accepted {
		t.Fatalf("duplicate submit = %+v, want duplicate and not accepted", second)
	}
	if second.Sequence != first.Sequence {
		t.Fatalf("duplicate sequence = %d, want %d", second.Sequence, first.Sequence)
	}
	if got := r.Stats().Accepted; got != 1 {
		t.Fatalf("accepted counter = %d, want 1", got)
	}
}

func TestSubmitAssignsPerChannelSequence(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	openEventChannel(t, r, "alerts", 8)
	for i, want := range []uint64{1, 2, 3} {
		res, err := r.Submit(impl.RawEvent{EventID: "e-" + strconv.Itoa(i), Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if res.Sequence != want {
			t.Fatalf("events sequence %d = %d, want %d", i, res.Sequence, want)
		}
	}
	res, err := r.Submit(impl.RawEvent{EventID: "a-1", Tenant: "acme", Channel: "alerts", Severity: impl.SeverityWarn})
	if err != nil {
		t.Fatalf("alerts submit: %v", err)
	}
	if res.Sequence != 1 {
		t.Fatalf("alerts first sequence = %d, want 1", res.Sequence)
	}
}

func TestSubmitDropsWhenQueueFull(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 2)
	for i := 0; i < 2; i++ {
		if _, err := r.Submit(impl.RawEvent{EventID: "e-" + strconv.Itoa(i), Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	res, err := r.Submit(impl.RawEvent{EventID: "e-3", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
	if err != nil {
		t.Fatalf("full queue submit: %v", err)
	}
	if !res.Dropped || res.Accepted {
		t.Fatalf("full queue submit = %+v, want dropped and not accepted", res)
	}
	if got := r.Stats().Dropped; got != 1 {
		t.Fatalf("dropped counter = %d, want 1", got)
	}
	drained, err := r.DrainChannel("events")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(drained) != 2 {
		t.Fatalf("drained events = %d, want 2", len(drained))
	}
	pending, err := r.Pending("events")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("pending after drain = %d, want 0", pending)
	}
}

func TestProjectRedactsSensitiveFields(t *testing.T) {
	r := impl.NewAuditRouter([]string{"ssn"})
	openEventChannel(t, r, "events", 8)
	evt := impl.RawEvent{
		EventID:  "e-1",
		Tenant:   "acme",
		Channel:  "events",
		Severity: impl.SeverityInfo,
		Fields:   map[string]string{"token": "abc", "password": "pw", "ssn": "123-45-6789", "note": "login"},
	}
	got, err := r.Project(evt)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	for _, key := range []string{"token", "password", "ssn"} {
		if got.Fields[key] != "[redacted]" {
			t.Fatalf("field %s = %q, want %q", key, got.Fields[key], "[redacted]")
		}
	}
	if got.Fields["note"] != "login" {
		t.Fatalf("benign field = %q, want %q", got.Fields["note"], "login")
	}
}

func TestProjectPromotesSeverityToFloor(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	if err := r.SetSeverityFloor("events", impl.SeverityWarn); err != nil {
		t.Fatalf("set severity floor: %v", err)
	}
	got, err := r.Project(impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if got.Severity != impl.SeverityWarn {
		t.Fatalf("projected severity = %d, want %d", got.Severity, impl.SeverityWarn)
	}
}

func TestProjectRejectsInvalidSeverity(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	_, err := r.Project(impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "events", Severity: impl.Severity(9)})
	if !errors.Is(err, impl.ErrInvalidSeverity) {
		t.Fatalf("invalid severity error = %v, want %v", err, impl.ErrInvalidSeverity)
	}
}

func TestProjectDoesNotMutateInputFields(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	fields := map[string]string{"token": "abc", "note": "login"}
	evt := impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo, Fields: fields}
	if _, err := r.Project(evt); err != nil {
		t.Fatalf("project: %v", err)
	}
	if fields["token"] != "abc" {
		t.Fatalf("input token field mutated to %q", fields["token"])
	}
}

func TestDrainChannelReturnsSequenceOrder(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	for i := 0; i < 3; i++ {
		if _, err := r.Submit(impl.RawEvent{EventID: "e-" + strconv.Itoa(i), Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	drained, err := r.DrainChannel("events")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(drained) != 3 {
		t.Fatalf("drained events = %d, want 3", len(drained))
	}
	for i, want := range []uint64{1, 2, 3} {
		if drained[i].Sequence != want {
			t.Fatalf("drained event %d sequence = %d, want %d", i, drained[i].Sequence, want)
		}
	}
}

func TestDrainChannelEmptiesQueue(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	if _, err := r.Submit(impl.RawEvent{EventID: "e-1", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := r.DrainChannel("events"); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	second, err := r.DrainChannel("events")
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second drain returned %d events, want 0", len(second))
	}
}

func TestSnapshotOmitsEventBodies(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	for i := 0; i < 2; i++ {
		if _, err := r.Submit(impl.RawEvent{EventID: "e-" + strconv.Itoa(i), Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap.Channels) != 1 {
		t.Fatalf("snapshot channels = %d, want 1", len(snap.Channels))
	}
	if snap.Channels[0].Pending != 2 {
		t.Fatalf("snapshot pending = %d, want 2", snap.Channels[0].Pending)
	}
	restored := impl.NewAuditRouter([]string{"ssn"})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	pending, err := restored.Pending("events")
	if err != nil {
		t.Fatalf("restored pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("restored pending = %d, want 0 because bodies are not snapshotted", pending)
	}
	drained, err := restored.DrainChannel("events")
	if err != nil {
		t.Fatalf("restored drain: %v", err)
	}
	if len(drained) != 0 {
		t.Fatalf("restored drain returned %d events, want 0", len(drained))
	}
	if got := restored.Stats().Accepted; got != 2 {
		t.Fatalf("restored accepted counter = %d, want 2", got)
	}
}

func TestRestoreKeepsSequenceContinuity(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	for i := 0; i < 2; i++ {
		if _, err := r.Submit(impl.RawEvent{EventID: "e-" + strconv.Itoa(i), Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewAuditRouter(nil)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	res, err := restored.Submit(impl.RawEvent{EventID: "e-3", Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
	if err != nil {
		t.Fatalf("submit after restore: %v", err)
	}
	if res.Sequence != 3 {
		t.Fatalf("sequence after restore = %d, want 3", res.Sequence)
	}
}

func TestRestoreRejectsEmptySnapshot(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 8)
	if err := r.Restore(impl.RouterSnapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty snapshot error = %v, want %v", err, impl.ErrBadSnapshot)
	}
	if got := len(r.Channels()); got != 1 {
		t.Fatalf("channels after rejected restore = %d, want 1", got)
	}
}

func TestConcurrentSubmitKeepsSequenceUnique(t *testing.T) {
	r := impl.NewAuditRouter(nil)
	openEventChannel(t, r, "events", 100)
	const events = 50
	var wg sync.WaitGroup
	seqs := make(chan uint64, events)
	for i := 0; i < events; i++ {
		id := "e-" + strconv.Itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Submit(impl.RawEvent{EventID: id, Tenant: "acme", Channel: "events", Severity: impl.SeverityInfo})
			if err != nil || !res.Accepted {
				return
			}
			seqs <- res.Sequence
		}()
	}
	wg.Wait()
	close(seqs)
	seen := make(map[uint64]bool)
	for seq := range seqs {
		if seen[seq] {
			t.Fatalf("duplicate sequence %d under concurrent submit", seq)
		}
		seen[seq] = true
	}
	if len(seen) != events {
		t.Fatalf("accepted sequences = %d, want %d", len(seen), events)
	}
	pending, err := r.Pending("events")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending != events {
		t.Fatalf("pending after concurrent submit = %d, want %d", pending, events)
	}
}
