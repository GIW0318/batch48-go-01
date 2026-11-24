package task

import (
	"errors"
	"reflect"
	"testing"

	impl "gsb/batch48-go-safety-c-09"
)

const peSectionTableOffset = 0x188

func put16(b []byte, off int, v uint16) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
}

func put32(b []byte, off int, v uint32) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
	b[off+2] = byte(v >> 16)
	b[off+3] = byte(v >> 24)
}

func put64(b []byte, off int, v uint64) {
	for i := 0; i < 8; i++ {
		b[off+i] = byte(v >> (8 * uint(i)))
	}
}

func peHeader(b []byte, sections uint16) {
	b[0], b[1] = 'M', 'Z'
	put32(b, 0x3C, 0x80)
	copy(b[0x80:], []byte{'P', 'E', 0, 0})
	put16(b, 0x84, 0x8664)
	put16(b, 0x86, sections)
	put16(b, 0x94, 240)
	put16(b, 0x98, 0x20b)
	put32(b, 0x98+108, 16)
}

func addSection(b []byte, index int, name string, virtualSize, virtualAddress, rawSize, rawOffset uint32) {
	off := peSectionTableOffset + index*40
	copy(b[off:off+8], []byte(name))
	put32(b, off+8, virtualSize)
	put32(b, off+12, virtualAddress)
	put32(b, off+16, rawSize)
	put32(b, off+20, rawOffset)
}

func peSectionsSample() []byte {
	b := make([]byte, 0x600)
	peHeader(b, 2)
	addSection(b, 0, ".text", 0x1000, 0x1000, 0x200, 0x200)
	addSection(b, 1, ".rdata", 0x800, 0x2000, 0x180, 0x400)
	return b
}

func elfSample() []byte {
	b := make([]byte, 0x400)
	copy(b, []byte{0x7f, 'E', 'L', 'F'})
	b[4] = 2
	b[5] = 1
	b[6] = 1
	put16(b, 18, 62)
	put64(b, 0x28, 0x100)
	put16(b, 0x3A, 64)
	put16(b, 0x3C, 3)
	put16(b, 0x3E, 2)
	first := 0x100 + 64
	put32(b, first, 1)
	put32(b, first+4, 1)
	put64(b, first+24, 0x380)
	put64(b, first+32, 0x40)
	second := 0x100 + 128
	put32(b, second, 7)
	put32(b, second+4, 3)
	put64(b, second+24, 0x300)
	put64(b, second+32, 0x10)
	copy(b[0x300:], []byte{0, '.', 't', 'e', 'x', 't', 0, '.', 's', 't', 'r', 't', 'a', 'b', 0})
	return b
}

func peImportSample(thunkRVAs []uint64) []byte {
	b := make([]byte, 0x600)
	peHeader(b, 1)
	put32(b, 0x98+112+8, 0x1000)
	put32(b, 0x98+112+12, 40)
	addSection(b, 0, ".idata", 0x200, 0x1000, 0x200, 0x400)
	put32(b, 0x400, 0x1040)
	put32(b, 0x400+12, 0x1030)
	put32(b, 0x400+16, 0x1080)
	copy(b[0x430:], []byte("KERNEL32.dll\x00"))
	for i, rva := range thunkRVAs {
		put64(b, 0x440+i*8, rva)
	}
	copy(b[0x460:], []byte{0, 0, 'G', 'e', 't', 'P', 'r', 'o', 'c', 'A', 'd', 'd', 'r', 'e', 's', 's', 0})
	copy(b[0x480:], []byte{0, 0, 'L', 'o', 'a', 'd', 'L', 'i', 'b', 'r', 'a', 'r', 'y', 'A', 0})
	return b
}

func fingerprinter(opts impl.ExtractorOptions) *impl.BinaryFingerprinter {
	return impl.NewBinaryFingerprinter(opts)
}

func TestDetectRejectsShortAndUnknownSamples(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	if _, err := fp.Detect(nil); !errors.Is(err, impl.ErrTruncated) {
		t.Fatalf("empty sample error = %v, want ErrTruncated", err)
	}
	if _, err := fp.Detect([]byte{'M', 'Z', 0, 0}); !errors.Is(err, impl.ErrTruncated) {
		t.Fatalf("short sample error = %v, want ErrTruncated", err)
	}
	junk := make([]byte, 64)
	for i := range junk {
		junk[i] = 0xFF
	}
	if _, err := fp.Detect(junk); !errors.Is(err, impl.ErrUnknownFormat) {
		t.Fatalf("junk sample error = %v, want ErrUnknownFormat", err)
	}
}

