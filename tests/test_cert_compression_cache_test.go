package task

import (
	"bytes"
	"compress/zlib"
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-27"
)

func samplePlain(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*37 + 11) % 251)
	}
	return out
}

func zlibCompress(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("zlib write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close failed: %v", err)
	}
	return buf.Bytes()
}

func newCertCache() *impl.CertCompressionCache {
	return impl.NewCertCompressionCache(8, 4)
}

func TestNegotiateSelectsFirstSupportedAlgorithm(t *testing.T) {
	c := newCertCache()
	algo, ok, err := c.Negotiate("conn-a", []impl.Algorithm{
		impl.AlgorithmBrotli,
		impl.AlgorithmZlib,
		impl.AlgorithmDeflate,
	})
	if err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	if !ok || algo != impl.AlgorithmZlib {
		t.Fatalf("negotiation = %q/%v, want zlib/true", algo, ok)
	}
	algo, ok, err = c.Negotiate("conn-b", []impl.Algorithm{impl.AlgorithmDeflate})
	if err != nil {
		t.Fatalf("second negotiation must succeed, got %v", err)
	}
	if !ok || algo != impl.AlgorithmDeflate {
		t.Fatalf("second negotiation = %q/%v, want deflate/true", algo, ok)
	}
}

func TestNegotiateFallbackDoesNotConsumeConnectionBudget(t *testing.T) {
	c := impl.NewCertCompressionCache(1, 2)
	algo, ok, err := c.Negotiate("fallback", []impl.Algorithm{impl.AlgorithmBrotli})
	if err != nil {
		t.Fatalf("fallback negotiation must succeed, got %v", err)
	}
	if ok || algo != impl.AlgorithmNone {
		t.Fatalf("fallback negotiation = %q/%v, want none/false", algo, ok)
	}
	if got := c.Stats().Connections; got != 0 {
		t.Fatalf("fallback consumed %d connection slots, want 0", got)
	}
	if _, _, err := c.Negotiate("real", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("the only connection slot must still be available, got %v", err)
	}
}

