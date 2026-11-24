package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-08"
)

func equalWeights() map[impl.Consumer]uint32 {
	return map[impl.Consumer]uint32{
		impl.ConsumerMain:       1,
		impl.ConsumerShadow:     1,
		impl.ConsumerReflection: 1,
	}
}

func newBroker(quota uint32) *impl.VisibilityConfidenceBroker {
	return impl.NewVisibilityConfidenceBroker(impl.Config{
		SampleQuota: quota,
		TTLFrames:   8,
		Low:         0.2,
		High:        0.7,
		Weights:     equalWeights(),
	})
}

func TestBeginFrameRejectsNonMonotonic(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(0); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("BeginFrame(0): got %v, want ErrFrameOrder", err)
	}
	if err := b.BeginFrame(5); err != nil {
		t.Fatalf("BeginFrame(5) returned unexpected error: %v", err)
	}
	if err := b.BeginFrame(5); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("repeated frame 5: got %v, want ErrFrameOrder", err)
	}
	if err := b.BeginFrame(4); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("backwards frame 4: got %v, want ErrFrameOrder", err)
	}
	if got := b.Frame(); got != 5 {
		t.Fatalf("Frame = %d, want 5", got)
	}
}

func TestSubmitRequiresOpenFrame(t *testing.T) {
	b := newBroker(30)
	if err := b.Submit(1, impl.ConsumerMain, 1, 2); !errors.Is(err, impl.ErrNoFrame) {
		t.Fatalf("Submit before BeginFrame: got %v, want ErrNoFrame", err)
	}
}

func TestSubmitRejectsUnknownConsumer(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(1, impl.Consumer("hud"), 1, 2); !errors.Is(err, impl.ErrUnknownConsumer) {
		t.Fatalf("Submit(hud): got %v, want ErrUnknownConsumer", err)
	}
}

func TestSubmitRejectsPassedAboveTotal(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(1, impl.ConsumerMain, 5, 3); !errors.Is(err, impl.ErrBadSamples) {
		t.Fatalf("Submit(5 of 3): got %v, want ErrBadSamples", err)
	}
	if got := b.SamplesLeft(impl.ConsumerMain); got != 10 {
		t.Fatalf("SamplesLeft after rejected Submit = %d, want 10", got)
	}
}

func TestSetThresholdsValidatesRange(t *testing.T) {
	b := newBroker(30)
	if err := b.SetThresholds(0.8, 0.4); !errors.Is(err, impl.ErrBadThresholds) {
		t.Fatalf("SetThresholds(0.8,0.4): got %v, want ErrBadThresholds", err)
	}
	if err := b.SetThresholds(-0.1, 0.5); !errors.Is(err, impl.ErrBadThresholds) {
		t.Fatalf("SetThresholds(-0.1,0.5): got %v, want ErrBadThresholds", err)
	}
	if err := b.SetThresholds(0.2, 1.5); !errors.Is(err, impl.ErrBadThresholds) {
		t.Fatalf("SetThresholds(0.2,1.5): got %v, want ErrBadThresholds", err)
	}
	if err := b.SetThresholds(0.3, 0.7); err != nil {
		t.Fatalf("SetThresholds(0.3,0.7) returned unexpected error: %v", err)
	}
	low, high := b.Thresholds()
	if low != 0.3 || high != 0.7 {
		t.Fatalf("Thresholds = %v,%v, want 0.3,0.7", low, high)
	}
}

func TestVisibleUsesRatioAgainstThresholds(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(1, impl.ConsumerMain, 9, 10); err != nil {
		t.Fatalf("Submit(9 of 10) returned unexpected error: %v", err)
	}
	if err := b.Submit(2, impl.ConsumerMain, 1, 10); err != nil {
		t.Fatalf("Submit(1 of 10) returned unexpected error: %v", err)
	}
	if got, err := b.Visible(1, impl.ConsumerMain); err != nil || got != impl.VerdictVisible {
		t.Fatalf("Visible(1) = %q,%v, want visible", got, err)
	}
	if got, err := b.Visible(2, impl.ConsumerMain); err != nil || got != impl.VerdictHidden {
		t.Fatalf("Visible(2) = %q,%v, want hidden", got, err)
	}
}