func TestDetectIdentifiesPEAndELF(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	got, err := fp.Detect(peSectionsSample())
	if err != nil {
		t.Fatalf("detect pe: unexpected error %v", err)
	}
	if got != impl.FormatPE {
		t.Fatalf("pe format = %q, want %q", got, impl.FormatPE)
	}
	got, err = fp.Detect(elfSample())
	if err != nil {
		t.Fatalf("detect elf: unexpected error %v", err)
	}
	if got != impl.FormatELF {
		t.Fatalf("elf format = %q, want %q", got, impl.FormatELF)
	}
}

func TestSectionsParsesPESectionTable(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	sections, err := fp.Sections(peSectionsSample())
	if err != nil {
		t.Fatalf("sections: unexpected error %v", err)
	}
	want := []impl.Section{
		{Name: ".rdata", VirtualSize: 0x800, RawSize: 0x180, Offset: 0x400},
		{Name: ".text", VirtualSize: 0x1000, RawSize: 0x200, Offset: 0x200},
	}
	if !reflect.DeepEqual(sections, want) {
		t.Fatalf("sections = %+v, want %+v", sections, want)
	}
}

func TestSectionsSortedByName(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	b := make([]byte, 0x600)
	peHeader(b, 3)
	addSection(b, 0, ".zdata", 0x100, 0x3000, 0x100, 0x500)
	addSection(b, 1, ".bss", 0x80, 0x2000, 0x80, 0x480)
	addSection(b, 2, ".text", 0x1000, 0x1000, 0x200, 0x200)
	sections, err := fp.Sections(b)
	if err != nil {
		t.Fatalf("sections: unexpected error %v", err)
	}
	names := make([]string, 0, len(sections))
	for _, section := range sections {
		names = append(names, section.Name)
	}
	want := []string{".bss", ".text", ".zdata"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("section order = %v, want %v", names, want)
	}
}

func TestSectionsEnforcesSectionLimit(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{MaxSections: 2})
	b := make([]byte, 0x600)
	peHeader(b, 3)
	addSection(b, 0, ".one", 0x10, 0x1000, 0x10, 0x200)
	addSection(b, 1, ".two", 0x10, 0x2000, 0x10, 0x220)
	addSection(b, 2, ".three", 0x10, 0x3000, 0x10, 0x240)
	if _, err := fp.Sections(b); !errors.Is(err, impl.ErrSectionLimit) {
		t.Fatalf("section limit error = %v, want ErrSectionLimit", err)
	}
}

func TestSectionsRejectsTableBeyondFile(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	full := peSectionsSample()
	cut := full[:0x1A0]
	if _, err := fp.Sections(cut); !errors.Is(err, impl.ErrTruncated) {
		t.Fatalf("short section table error = %v, want ErrTruncated", err)
	}
}

func TestSectionsParsesELFSectionNames(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	sections, err := fp.Sections(elfSample())
	if err != nil {
		t.Fatalf("elf sections: unexpected error %v", err)
	}
	want := []impl.Section{
		{Name: ".strtab", VirtualSize: 0x10, RawSize: 0x10, Offset: 0x300},
		{Name: ".text", VirtualSize: 0x40, RawSize: 0x40, Offset: 0x380},
	}
	if !reflect.DeepEqual(sections, want) {
		t.Fatalf("elf sections = %+v, want %+v", sections, want)
	}
}

func TestImportsParsesNamedImports(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	imports, err := fp.Imports(peImportSample([]uint64{0x1060, 0x1080}))
	if err != nil {
		t.Fatalf("imports: unexpected error %v", err)
	}
	want := []impl.Import{
		{Library: "KERNEL32.dll", Symbol: "GetProcAddress"},
		{Library: "KERNEL32.dll", Symbol: "LoadLibraryA"},
	}
	if !reflect.DeepEqual(imports, want) {
		t.Fatalf("imports = %+v, want %+v", imports, want)
	}
}

func TestImportsDeduplicatesSymbols(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	imports, err := fp.Imports(peImportSample([]uint64{0x1060, 0x1060}))
	if err != nil {
		t.Fatalf("imports: unexpected error %v", err)
	}
	if len(imports) != 1 {
		t.Fatalf("deduplicated imports = %d, want 1", len(imports))
	}
	if imports[0].Symbol != "GetProcAddress" {
		t.Fatalf("kept symbol = %q, want GetProcAddress", imports[0].Symbol)
	}
}

