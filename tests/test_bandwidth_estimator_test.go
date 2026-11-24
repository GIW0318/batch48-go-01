package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-c-03"
)

func c03Bounds() impl.Bounds {
	return impl.Bounds{
		MinBitrate:        300000,
		MaxBitrate:        2000000,
		StartBitrate:      800000,
		IncreaseStep:      40000,
		DecreaseFactor:    0.7,
		GradientThreshold: 8,
		OverloadHoldMs:    300,
		LossThreshold:     0.1,
		CooldownMs:        1000,
		WindowSize:        5,
	}
}

func c03Sample(id uint64, send, arrival int64, received, lost int) impl.Sample {
	return impl.Sample{GroupID: id, SendMs: send, ArrivalMs: arrival, Bytes: 1200, Received: received, Lost: lost}
}

func c03New(t *testing.T, b impl.Bounds) *impl.BandwidthEstimator {
	t.Helper()
	e, err := impl.NewBandwidthEstimator(b)
	if err != nil {
		t.Fatalf("new estimator: %v", err)
	}
	return e
}

func TestNewEstimatorValidatesBounds(t *testing.T) {
	cases := []struct {
		name  string
		mutate func(*impl.Bounds)
	}{
		{"max below min", func(b *impl.Bounds) { b.MaxBitrate = 100000 }},
		{"start above max", func(b *impl.Bounds) { b.StartBitrate = 2500000 }},
		{"zero increase", func(b *impl.Bounds) { b.IncreaseStep = 0 }},
		{"decrease factor above one", func(b *impl.Bounds) { b.DecreaseFactor = 1.5 }},
		{"zero window", func(b *impl.Bounds) { b.WindowSize = 0 }},
	}
	for _, c := range cases {
		b := c03Bounds()
		c.mutate(&b)
		if _, err := impl.NewBandwidthEstimator(b); !errors.Is(err, impl.ErrBoundsInvalid) {
			t.Fatalf("%s error = %v, want ErrBoundsInvalid", c.name, err)
		}
	}
}

func TestAddSampleRejectsMalformedSamples(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(impl.Sample{GroupID: 1, SendMs: 1000, ArrivalMs: 1000, Bytes: 1200, Received: 10}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("equal send and arrival error = %v, want ErrSampleRejected", err)
	}
	if err := e.AddSample(impl.Sample{GroupID: 2, SendMs: 0, ArrivalMs: 1000, Bytes: 0, Received: 10}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("zero bytes error = %v, want ErrSampleRejected", err)
	}
	if err := e.AddSample(impl.Sample{GroupID: 3, SendMs: 0, ArrivalMs: 1000, Bytes: 1200}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("empty group error = %v, want ErrSampleRejected", err)
	}
	if err := e.AddSample(impl.Sample{GroupID: 4, SendMs: 0, ArrivalMs: 1000, Bytes: 1200, Received: 10, Lost: -5}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("negative loss error = %v, want ErrSampleRejected", err)
	}
	if got := e.Stats().BadSamples; got != 4 {
		t.Fatalf("bad samples = %d, want 4", got)
	}
	if got := e.Stats().Samples; got != 0 {
		t.Fatalf("accepted samples = %d, want none", got)
	}
}

func TestDuplicateGroupIsCountedOnce(t *testing.T) {
	e := c03New(t, c03Bounds())
	s := c03Sample(1, 0, 1000, 10, 0)
	if err := e.AddSample(s); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := e.AddSample(s); err != nil {
		t.Fatalf("duplicate sample: %v", err)
	}
	st := e.Stats()
	if st.Samples != 1 {
		t.Fatalf("accepted samples = %d, want the duplicate folded", st.Samples)
	}
	if st.Duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1", st.Duplicates)
	}
}