func TestVisibleMiddleRatioIsUnknown(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(7, impl.ConsumerMain, 5, 10); err != nil {
		t.Fatalf("Submit(5 of 10) returned unexpected error: %v", err)
	}
	got, err := b.Visible(7, impl.ConsumerMain)
	if err != nil {
		t.Fatalf("Visible(7) returned unexpected error: %v", err)
	}
	if got != impl.VerdictUnknown {
		t.Fatalf("Visible(7) = %q, want unknown for a ratio inside the band", got)
	}
}

func TestVisibleUnsubmittedObjectIsUnknown(t *testing.T) {
	b := newBroker(30)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	got, err := b.Visible(4242, impl.ConsumerMain)
	if err != nil {
		t.Fatalf("Visible(unsubmitted) returned unexpected error: %v", err)
	}
	if got != impl.VerdictUnknown {
		t.Fatalf("Visible(unsubmitted) = %q, want unknown", got)
	}
}

func TestQuotaForSplitsByWeight(t *testing.T) {
	b := impl.NewVisibilityConfidenceBroker(impl.Config{
		SampleQuota: 70,
		TTLFrames:   8,
		Low:         0.2,
		High:        0.7,
		Weights: map[impl.Consumer]uint32{
			impl.ConsumerMain:       4,
			impl.ConsumerShadow:     2,
			impl.ConsumerReflection: 1,
		},
	})
	if got := b.QuotaFor(impl.ConsumerMain); got != 40 {
		t.Fatalf("QuotaFor(main) = %d, want 40", got)
	}
	if got := b.QuotaFor(impl.ConsumerShadow); got != 20 {
		t.Fatalf("QuotaFor(shadow) = %d, want 20", got)
	}
	if got := b.QuotaFor(impl.ConsumerReflection); got != 10 {
		t.Fatalf("QuotaFor(reflection) = %d, want 10", got)
	}
	if err := b.SetWeights(equalWeights()); err != nil {
		t.Fatalf("SetWeights returned unexpected error: %v", err)
	}
	if got := b.QuotaFor(impl.ConsumerMain); got != 23 {
		t.Fatalf("QuotaFor(main) after equal weights = %d, want floor(70/3)=23", got)
	}
	if err := b.SetWeights(map[impl.Consumer]uint32{impl.ConsumerMain: 0}); !errors.Is(err, impl.ErrBadWeights) {
		t.Fatalf("SetWeights(zero): got %v, want ErrBadWeights", err)
	}
}

func TestSubmitRejectsWhenQuotaExhausted(t *testing.T) {
	b := newBroker(6)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(1, impl.ConsumerMain, 4, 5); err != nil {
		t.Fatalf("first Submit returned unexpected error: %v", err)
	}
	if err := b.Submit(2, impl.ConsumerMain, 4, 5); err != nil {
		t.Fatalf("second Submit returned unexpected error: %v", err)
	}
	if err := b.Submit(3, impl.ConsumerMain, 4, 5); !errors.Is(err, impl.ErrQuotaExhausted) {
		t.Fatalf("third Submit: got %v, want ErrQuotaExhausted", err)
	}
	if err := b.Submit(4, impl.ConsumerShadow, 4, 5); err != nil {
		t.Fatalf("shadow Submit must keep its own quota, got error: %v", err)
	}
}