func TestImportsRejectsELFSamples(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	if _, err := fp.Imports(elfSample()); !errors.Is(err, impl.ErrUnsupportedFormat) {
		t.Fatalf("elf imports error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestFuzzyHashDeterministicAndContentSensitive(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	first := []byte("sample payload used by the fuzzy hasher")
	copyOfFirst := append([]byte(nil), first...)
	other := []byte("a completely different sample payload")
	h1, err := fp.FuzzyHash(first)
	if err != nil {
		t.Fatalf("fuzzy hash: unexpected error %v", err)
	}
	if h1 == 0 {
		t.Fatal("fuzzy hash must not be zero")
	}
	h2, err := fp.FuzzyHash(copyOfFirst)
	if err != nil {
		t.Fatalf("fuzzy hash copy: unexpected error %v", err)
	}
	if h1 != h2 {
		t.Fatalf("identical samples hashed to %d and %d", h1, h2)
	}
	h3, err := fp.FuzzyHash(other)
	if err != nil {
		t.Fatalf("fuzzy hash other: unexpected error %v", err)
	}
	if h3 == h1 {
		t.Fatal("different samples must hash differently")
	}
}

func TestStableRequiresNormalizedFeature(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	valid := impl.Feature{
		Digest:   "0123456789abcdef",
		Format:   impl.FormatPE,
		Sections: []impl.Section{{Name: ".rdata"}, {Name: ".text"}},
		Imports: []impl.Import{
			{Library: "KERNEL32.dll", Symbol: "GetProcAddress"},
			{Library: "KERNEL32.dll", Symbol: "LoadLibraryA"},
		},
		Fuzzy: 42,
		Seen:  1,
	}
	if !fp.Stable(valid) {
		t.Fatal("normalised feature must be stable")
	}
	unsorted := valid
	unsorted.Sections = []impl.Section{{Name: ".text"}, {Name: ".rdata"}}
	if fp.Stable(unsorted) {
		t.Fatal("unsorted sections must not be stable")
	}
	duplicated := valid
	duplicated.Imports = []impl.Import{
		{Library: "KERNEL32.dll", Symbol: "GetProcAddress"},
		{Library: "KERNEL32.dll", Symbol: "GetProcAddress"},
	}
	if fp.Stable(duplicated) {
		t.Fatal("duplicated imports must not be stable")
	}
	empty := valid
	empty.Fuzzy = 0
	if fp.Stable(empty) {
		t.Fatal("feature without fuzzy hash must not be stable")
	}
}

func TestExtractBuildsFeatureAndCapsTable(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{MaxFeatures: 2})
	sample := peSectionsSample()
	if got := fp.Capacity(); got != 2 {
		t.Fatalf("capacity = %d, want 2", got)
	}
	first, err := fp.Extract(sample)
	if err != nil {
		t.Fatalf("extract: unexpected error %v", err)
	}
	if len(first.Digest) != 16 {
		t.Fatalf("digest = %q, want 16 hex characters", first.Digest)
	}
	for _, c := range first.Digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("digest %q contains a non lowercase hex character", first.Digest)
		}
	}
	if first.Format != impl.FormatPE {
		t.Fatalf("feature format = %q, want %q", first.Format, impl.FormatPE)
	}
	if first.Seen != 1 {
		t.Fatalf("seen = %d, want 1", first.Seen)
	}
	if len(first.Sections) != 2 {
		t.Fatalf("feature sections = %d, want 2", len(first.Sections))
	}
	if _, err := fp.Extract(sample); err != nil {
		t.Fatalf("second extract: unexpected error %v", err)
	}
	if _, err := fp.Extract(sample); !errors.Is(err, impl.ErrFeatureLimit) {
		t.Fatalf("third extract error = %v, want ErrFeatureLimit", err)
	}
	if got := len(fp.Features()); got != 2 {
		t.Fatalf("feature table = %d, want 2", got)
	}
}

func TestSnapshotRestorePreservesFeatures(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{MaxFeatures: 4})
	if _, err := fp.Extract(peSectionsSample()); err != nil {
		t.Fatalf("extract pe: unexpected error %v", err)
	}
	if _, err := fp.Extract(elfSample()); err != nil {
		t.Fatalf("extract elf: unexpected error %v", err)
	}
	data, err := fp.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := fingerprinter(impl.ExtractorOptions{MaxFeatures: 4})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if !reflect.DeepEqual(restored.Features(), fp.Features()) {
		t.Fatalf("restored features = %+v, want %+v", restored.Features(), fp.Features())
	}
	if restored.Stats() != fp.Stats() {
		t.Fatalf("restored stats = %+v, want %+v", restored.Stats(), fp.Stats())
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	fp := fingerprinter(impl.ExtractorOptions{})
	if err := fp.Restore([]byte(`{"features":[{"digest":`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore error = %v, want ErrBadSnapshot", err)
	}
	if got := len(fp.Features()); got != 0 {
		t.Fatalf("features after failed restore = %d, want 0", got)
	}
}
