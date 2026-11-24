package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-18"
)

func TestSampleComputesOffsetFromMidpoint(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(1000, 1100, 5000); err != nil {
		t.Fatalf("sample: %v", err)
	}
	if got := e.Offset(); got != 3950 {
		t.Fatalf("offset = %d, want 3950", got)
	}
	if got := e.RTT(); got != 100 {
		t.Fatalf("rtt = %d, want 100", got)
	}
	if got := e.Samples(); got != 1 {
		t.Fatalf("samples = %d, want 1", got)
	}
}

func TestRTTUsesMedianOfWindow(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(1000, 1100, 5000); err != nil {
		t.Fatalf("sample one: %v", err)
	}
	if err := e.Sample(2000, 2020, 6000); err != nil {
		t.Fatalf("sample two: %v", err)
	}
	if err := e.Sample(3000, 3060, 7000); err != nil {
		t.Fatalf("sample three: %v", err)
	}
	if got := e.RTT(); got != 60 {
		t.Fatalf("median rtt = %d, want 60", got)
	}
	if got := e.Offset(); got != 3970 {
		t.Fatalf("median offset = %d, want 3970", got)
	}
}

func TestOffsetMedianRejectsSingleOutlier(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	samples := [][3]int64{
		{0, 100, 150},
		{200, 300, 350},
		{400, 500, 550},
		{600, 700, 1550},
		{800, 900, 950},
	}
	for _, s := range samples {
		if err := e.Sample(s[0], s[1], s[2]); err != nil {
			t.Fatalf("sample %v: %v", s, err)
		}
	}
	if got := e.Offset(); got != 100 {
		t.Fatalf("offset = %d, want 100: a single outlier must not move the median", got)
	}
	if got := e.Samples(); got != 5 {
		t.Fatalf("samples = %d, want 5", got)
	}
}

func TestWindowDropsOldestSamples(t *testing.T) {
	e := impl.NewSessionClockEstimator(3)
	if err := e.Sample(0, 100, 60); err != nil {
		t.Fatalf("sample one: %v", err)
	}
	if err := e.Sample(200, 300, 270); err != nil {
		t.Fatalf("sample two: %v", err)
	}
	if err := e.Sample(400, 500, 480); err != nil {
		t.Fatalf("sample three: %v", err)
	}
	if got := e.Offset(); got != 20 {
		t.Fatalf("offset before slide = %d, want 20", got)
	}
	if err := e.Sample(600, 700, 950); err != nil {
		t.Fatalf("sample four: %v", err)
	}
	if got := e.Samples(); got != 3 {
		t.Fatalf("samples = %d, want 3", got)
	}
	if got := e.Offset(); got != 30 {
		t.Fatalf("offset after slide = %d, want 30", got)
	}
}

func TestSampleRejectsInvalidRoundTrip(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(500, 500, 0); !errors.Is(err, impl.ErrInvalidSample) {
		t.Fatalf("zero rtt sample error = %v, want ErrInvalidSample", err)
	}
	if err := e.Sample(600, 590, 0); !errors.Is(err, impl.ErrInvalidSample) {
		t.Fatalf("negative rtt sample error = %v, want ErrInvalidSample", err)
	}
	if got := e.Samples(); got != 0 {
		t.Fatalf("samples = %d, want 0", got)
	}
	if got := e.Rejected(); got != 2 {
		t.Fatalf("rejected = %d, want 2", got)
	}
}

func TestSetWindowTrimsToNewestSamples(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	samples := [][3]int64{
		{0, 100, 60},
		{200, 300, 270},
		{400, 500, 480},
		{600, 700, 690},
		{800, 900, 900},
	}
	for _, s := range samples {
		if err := e.Sample(s[0], s[1], s[2]); err != nil {
			t.Fatalf("sample %v: %v", s, err)
		}
	}
	if err := e.SetWindow(3); err != nil {
		t.Fatalf("set window: %v", err)
	}
	if got := e.Samples(); got != 3 {
		t.Fatalf("samples after trim = %d, want 3", got)
	}
	if got := e.Offset(); got != 40 {
		t.Fatalf("offset after trim = %d, want 40 from the newest three samples", got)
	}
}

func TestSetWindowRejectsNonPositive(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.SetWindow(0); !errors.Is(err, impl.ErrInvalidWindow) {
		t.Fatalf("zero window error = %v, want ErrInvalidWindow", err)
	}
	if err := e.SetWindow(-3); !errors.Is(err, impl.ErrInvalidWindow) {
		t.Fatalf("negative window error = %v, want ErrInvalidWindow", err)
	}
	if err := e.SetWindow(4); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
}

