package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-b-24"
)

func TestNormalizeKeyTrimsAndLowercases(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if got := c.NormalizeKey("  UI/Hero.PNG "); got != "ui/hero.png" {
		t.Fatalf("normalize %q = %q, want %q", "  UI/Hero.PNG ", got, "ui/hero.png")
	}
	if got := c.NormalizeKey("TEXTURE/A.PNG"); got != "texture/a.png" {
		t.Fatalf("normalize %q = %q, want %q", "TEXTURE/A.PNG", got, "texture/a.png")
	}
}

func TestRequestRejectsEmptyKey(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("   ", 0); !errors.Is(err, impl.ErrEmptyKey) {
		t.Fatalf("blank key: got %v, want ErrEmptyKey", err)
	}
	if _, err := c.Request("", 0); !errors.Is(err, impl.ErrEmptyKey) {
		t.Fatalf("empty key: got %v, want ErrEmptyKey", err)
	}
}

func TestRequestRejectsOutOfRangePriority(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/a.png", 100); !errors.Is(err, impl.ErrBadPriority) {
		t.Fatalf("priority 100: got %v, want ErrBadPriority", err)
	}
	if _, err := c.Request("ui/a.png", -1); !errors.Is(err, impl.ErrBadPriority) {
		t.Fatalf("priority -1: got %v, want ErrBadPriority", err)
	}
}

func TestFirstRequestStartsLoading(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	ticket, err := c.Request("  UI/Hero.PNG ", 3)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if ticket.Key != "ui/hero.png" {
		t.Fatalf("ticket key = %q, want the normalized %q", ticket.Key, "ui/hero.png")
	}
	if ticket.Seq == 0 {
		t.Fatalf("ticket seq must be assigned")
	}
	state, err := c.Status("ui/hero.png")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if state != impl.StateLoading {
		t.Fatalf("state = %v, want StateLoading", state)
	}
	if got := c.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want 1", got)
	}
	if got := c.RefCount("ui/hero.png"); got != 1 {
		t.Fatalf("ref count = %d, want 1", got)
	}
	if got := c.Priority("ui/hero.png"); got != 3 {
		t.Fatalf("priority = %d, want 3", got)
	}
}

func TestSameKeyRequestsShareOneLoad(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("UI/Hero.PNG", 1); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if _, err := c.Request(" ui/hero.png ", 1); err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	if got := c.RefCount("ui/hero.png"); got != 2 {
		t.Fatalf("ref count = %d, want 2 requesters", got)
	}
	if got := c.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want one coalesced load", got)
	}
	if got := c.Stats().Started; got != 1 {
		t.Fatalf("loads started = %d, want 1", got)
	}
}

func TestLaterRequestRaisesPriority(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/boss.png", 1); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if _, err := c.Request("ui/boss.png", 7); err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	if got := c.Priority("ui/boss.png"); got != 7 {
		t.Fatalf("priority = %d, want the raised 7", got)
	}
}

func TestLowerPriorityKeepsCurrent(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/boss.png", 7); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if _, err := c.Request("ui/boss.png", 2); err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	if got := c.Priority("ui/boss.png"); got != 7 {
		t.Fatalf("priority = %d, want the current 7", got)
	}
}

func TestCompletePublishesResult(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/hero.png", 4); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if _, err := c.Request("ui/hero.png", 4); err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	want := impl.Payload{Bytes: 4096, Format: "ktx2"}
	if err := c.Complete("UI/Hero.PNG", want, nil); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	state, err := c.Status("ui/hero.png")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if state != impl.StateReady {
		t.Fatalf("state = %v, want StateReady", state)
	}
	if got := c.Resident(); got != 1 {
		t.Fatalf("resident = %d, want the published result to stay resident", got)
	}
	if got := c.InFlight(); got != 0 {
		t.Fatalf("in flight = %d, want 0", got)
	}
	if got := c.RefCount("ui/hero.png"); got != 2 {
		t.Fatalf("ref count = %d, want the 2 requesters kept", got)
	}
	got, ok, err := c.Result("ui/hero.png")
	if err != nil {
		t.Fatalf("result failed: %v", err)
	}
	if !ok {
		t.Fatalf("result must be available once ready")
	}
	if got != want {
		t.Fatalf("payload = %+v, want %+v", got, want)
	}
	if n := c.Stats().Completed; n != 1 {
		t.Fatalf("completed counter = %d, want 1", n)
	}
}