func TestSamplesLeftTracksSubmits(t *testing.T) {
	b := newBroker(9)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if got := b.SamplesLeft(impl.ConsumerMain); got != 3 {
		t.Fatalf("SamplesLeft at frame start = %d, want 3", got)
	}
	if err := b.Submit(1, impl.ConsumerMain, 2, 4); err != nil {
		t.Fatalf("first Submit returned unexpected error: %v", err)
	}
	if err := b.Submit(2, impl.ConsumerMain, 3, 4); err != nil {
		t.Fatalf("second Submit returned unexpected error: %v", err)
	}
	if got := b.SamplesLeft(impl.ConsumerMain); got != 1 {
		t.Fatalf("SamplesLeft after two submits = %d, want 1", got)
	}
	if got := b.SamplesLeft(impl.Consumer("hud")); got != 0 {
		t.Fatalf("SamplesLeft(unknown) = %d, want 0", got)
	}
}

func TestBeginFrameRefillsQuota(t *testing.T) {
	b := newBroker(6)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	if err := b.Submit(1, impl.ConsumerMain, 4, 5); err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if err := b.Submit(2, impl.ConsumerMain, 4, 5); err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if got := b.SamplesLeft(impl.ConsumerMain); got != 0 {
		t.Fatalf("SamplesLeft before new frame = %d, want 0", got)
	}
	if err := b.BeginFrame(2); err != nil {
		t.Fatalf("BeginFrame(2) returned unexpected error: %v", err)
	}
	if got := b.SamplesLeft(impl.ConsumerMain); got != 2 {
		t.Fatalf("SamplesLeft after new frame = %d, want 2", got)
	}
}

func TestSnapshotRestoreKeepsVerdictsAndThresholds(t *testing.T) {
	b := newBroker(9)
	if err := b.BeginFrame(7); err != nil {
		t.Fatalf("BeginFrame(7) returned unexpected error: %v", err)
	}
	if err := b.SetThresholds(0.25, 0.75); err != nil {
		t.Fatalf("SetThresholds returned unexpected error: %v", err)
	}
	if err := b.Submit(11, impl.ConsumerMain, 9, 10); err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := newBroker(9)
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := fresh.Frame(); got != 7 {
		t.Fatalf("Frame after Restore = %d, want 7", got)
	}
	low, high := fresh.Thresholds()
	if low != 0.25 || high != 0.75 {
		t.Fatalf("Thresholds after Restore = %v,%v, want 0.25,0.75", low, high)
	}
	if got, err := fresh.Visible(11, impl.ConsumerMain); err != nil || got != impl.VerdictVisible {
		t.Fatalf("Visible(11) after Restore = %q,%v, want visible", got, err)
	}
	if got := fresh.SamplesLeft(impl.ConsumerMain); got != 2 {
		t.Fatalf("SamplesLeft after Restore = %d, want 2", got)
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	b := newBroker(9)
	if err := b.BeginFrame(4); err != nil {
		t.Fatalf("BeginFrame(4) returned unexpected error: %v", err)
	}
	if err := b.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := b.Frame(); got != 4 {
		t.Fatalf("Frame after rejected Restore = %d, want 4", got)
	}
}

func TestConcurrentSubmitsKeepExactSampleCounts(t *testing.T) {
	b := newBroker(300)
	if err := b.BeginFrame(1); err != nil {
		t.Fatalf("BeginFrame(1) returned unexpected error: %v", err)
	}
	consumers := []impl.Consumer{impl.ConsumerMain, impl.ConsumerShadow, impl.ConsumerReflection}
	const perConsumer = 30
	var wg sync.WaitGroup
	for ci, c := range consumers {
		for i := 0; i < perConsumer; i++ {
			wg.Add(1)
			go func(ci, i int, c impl.Consumer) {
				defer wg.Done()
				obj := impl.ObjectID(ci*1000 + i)
				if err := b.Submit(obj, c, 8, 10); err != nil {
					t.Errorf("Submit(%d,%s) returned error: %v", obj, c, err)
				}
			}(ci, i, c)
		}
	}
	wg.Wait()
	for _, c := range consumers {
		if got := b.SamplesLeft(c); got != 70 {
			t.Fatalf("SamplesLeft(%s) = %d, want 70", c, got)
		}
	}
}