func TestResetClearsSamplesAndEstimate(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(0, 100, 150); err != nil {
		t.Fatalf("sample: %v", err)
	}
	e.Reset()
	if got := e.Samples(); got != 0 {
		t.Fatalf("samples after reset = %d, want 0", got)
	}
	if got := e.Offset(); got != 0 {
		t.Fatalf("offset after reset = %d, want 0", got)
	}
	if got := e.RTT(); got != 0 {
		t.Fatalf("rtt after reset = %d, want 0", got)
	}
	if err := e.Sample(200, 300, 550); err != nil {
		t.Fatalf("sample after reset: %v", err)
	}
	if got := e.Offset(); got != 300 {
		t.Fatalf("offset after new sample = %d, want 300", got)
	}
}

func TestSampleRejectsLocalClockRollback(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(1000, 1100, 5000); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := e.Sample(900, 1000, 5000); !errors.Is(err, impl.ErrClockRollback) {
		t.Fatalf("rollback sample error = %v, want ErrClockRollback", err)
	}
	if got := e.Samples(); got != 1 {
		t.Fatalf("samples = %d, want 1: the rollback sample must be rejected", got)
	}
	if got := e.Rejected(); got != 1 {
		t.Fatalf("rejected = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsWindowAndEstimate(t *testing.T) {
	e := impl.NewSessionClockEstimator(3)
	if err := e.Sample(0, 100, 80); err != nil {
		t.Fatalf("sample one: %v", err)
	}
	if err := e.Sample(200, 300, 260); err != nil {
		t.Fatalf("sample two: %v", err)
	}
	if err := e.Sample(400, 500, 470); err != nil {
		t.Fatalf("sample three: %v", err)
	}
	payload, err := e.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewSessionClockEstimator(3)
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Samples(); got != 3 {
		t.Fatalf("samples after restore = %d, want 3", got)
	}
	if got := restored.Offset(); got != 20 {
		t.Fatalf("offset after restore = %d, want 20", got)
	}
	if got := restored.RTT(); got != 100 {
		t.Fatalf("rtt after restore = %d, want 100", got)
	}
}

func TestRestoreRejectsEmptyPayload(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Restore(nil); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty payload error = %v, want ErrBadSnapshot", err)
	}
	if err := e.Restore([]byte("junk-payload")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("damaged payload error = %v, want ErrBadSnapshot", err)
	}
}

func TestStatsCountsSamplesAndRejections(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(0, 100, 150); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := e.Sample(200, 200, 0); !errors.Is(err, impl.ErrInvalidSample) {
		t.Fatalf("invalid sample error = %v, want ErrInvalidSample", err)
	}
	if err := e.Sample(400, 500, 550); err != nil {
		t.Fatalf("second sample: %v", err)
	}
	st := e.Stats()
	if st.Samples != 2 || st.Rejected != 1 {
		t.Fatalf("stats = %+v, want 2 samples and 1 rejection", st)
	}
	if st.Offset != 100 || st.RTT != 100 {
		t.Fatalf("stats estimate = %+v, want offset 100 and rtt 100", st)
	}
}

func TestConcurrentReadersSeeStableEstimate(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = e.Offset()
					_ = e.RTT()
					_ = e.Samples()
				}
			}
		}()
	}
	samples := [][3]int64{
		{0, 100, 60},
		{200, 300, 270},
		{400, 500, 480},
		{600, 700, 690},
		{800, 900, 900},
	}
	for _, s := range samples {
		if err := e.Sample(s[0], s[1], s[2]); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("concurrent sample %v failed: %v", s, err)
		}
	}
	close(stop)
	wg.Wait()
	if got := e.Samples(); got != 5 {
		t.Fatalf("samples = %d, want 5", got)
	}
	if got := e.Offset(); got != 30 {
		t.Fatalf("offset = %d, want 30", got)
	}
}

func TestWindowOfOneTracksLatestSample(t *testing.T) {
	e := impl.NewSessionClockEstimator(1)
	if err := e.Sample(0, 100, 150); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := e.Sample(200, 300, 550); err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if got := e.Samples(); got != 1 {
		t.Fatalf("samples = %d, want 1", got)
	}
	if got := e.Offset(); got != 300 {
		t.Fatalf("offset = %d, want 300 from the latest sample", got)
	}
	if got := e.RTT(); got != 100 {
		t.Fatalf("rtt = %d, want 100", got)
	}
}

func TestRejectedSamplesDoNotEnterWindow(t *testing.T) {
	e := impl.NewSessionClockEstimator(5)
	if err := e.Sample(0, 100, 60); err != nil {
		t.Fatalf("sample one: %v", err)
	}
	if err := e.Sample(200, 200, 0); !errors.Is(err, impl.ErrInvalidSample) {
		t.Fatalf("invalid sample error = %v, want ErrInvalidSample", err)
	}
	if err := e.Sample(400, 500, 480); err != nil {
		t.Fatalf("sample two: %v", err)
	}
	if err := e.Sample(600, 700, 690); err != nil {
		t.Fatalf("sample three: %v", err)
	}
	if got := e.Samples(); got != 3 {
		t.Fatalf("samples = %d, want 3 valid samples only", got)
	}
	if got := e.Offset(); got != 30 {
		t.Fatalf("offset = %d, want 30 from the three valid samples", got)
	}
}