func TestCompleteUnknownLoadRejected(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if err := c.Complete("ui/missing.png", impl.Payload{Bytes: 1}, nil); !errors.Is(err, impl.ErrNoLoad) {
		t.Fatalf("complete without a load: got %v, want ErrNoLoad", err)
	}
}

func TestCompleteFailurePublishesFailedState(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/broken.png", 0); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := c.Complete("ui/broken.png", impl.Payload{}, errors.New("decode failed")); err != nil {
		t.Fatalf("complete with a load error must be accepted: %v", err)
	}
	state, err := c.Status("ui/broken.png")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if state != impl.StateFailed {
		t.Fatalf("state = %v, want StateFailed", state)
	}
	if got := c.InFlight(); got != 0 {
		t.Fatalf("in flight = %d, want 0 after a failure", got)
	}
	if n := c.Stats().Failed; n != 1 {
		t.Fatalf("failed counter = %d, want 1", n)
	}
}

func TestRetryAfterFailureDoesNotStackInFlight(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/broken.png", 0); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := c.Complete("ui/broken.png", impl.Payload{}, errors.New("timeout")); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if _, err := c.Request("ui/broken.png", 2); err != nil {
		t.Fatalf("retry request failed: %v", err)
	}
	state, err := c.Status("ui/broken.png")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if state != impl.StateLoading {
		t.Fatalf("state after retry = %v, want StateLoading", state)
	}
	if got := c.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want 1 and not stacked", got)
	}
	if got := c.Stats().Started; got != 2 {
		t.Fatalf("loads started = %d, want 2", got)
	}
	if got := c.RefCount("ui/broken.png"); got != 1 {
		t.Fatalf("ref count = %d, want the retry to start from 1", got)
	}
}

func TestCancelLastRequesterCancelsLoad(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	ticket, err := c.Request("ui/only.png", 0)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := c.Cancel(ticket); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if got := c.InFlight(); got != 0 {
		t.Fatalf("in flight = %d, want the load cancelled", got)
	}
	if got := c.RefCount("ui/only.png"); got != 0 {
		t.Fatalf("ref count = %d, want 0", got)
	}
	if n := c.Stats().Cancelled; n != 1 {
		t.Fatalf("cancelled counter = %d, want 1", n)
	}
}

func TestCancelOneOfTwoKeepsLoadRunning(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/shared.png", 3); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	second, err := c.Request("ui/shared.png", 3)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	if err := c.Cancel(second); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	state, err := c.Status("ui/shared.png")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if state != impl.StateLoading {
		t.Fatalf("state = %v, want StateLoading with one requester left", state)
	}
	if got := c.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want 1", got)
	}
	if got := c.RefCount("ui/shared.png"); got != 1 {
		t.Fatalf("ref count = %d, want 1", got)
	}
}

func TestCancelUnknownTicketRejected(t *testing.T) {
	c := impl.NewLoadCoalescer(4)
	if _, err := c.Request("ui/a.png", 0); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := c.Cancel(impl.Ticket{}); !errors.Is(err, impl.ErrUnknownTicket) {
		t.Fatalf("zero ticket: got %v, want ErrUnknownTicket", err)
	}
	if err := c.Cancel(impl.Ticket{Key: "ui/ghost.png", Seq: 99}); !errors.Is(err, impl.ErrUnknownTicket) {
		t.Fatalf("foreign ticket: got %v, want ErrUnknownTicket", err)
	}
}

func TestKeyLimitRejectsNewKey(t *testing.T) {
	c := impl.NewLoadCoalescer(1)
	if _, err := c.Request("ui/a.png", 0); err != nil {
		t.Fatalf("first key failed: %v", err)
	}
	if _, err := c.Request("ui/b.png", 0); !errors.Is(err, impl.ErrKeyLimit) {
		t.Fatalf("second key above a limit of 1: got %v, want ErrKeyLimit", err)
	}
}