func TestNegotiateRejectsBlankAndRepeatedConnection(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("   ", []impl.Algorithm{impl.AlgorithmZlib}); !errors.Is(err, impl.ErrBlankConn) {
		t.Fatalf("blank connection must be rejected with ErrBlankConn, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("first negotiation must succeed, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); !errors.Is(err, impl.ErrAlreadyNegotiated) {
		t.Fatalf("repeated negotiation must report ErrAlreadyNegotiated, got %v", err)
	}
}

func TestNegotiateEnforcesConnectionLimit(t *testing.T) {
	c := impl.NewCertCompressionCache(1, 4)
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("first negotiation must succeed, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-b", []impl.Algorithm{impl.AlgorithmZlib}); !errors.Is(err, impl.ErrConnLimit) {
		t.Fatalf("second connection must be rejected with ErrConnLimit, got %v", err)
	}
	if err := c.Reset("conn-a"); err != nil {
		t.Fatalf("reset must release the slot, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-b", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("freed slot must be reusable, got %v", err)
	}
}

func TestDecompressRoundTripsZlibPayload(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	plain := samplePlain(512)
	out, err := c.Decompress("conn-a", zlibCompress(t, plain))
	if err != nil {
		t.Fatalf("decompression must succeed, got %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("decompressed payload differs from the original")
	}
}

func TestDecompressRejectsOutputOverLimit(t *testing.T) {
	c := newCertCache()
	if err := c.SetLimits(256, 64); err != nil {
		t.Fatalf("setting limits must succeed, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	out, err := c.Decompress("conn-a", zlibCompress(t, samplePlain(1024)))
	if !errors.Is(err, impl.ErrOutputTooLarge) {
		t.Fatalf("output over the limit must report ErrOutputTooLarge, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("rejected decompression returned %d plaintext bytes, want 0", len(out))
	}
}

func TestDecompressRequiresNegotiation(t *testing.T) {
	c := newCertCache()
	if _, err := c.Decompress("ghost", []byte{0x78, 0x9c}); !errors.Is(err, impl.ErrNotNegotiated) {
		t.Fatalf("decompression without negotiation must report ErrNotNegotiated, got %v", err)
	}
}

func TestDecompressNoneConnectionRejected(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("fallback", []impl.Algorithm{impl.AlgorithmBrotli}); err != nil {
		t.Fatalf("fallback negotiation must succeed, got %v", err)
	}
	if _, err := c.Decompress("fallback", []byte("plain")); !errors.Is(err, impl.ErrNoCompression) {
		t.Fatalf("fallback connection must report ErrNoCompression, got %v", err)
	}
}

func TestSetLimitsValidatesAndApplies(t *testing.T) {
	c := newCertCache()
	if err := c.SetLimits(0, 4); !errors.Is(err, impl.ErrBadLimits) {
		t.Fatalf("zero output limit must report ErrBadLimits, got %v", err)
	}
	if err := c.SetLimits(100, 0); !errors.Is(err, impl.ErrBadLimits) {
		t.Fatalf("ratio below one must report ErrBadLimits, got %v", err)
	}
	if err := c.SetLimits(100, 1); err != nil {
		t.Fatalf("valid limits must be accepted, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	if _, err := c.Decompress("conn-a", zlibCompress(t, samplePlain(200))); !errors.Is(err, impl.ErrOutputTooLarge) {
		t.Fatalf("tightened output limit must reject the payload, got %v", err)
	}
}

func TestDecompressRejectsCorruptPayload(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	out, err := c.Decompress("conn-a", []byte{0x78, 0x9c, 0xff, 0xff, 0xff, 0xff, 0xff})
	if !errors.Is(err, impl.ErrCorruptCompressed) {
		t.Fatalf("corrupt payload must report ErrCorruptCompressed, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("corrupt payload returned %d plaintext bytes, want 0", len(out))
	}
}

func TestResetDropsNegotiationState(t *testing.T) {
	c := newCertCache()
	if err := c.Reset("ghost"); !errors.Is(err, impl.ErrConnNotFound) {
		t.Fatalf("reset of an unknown connection must report ErrConnNotFound, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	if err := c.Reset("conn-a"); err != nil {
		t.Fatalf("reset of a negotiated connection must succeed, got %v", err)
	}
	if algo, ok := c.Negotiated("conn-a"); ok || algo != impl.AlgorithmNone {
		t.Fatalf("negotiated state after reset = %q/%v, want none/false", algo, ok)
	}
	if _, err := c.Decompress("conn-a", zlibCompress(t, samplePlain(32))); !errors.Is(err, impl.ErrNotNegotiated) {
		t.Fatalf("decompression after reset must report ErrNotNegotiated, got %v", err)
	}
	if got := c.Stats().Connections; got != 0 {
		t.Fatalf("connections after reset = %d, want 0", got)
	}
}

func TestConcurrentDecompressIsSafe(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	plain := samplePlain(256)
	compressed := zlibCompress(t, plain)
	const workers = 8
	const rounds = 20
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				out, err := c.Decompress("conn-a", compressed)
				if err != nil {
					errCh <- err
					return
				}
				if !bytes.Equal(out, plain) {
					errCh <- errors.New("concurrent decompression corrupted the payload")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent decompression failed: %v", err)
	}
}

func TestSnapshotRestoreKeepsNegotiatedState(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("zlib negotiation must succeed, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-b", []impl.Algorithm{impl.AlgorithmDeflate}); err != nil {
		t.Fatalf("deflate negotiation must succeed, got %v", err)
	}
	dictID := c.DictionaryID("conn-a")
	if dictID == "" {
		t.Fatalf("negotiated connection must have a dictionary identity")
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewCertCompressionCache(8, 4)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if algo, ok := restored.Negotiated("conn-a"); !ok || algo != impl.AlgorithmZlib {
		t.Fatalf("restored algorithm for conn-a = %q/%v, want zlib/true", algo, ok)
	}
	if got := restored.DictionaryID("conn-a"); got != dictID {
		t.Fatalf("restored dictionary identity = %q, want %q", got, dictID)
	}
	plain := samplePlain(128)
	out, err := restored.Decompress("conn-a", zlibCompress(t, plain))
	if err != nil {
		t.Fatalf("decompression after restore must succeed, got %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("decompressed payload after restore differs from the original")
	}
	if got := restored.Stats().Connections; got != 2 {
		t.Fatalf("restored connections = %d, want 2", got)
	}
}

func TestRestoreRequiresRenegotiationForUnknownConnection(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("negotiation must succeed, got %v", err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewCertCompressionCache(8, 4)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if algo, ok := restored.Negotiated("conn-b"); ok || algo != impl.AlgorithmNone {
		t.Fatalf("unknown connection state = %q/%v, want none/false", algo, ok)
	}
	if _, err := restored.Decompress("conn-b", zlibCompress(t, samplePlain(16))); !errors.Is(err, impl.ErrNotNegotiated) {
		t.Fatalf("unknown connection must renegotiate, got %v", err)
	}
}

func TestDictionaryIdentityIsSharedPerAlgorithm(t *testing.T) {
	c := newCertCache()
	if _, _, err := c.Negotiate("conn-a", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("first negotiation must succeed, got %v", err)
	}
	if _, _, err := c.Negotiate("conn-b", []impl.Algorithm{impl.AlgorithmZlib}); err != nil {
		t.Fatalf("second negotiation must succeed, got %v", err)
	}
	idA := c.DictionaryID("conn-a")
	idB := c.DictionaryID("conn-b")
	if idA == "" || idA != idB {
		t.Fatalf("same algorithm dictionary identities = %q and %q, want one shared identity", idA, idB)
	}
	if got := c.Stats().Dictionaries; got != 1 {
		t.Fatalf("dictionary cache size = %d, want 1", got)
	}
	if got := c.Stats().Connections; got != 2 {
		t.Fatalf("connections = %d, want 2", got)
	}
}