func TestSustainedUnderuseIncreasesTarget(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(c03Sample(1, 0, 1000, 10, 0)); err != nil {
		t.Fatalf("baseline sample: %v", err)
	}
	if err := e.AddSample(c03Sample(2, 400, 1300, 10, 0)); err != nil {
		t.Fatalf("underuse sample: %v", err)
	}
	if got := e.Target(); got != 800000 {
		t.Fatalf("target = %d, want the start bitrate while the underuse is short", got)
	}
	if err := e.AddSample(c03Sample(3, 800, 1600, 10, 0)); err != nil {
		t.Fatalf("sustained underuse sample: %v", err)
	}
	if got := e.Target(); got != 840000 {
		t.Fatalf("target = %d, want one increase step of 40000", got)
	}
	if got := e.State(); got != impl.StateIncrease {
		t.Fatalf("state = %q, want increase", got)
	}
	st := e.Stats()
	if st.Increases != 1 || st.StateSwitches != 1 {
		t.Fatalf("increases=%d switches=%d, want 1 and 1", st.Increases, st.StateSwitches)
	}
	if st.Underuse == 0 {
		t.Fatalf("underuse samples were not counted")
	}
}

func TestSingleDelaySpikeDoesNotSwitch(t *testing.T) {
	e := c03New(t, c03Bounds())
	seq := []impl.Sample{
		c03Sample(1, 0, 1000, 10, 0),
		c03Sample(2, 400, 2300, 10, 0),
		c03Sample(3, 800, 2350, 10, 0),
		c03Sample(4, 1200, 2400, 10, 0),
	}
	for i, s := range seq {
		if err := e.AddSample(s); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	if got := e.Target(); got != 800000 {
		t.Fatalf("target = %d, want the start bitrate after a single spike", got)
	}
	if got := e.State(); got != impl.StateHold {
		t.Fatalf("state = %q, want hold for a spike shorter than the hold window", got)
	}
	st := e.Stats()
	if st.Decreases != 0 || st.Increases != 0 {
		t.Fatalf("increases=%d decreases=%d, want no switch", st.Increases, st.Decreases)
	}
}

func TestSustainedOverloadDecreasesTarget(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(c03Sample(1, 0, 1000, 10, 0)); err != nil {
		t.Fatalf("baseline sample: %v", err)
	}
	if err := e.AddSample(c03Sample(2, 400, 2300, 10, 0)); err != nil {
		t.Fatalf("overload sample: %v", err)
	}
	if got := e.Target(); got != 800000 {
		t.Fatalf("target = %d, want no change before the hold window elapsed", got)
	}
	if err := e.AddSample(c03Sample(3, 800, 3600, 10, 0)); err != nil {
		t.Fatalf("sustained overload sample: %v", err)
	}
	if got := e.Target(); got != 560000 {
		t.Fatalf("target = %d, want the target multiplied by the decrease factor", got)
	}
	if got := e.State(); got != impl.StateDecrease {
		t.Fatalf("state = %q, want decrease", got)
	}
	if got := e.Stats().Decreases; got != 1 {
		t.Fatalf("decreases = %d, want 1", got)
	}
}

func TestIncreaseIsSlowerThanDecrease(t *testing.T) {
	up := c03New(t, c03Bounds())
	for _, s := range []impl.Sample{
		c03Sample(1, 0, 1000, 10, 0),
		c03Sample(2, 400, 1300, 10, 0),
		c03Sample(3, 800, 1600, 10, 0),
	} {
		if err := up.AddSample(s); err != nil {
			t.Fatalf("increase sample: %v", err)
		}
	}
	down := c03New(t, c03Bounds())
	for _, s := range []impl.Sample{
		c03Sample(1, 0, 1000, 10, 0),
		c03Sample(2, 400, 2300, 10, 0),
		c03Sample(3, 800, 3600, 10, 0),
	} {
		if err := down.AddSample(s); err != nil {
			t.Fatalf("decrease sample: %v", err)
		}
	}
	rise := up.Target() - 800000
	fall := 800000 - down.Target()
	if rise <= 0 || fall <= 0 {
		t.Fatalf("rise=%d fall=%d, want both directions to move the target", rise, fall)
	}
	if rise >= fall {
		t.Fatalf("rise=%d is not slower than fall=%d", rise, fall)
	}
}

func TestCooldownBlocksThenAllowsSwitch(t *testing.T) {
	e := c03New(t, c03Bounds())
	for _, s := range []impl.Sample{
		c03Sample(1, 0, 1000, 10, 0),
		c03Sample(2, 400, 1300, 10, 0),
		c03Sample(3, 800, 1600, 10, 0),
	} {
		if err := e.AddSample(s); err != nil {
			t.Fatalf("warm up sample: %v", err)
		}
	}
	if got := e.Target(); got != 840000 {
		t.Fatalf("target = %d, want the first increase applied", got)
	}
	if err := e.AddSample(c03Sample(4, 1200, 2000, 10, 0)); err != nil {
		t.Fatalf("sample inside the cooldown: %v", err)
	}
	if got := e.Target(); got != 840000 {
		t.Fatalf("target = %d, want no switch while the cooldown is running", got)
	}
	if got := e.CooldownRemaining(); got != 600 {
		t.Fatalf("cooldown remaining = %d, want 600 after 400ms elapsed", got)
	}
	if got := e.Stats().CooldownBlocks; got != 1 {
		t.Fatalf("cooldown blocks = %d, want 1", got)
	}
	if err := e.AddSample(c03Sample(5, 2800, 2700, 10, 0)); err != nil {
		t.Fatalf("sample after the cooldown: %v", err)
	}
	if got := e.Target(); got != 880000 {
		t.Fatalf("target = %d, want the next increase once the cooldown expired", got)
	}
}

func TestEnterCooldownCountsDown(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(c03Sample(1, 0, 1000, 10, 0)); err != nil {
		t.Fatalf("baseline sample: %v", err)
	}
	if err := e.EnterCooldown(-1); !errors.Is(err, impl.ErrCooldownInvalid) {
		t.Fatalf("negative cooldown error = %v, want ErrCooldownInvalid", err)
	}
	if err := e.EnterCooldown(2000); err != nil {
		t.Fatalf("enter cooldown: %v", err)
	}
	if got := e.CooldownRemaining(); got != 2000 {
		t.Fatalf("cooldown remaining = %d, want the forced 2000", got)
	}
	if err := e.AddSample(c03Sample(2, 400, 1500, 10, 0)); err != nil {
		t.Fatalf("sample while cooling: %v", err)
	}
	if got := e.CooldownRemaining(); got != 1500 {
		t.Fatalf("cooldown remaining = %d, want the sample arrival to consume time", got)
	}
}

func TestLossGateBlocksIncrease(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(c03Sample(1, 0, 1000, 10, 0)); err != nil {
		t.Fatalf("baseline sample: %v", err)
	}
	if err := e.AddSample(c03Sample(2, 400, 1300, 5, 5)); err != nil {
		t.Fatalf("lossy sample: %v", err)
	}
	if err := e.AddSample(c03Sample(3, 800, 1600, 5, 5)); err != nil {
		t.Fatalf("sustained lossy sample: %v", err)
	}
	if got := e.Target(); got != 800000 {
		t.Fatalf("target = %d, want no increase while the loss rate is over the threshold", got)
	}
	if got := e.State(); got != impl.StateHold {
		t.Fatalf("state = %q, want hold", got)
	}
	if got := e.Stats().LossGated; got == 0 {
		t.Fatalf("loss gated counter was not raised")
	}
	if got := e.Stats().Increases; got != 0 {
		t.Fatalf("increases = %d, want 0", got)
	}
}

func TestTargetNeverFallsBelowFloor(t *testing.T) {
	e := c03New(t, c03Bounds())
	seq := []impl.Sample{
		c03Sample(1, 0, 1000, 10, 0),
		c03Sample(2, 400, 2300, 10, 0),
		c03Sample(3, 800, 3600, 10, 0),
		c03Sample(4, 1200, 4400, 10, 0),
		c03Sample(5, 1600, 4600, 10, 0),
		c03Sample(6, 2000, 4900, 10, 0),
		c03Sample(7, 2400, 5600, 10, 0),
	}
	for i, s := range seq {
		if err := e.AddSample(s); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	if got := e.Target(); got != 300000 {
		t.Fatalf("target = %d, want the floor applied after repeated decreases", got)
	}
	if got := e.Stats().Decreases; got != 3 {
		t.Fatalf("decreases = %d, want 3", got)
	}
}

func TestSetBoundsClampsTarget(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.SetBounds(300000, 500000); err != nil {
		t.Fatalf("set lower ceiling: %v", err)
	}
	if got := e.Target(); got != 500000 {
		t.Fatalf("target = %d, want the new ceiling applied immediately", got)
	}
	if err := e.SetBounds(0, 500000); !errors.Is(err, impl.ErrBoundsInvalid) {
		t.Fatalf("zero minimum error = %v, want ErrBoundsInvalid", err)
	}
	if err := e.SetBounds(600000, 500000); !errors.Is(err, impl.ErrBoundsInvalid) {
		t.Fatalf("inverted range error = %v, want ErrBoundsInvalid", err)
	}
	if got := e.Target(); got != 500000 {
		t.Fatalf("target = %d, want unchanged after rejected bounds", got)
	}
}

func TestEstimatorStartsHolding(t *testing.T) {
	e := c03New(t, c03Bounds())
	if got := e.Target(); got != 800000 {
		t.Fatalf("initial target = %d, want the configured start bitrate", got)
	}
	if got := e.State(); got != impl.StateHold {
		t.Fatalf("initial state = %q, want hold", got)
	}
	if got := e.CooldownRemaining(); got != 0 {
		t.Fatalf("initial cooldown = %d, want 0", got)
	}
	if got := e.Stats().Samples; got != 0 {
		t.Fatalf("initial samples = %d, want 0", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	e := c03New(t, c03Bounds())
	if err := e.AddSample(c03Sample(1, 0, 1000, 10, 0)); err != nil {
		t.Fatalf("baseline sample: %v", err)
	}
	if err := e.AddSample(c03Sample(2, 400, 1300, 10, 0)); err != nil {
		t.Fatalf("underuse sample: %v", err)
	}
	payload, err := e.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	h := c03New(t, c03Bounds())
	if err := h.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if h.Target() != e.Target() || h.State() != e.State() {
		t.Fatalf("restored target/state = %d/%q, want %d/%q", h.Target(), h.State(), e.Target(), e.State())
	}
	if h.CooldownRemaining() != e.CooldownRemaining() {
		t.Fatalf("restored cooldown = %d, want %d", h.CooldownRemaining(), e.CooldownRemaining())
	}
	if h.Stats().Samples != e.Stats().Samples {
		t.Fatalf("restored samples = %d, want %d", h.Stats().Samples, e.Stats().Samples)
	}
	next := c03Sample(3, 800, 1600, 10, 0)
	if err := h.AddSample(next); err != nil {
		t.Fatalf("post restore sample: %v", err)
	}
	if err := e.AddSample(next); err != nil {
		t.Fatalf("original sample: %v", err)
	}
	if h.Target() != 840000 || e.Target() != 840000 {
		t.Fatalf("targets after restore = %d and %d, want both 840000", h.Target(), e.Target())
	}
	if err := h.Restore([]byte("junk")); !errors.Is(err, impl.ErrStateCorrupt) {
		t.Fatalf("bad snapshot error = %v, want ErrStateCorrupt", err)
	}
	if got := h.Target(); got != 840000 {
		t.Fatalf("bad restore changed the target to %d", got)
	}
}

func TestConcurrentReadersSeeWholeWindow(t *testing.T) {
	e := c03New(t, c03Bounds())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = e.Target()
				_ = e.State()
				_ = e.Stats()
			}
		}()
	}
	for i := 1; i <= 20; i++ {
		s := c03Sample(uint64(i), int64(i)*400, int64(1000+i*400), 10, 0)
		if err := e.AddSample(s); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	wg.Wait()
	if got := e.Stats().Samples; got != 20 {
		t.Fatalf("samples = %d, want every sample counted while readers run", got)
	}
	if got := e.Target(); got != 800000 {
		t.Fatalf("target = %d, want the start bitrate for a flat gradient", got)
	}
}
