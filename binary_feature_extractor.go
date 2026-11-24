package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

// Sentinel errors reported by the extractor.
var (
	ErrTruncated         = errors.New("binary_feature_extractor: truncated sample")
	ErrUnknownFormat     = errors.New("binary_feature_extractor: unknown format")
	ErrUnsupportedFormat = errors.New("binary_feature_extractor: unsupported format")
	ErrSectionLimit      = errors.New("binary_feature_extractor: too many sections")
	ErrImportLimit       = errors.New("binary_feature_extractor: too many imports")
	ErrFeatureLimit      = errors.New("binary_feature_extractor: feature table full")
	ErrBadSnapshot       = errors.New("binary_feature_extractor: bad snapshot")
)

// Format is the container format detected from the sample header.
type Format string

// Known container formats.
const (
	FormatPE      Format = "pe"
	FormatELF     Format = "elf"
	FormatUnknown Format = "unknown"
)

const (
	defaultMaxSections = 96
	defaultMaxImports  = 512
	defaultMaxFeatures = 256

	peSignatureSize  = 4
	peFileHeaderSize = 20
	peSectionSize    = 40

	elfHeaderSize       = 64
	elfSectionHeaderMin = 40

	fuzzyBlockSize  = 512
	snapshotVersion = 1
)

// Section is one section header entry.
type Section struct {
	Name        string
	VirtualSize uint32
	RawSize     uint32
	Offset      uint32
	Truncated   bool
}

// Import is one imported symbol.
type Import struct {
	Library string
	Symbol  string
	Ordinal uint16
}

// Feature is the searchable fingerprint of one sample.
type Feature struct {
	Digest   string
	Format   Format
	Sections []Section
	Imports  []Import
	Fuzzy    uint64
	Seen     int
}

// ExtractorOptions tunes section, import and feature table capacities.
type ExtractorOptions struct {
	MaxSections int
	MaxImports  int
	MaxFeatures int
}

// ExtractorStats is a consistent view of the extractor counters.
type ExtractorStats struct {
	Extracted int
	Rejected  int
	Sections  int
	Imports   int
	Features  int
}

// BinaryFingerprinter extracts stable features from PE and ELF samples.
type BinaryFingerprinter struct {
	opts ExtractorOptions

	mu       sync.RWMutex
	features []Feature
	stats    ExtractorStats
}

// NewBinaryFingerprinter builds a fingerprinter with the given options.
func NewBinaryFingerprinter(opts ExtractorOptions) *BinaryFingerprinter {
	if opts.MaxSections <= 0 {
		opts.MaxSections = defaultMaxSections
	}
	if opts.MaxImports <= 0 {
		opts.MaxImports = defaultMaxImports
	}
	if opts.MaxFeatures <= 0 {
		opts.MaxFeatures = defaultMaxFeatures
	}
	return &BinaryFingerprinter{opts: opts}
}

// Detect reports the container format of the sample.
func (b *BinaryFingerprinter) Detect(data []byte) (Format, error) {
	if len(data) < 4 {
		return FormatUnknown, ErrTruncated
	}
	if data[0] == 0x7f && data[1] == 'E' && data[2] == 'L' && data[3] == 'F' {
		return FormatELF, nil
	}
	if data[0] != 'M' || data[1] != 'Z' {
		return FormatUnknown, ErrUnknownFormat
	}
	if len(data) < 0x40 {
		return FormatUnknown, ErrTruncated
	}
	peOffset := uint64(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	if peOffset > uint64(len(data)) || peSignatureSize > uint64(len(data))-peOffset {
		return FormatUnknown, ErrTruncated
	}
	if !bytes.Equal(data[peOffset:peOffset+peSignatureSize], []byte{'P', 'E', 0, 0}) {
		return FormatUnknown, ErrUnknownFormat
	}
	return FormatPE, nil
}

// Sections returns the section table sorted by name.
func (b *BinaryFingerprinter) Sections(data []byte) ([]Section, error) {
	format, err := b.Detect(data)
	if err != nil {
		return nil, err
	}
	switch format {
	case FormatPE:
		entries, err := b.readPESections(data)
		if err != nil {
			return nil, err
		}
		sections := make([]Section, len(entries))
		for i := range entries {
			sections[i] = entries[i].Section
		}
		sortSections(sections)
		return sections, nil
	case FormatELF:
		return b.readELFSections(data)
	default:
		return nil, ErrUnsupportedFormat
	}
}

// Imports returns the imported symbols sorted by library and name.
func (b *BinaryFingerprinter) Imports(data []byte) ([]Import, error) {
	format, err := b.Detect(data)
	if err != nil {
		return nil, err
	}
	if format != FormatPE {
		return nil, ErrUnsupportedFormat
	}

	layout, err := parsePELayout(data)
	if err != nil {
		return nil, err
	}
	entries, err := b.readPESections(data)
	if err != nil {
		return nil, err
	}
	importRVA, _, ok, err := readImportDirectory(data, layout)
	if err != nil {
		return nil, err
	}
	if !ok || importRVA == 0 {
		return nil, nil
	}
	importOffset, ok := mapRVAToOffset(entries, importRVA)
	if !ok {
		return nil, ErrTruncated
	}

	seen := make(map[string]Import)
	for {
		if importOffset > uint64(len(data)) || 20 > uint64(len(data))-importOffset {
			return nil, ErrTruncated
		}
		descriptor := data[importOffset : importOffset+20]
		if allZero(descriptor) {
			break
		}

		originalFirstThunk := binary.LittleEndian.Uint32(descriptor[0:4])
		nameRVA := binary.LittleEndian.Uint32(descriptor[12:16])
		firstThunk := binary.LittleEndian.Uint32(descriptor[16:20])

		nameOffset, ok := mapRVAToOffset(entries, nameRVA)
		if !ok {
			return nil, ErrTruncated
		}
		library, err := readCString(data, nameOffset)
		if err != nil {
			return nil, err
		}

		thunkRVA := originalFirstThunk
		if thunkRVA == 0 {
			thunkRVA = firstThunk
		}
		if thunkRVA != 0 {
			pointerSize := 4
			if layout.optionalMagic == 0x20b {
				pointerSize = 8
			}
			if err := b.readThunks(data, entries, library, thunkRVA, pointerSize, seen); err != nil {
				return nil, err
			}
		}
		importOffset += 20
	}

	imports := make([]Import, 0, len(seen))
	for _, imp := range seen {
		imports = append(imports, imp)
	}
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].Library != imports[j].Library {
			return imports[i].Library < imports[j].Library
		}
		return imports[i].Symbol < imports[j].Symbol
	})
	return imports, nil
}

// FuzzyHash returns the stable 64 bit fuzzy digest of the sample.
func (b *BinaryFingerprinter) FuzzyHash(data []byte) (uint64, error) {
	if len(data) == 0 {
		return 0, ErrTruncated
	}
	payload := b.canonicalFuzzyPayload(data)
	if len(payload) == 0 {
		payload = data
	}
	complete := len(payload) - len(payload)%fuzzyBlockSize
	if complete == 0 {
		complete = len(payload)
	}
	h := fnv.New64a()
	_, _ = h.Write(payload[:complete])
	value := h.Sum64()
	if value == 0 {
		value = 0x9e3779b97f4a7c15
	}
	return value, nil
}

func (b *BinaryFingerprinter) canonicalFuzzyPayload(data []byte) []byte {
	format, err := b.Detect(data)
	if err != nil {
		return nil
	}

	switch format {
	case FormatPE:
		layout, err := parsePELayout(data)
		if err != nil {
			return nil
		}
		tableBytes := uint64(layout.sectionCount) * peSectionSize
		if layout.sectionOffset > uint64(len(data)) || tableBytes > uint64(len(data))-layout.sectionOffset {
			return nil
		}
		headers := make([][]byte, int(layout.sectionCount))
		for i := range headers {
			offset := layout.sectionOffset + uint64(i)*peSectionSize
			headers[i] = append([]byte(nil), data[offset:offset+peSectionSize]...)
		}
		sort.SliceStable(headers, func(i, j int) bool {
			leftName := peHeaderName(headers[i])
			rightName := peHeaderName(headers[j])
			if leftName != rightName {
				return leftName < rightName
			}
			leftSize := binary.LittleEndian.Uint32(headers[i][16:20])
			rightSize := binary.LittleEndian.Uint32(headers[j][16:20])
			if leftSize != rightSize {
				return leftSize > rightSize
			}
			return bytes.Compare(headers[i], headers[j]) < 0
		})
		normalized := append([]byte(nil), data...)
		for i, header := range headers {
			offset := layout.sectionOffset + uint64(i)*peSectionSize
			copy(normalized[offset:offset+peSectionSize], header)
		}
		return normalized
	case FormatELF:
		if len(data) < elfHeaderSize || data[4] != 2 || data[5] != 1 {
			return nil
		}
		sectionOffset := binary.LittleEndian.Uint64(data[0x28:0x30])
		sectionSize := binary.LittleEndian.Uint16(data[0x3a:0x3c])
		sectionCount := binary.LittleEndian.Uint16(data[0x3c:0x3e])
		stringIndex := binary.LittleEndian.Uint16(data[0x3e:0x40])
		if sectionSize < elfSectionHeaderMin || stringIndex >= sectionCount {
			return nil
		}
		tableBytes := uint64(sectionCount) * uint64(sectionSize)
		if sectionOffset > uint64(len(data)) || tableBytes > uint64(len(data))-sectionOffset {
			return nil
		}
		stringTable, err := readELFStringTable(data, sectionOffset, uint64(sectionSize), sectionCount, stringIndex)
		if err != nil {
			return nil
		}
		headers := make([][]byte, int(sectionCount))
		for i := range headers {
			offset := sectionOffset + uint64(i)*uint64(sectionSize)
			headers[i] = append([]byte(nil), data[offset:offset+uint64(sectionSize)]...)
		}
		sort.SliceStable(headers, func(i, j int) bool {
			leftName := cStringInTable(stringTable, binary.LittleEndian.Uint32(headers[i][0:4]))
			rightName := cStringInTable(stringTable, binary.LittleEndian.Uint32(headers[j][0:4]))
			if leftName != rightName {
				return leftName < rightName
			}
			leftSize := binary.LittleEndian.Uint64(headers[i][32:40])
			rightSize := binary.LittleEndian.Uint64(headers[j][32:40])
			if leftSize != rightSize {
				return leftSize > rightSize
			}
			return bytes.Compare(headers[i], headers[j]) < 0
		})
		normalized := append([]byte(nil), data...)
		for i, header := range headers {
			offset := sectionOffset + uint64(i)*uint64(sectionSize)
			copy(normalized[offset:offset+uint64(sectionSize)], header)
		}
		return normalized
	default:
		return nil
	}
}

func peHeaderName(header []byte) string {
	name := string(header[0:8])
	if zero := bytes.IndexByte([]byte(name), 0); zero >= 0 {
		name = name[:zero]
	}
	return name
}

// Stable reports whether a feature is fully normalised.
func (b *BinaryFingerprinter) Stable(f Feature) bool {
	for i, section := range f.Sections {
		if section.Name == "" {
			return false
		}
		if i == 0 {
			continue
		}
		previous := f.Sections[i-1]
		if section.Name < previous.Name {
			return false
		}
		if section.Name == previous.Name && section.RawSize > previous.RawSize {
			return false
		}
	}

	for i, imp := range f.Imports {
		if i == 0 {
			continue
		}
		previous := f.Imports[i-1]
		if imp.Library < previous.Library {
			return false
		}
		if imp.Library == previous.Library {
			if imp.Symbol < previous.Symbol {
				return false
			}
			if imp.Symbol == previous.Symbol {
				return false
			}
		}
	}
	return f.Fuzzy != 0
}

// Extract builds the feature of one sample and appends it to the table.
func (b *BinaryFingerprinter) Extract(data []byte) (Feature, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.features) >= b.opts.MaxFeatures {
		b.stats.Rejected++
		return Feature{}, ErrFeatureLimit
	}

	format, err := b.Detect(data)
	if err != nil {
		b.stats.Rejected++
		return Feature{}, err
	}
	sections, err := b.Sections(data)
	if err != nil {
		b.stats.Rejected++
		return Feature{}, err
	}

	var imports []Import
	if format == FormatPE {
		imports, err = b.Imports(data)
		if err != nil {
			b.stats.Rejected++
			return Feature{}, err
		}
	}

	fuzzy, err := b.FuzzyHash(data)
	if err != nil {
		b.stats.Rejected++
		return Feature{}, err
	}

	feature := Feature{
		Format:   format,
		Sections: sections,
		Imports:  imports,
		Fuzzy:    fuzzy,
		Seen:     1,
	}
	feature.Digest = featureDigest(feature)

	b.features = append(b.features, feature)
	b.stats.Extracted++
	b.stats.Sections += len(feature.Sections)
	b.stats.Imports += len(feature.Imports)
	b.stats.Features = len(b.features)
	return cloneFeature(feature), nil
}

// Features returns the recorded feature table.
func (b *BinaryFingerprinter) Features() []Feature {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.features == nil {
		return nil
	}
	features := make([]Feature, len(b.features))
	for i := range b.features {
		features[i] = cloneFeature(b.features[i])
	}
	return features
}

// Capacity returns the maximum number of retained features.
func (b *BinaryFingerprinter) Capacity() int {
	return b.opts.MaxFeatures
}

// Reset drops the feature table and counters.
func (b *BinaryFingerprinter) Reset() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.features = nil
	b.stats = ExtractorStats{}
	return nil
}

// Stats returns a consistent snapshot of the counters.
func (b *BinaryFingerprinter) Stats() ExtractorStats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	stats := b.stats
	stats.Features = len(b.features)
	return stats
}

type snapshotState struct {
	Version  int            `json:"version"`
	Features []Feature      `json:"features"`
	Stats    ExtractorStats `json:"stats"`
}

type snapshotEnvelope struct {
	Version  int            `json:"version"`
	Features []Feature      `json:"features"`
	Stats    ExtractorStats `json:"stats"`
	Checksum string         `json:"checksum"`
}

// Snapshot serialises the feature table and counters.
func (b *BinaryFingerprinter) Snapshot() ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	state := snapshotState{
		Version:  snapshotVersion,
		Features: b.features,
		Stats:    b.stats,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	envelope, err := json.Marshal(snapshotEnvelope{
		Version:  state.Version,
		Features: state.Features,
		Stats:    state.Stats,
		Checksum: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

// Restore loads a state produced by Snapshot.
func (b *BinaryFingerprinter) Restore(data []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrBadSnapshot
	}
	state := snapshotState{
		Version:  envelope.Version,
		Features: envelope.Features,
		Stats:    envelope.Stats,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return ErrBadSnapshot
	}
	sum := sha256.Sum256(payload)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return ErrBadSnapshot
	}
	if state.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if len(state.Features) > b.opts.MaxFeatures {
		return ErrBadSnapshot
	}
	if state.Stats.Extracted < 0 || state.Stats.Rejected < 0 ||
		state.Stats.Sections < 0 || state.Stats.Imports < 0 ||
		state.Stats.Features != len(state.Features) {
		return ErrBadSnapshot
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.features = state.Features
	b.stats = state.Stats
	return nil
}

type peLayout struct {
	peOffset         uint64
	fileHeaderOffset uint64
	optionalOffset   uint64
	sectionOffset    uint64
	sectionCount     uint16
	optionalSize     uint16
	optionalMagic    uint16
}

type peSectionEntry struct {
	Section
	VirtualAddress uint32
}

func parsePELayout(data []byte) (peLayout, error) {
	if len(data) < 0x40 {
		return peLayout{}, ErrTruncated
	}
	if data[0] != 'M' || data[1] != 'Z' {
		return peLayout{}, ErrUnknownFormat
	}
	peOffset := uint64(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	if peOffset > uint64(len(data)) || peSignatureSize > uint64(len(data))-peOffset {
		return peLayout{}, ErrTruncated
	}
	if !bytes.Equal(data[peOffset:peOffset+peSignatureSize], []byte{'P', 'E', 0, 0}) {
		return peLayout{}, ErrUnknownFormat
	}

	fileHeader := peOffset + peSignatureSize
	if fileHeader > uint64(len(data)) || peFileHeaderSize > uint64(len(data))-fileHeader {
		return peLayout{}, ErrTruncated
	}
	optionalOffset := fileHeader + peFileHeaderSize
	layout := peLayout{
		peOffset:         peOffset,
		fileHeaderOffset: fileHeader,
		optionalOffset:   optionalOffset,
		sectionCount:     binary.LittleEndian.Uint16(data[fileHeader+2 : fileHeader+4]),
		optionalSize:     binary.LittleEndian.Uint16(data[fileHeader+16 : fileHeader+18]),
	}
	layout.sectionOffset = optionalOffset + uint64(layout.optionalSize)

	if optionalOffset > uint64(len(data)) || 2 > uint64(len(data))-optionalOffset {
		return peLayout{}, ErrTruncated
	}
	layout.optionalMagic = binary.LittleEndian.Uint16(data[optionalOffset : optionalOffset+2])
	return layout, nil
}

func (b *BinaryFingerprinter) readPESections(data []byte) ([]peSectionEntry, error) {
	return readPESectionsWithLimit(data, b.opts.MaxSections)
}

func readPESectionsWithLimit(data []byte, maxSections int) ([]peSectionEntry, error) {
	layout, err := parsePELayout(data)
	if err != nil {
		return nil, err
	}
	if int(layout.sectionCount) > maxSections {
		return nil, ErrSectionLimit
	}
	tableBytes := uint64(layout.sectionCount) * peSectionSize
	if layout.sectionOffset > uint64(len(data)) || tableBytes > uint64(len(data))-layout.sectionOffset {
		return nil, ErrTruncated
	}

	entries := make([]peSectionEntry, 0, layout.sectionCount)
	for i := 0; i < int(layout.sectionCount); i++ {
		offset := layout.sectionOffset + uint64(i)*peSectionSize
		entry := data[offset : offset+peSectionSize]

		name := string(entry[0:8])
		if zero := bytes.IndexByte([]byte(name), 0); zero >= 0 {
			name = name[:zero]
		}
		virtualSize := binary.LittleEndian.Uint32(entry[8:12])
		virtualAddress := binary.LittleEndian.Uint32(entry[12:16])
		declaredRawSize := binary.LittleEndian.Uint32(entry[16:20])
		rawOffset := binary.LittleEndian.Uint32(entry[20:24])

		rawSize := declaredRawSize
		truncated := false
		var remaining uint64
		if uint64(rawOffset) < uint64(len(data)) {
			remaining = uint64(len(data)) - uint64(rawOffset)
		}
		if uint64(declaredRawSize) > remaining {
			rawSize = clampToUint32(remaining)
			truncated = true
		}

		entries = append(entries, peSectionEntry{
			Section: Section{
				Name:        name,
				VirtualSize: virtualSize,
				RawSize:     rawSize,
				Offset:      rawOffset,
				Truncated:   truncated,
			},
			VirtualAddress: virtualAddress,
		})
	}
	return entries, nil
}

func (b *BinaryFingerprinter) readELFSections(data []byte) ([]Section, error) {
	return readELFSectionsWithLimit(data, b.opts.MaxSections)
}

func readELFSectionsWithLimit(data []byte, maxSections int) ([]Section, error) {
	if len(data) < elfHeaderSize {
		return nil, ErrTruncated
	}
	if data[4] != 2 || data[5] != 1 {
		return nil, ErrUnsupportedFormat
	}

	sectionOffset := binary.LittleEndian.Uint64(data[0x28:0x30])
	sectionSize := binary.LittleEndian.Uint16(data[0x3a:0x3c])
	sectionCount := binary.LittleEndian.Uint16(data[0x3c:0x3e])
	stringIndex := binary.LittleEndian.Uint16(data[0x3e:0x40])
	if sectionSize < elfSectionHeaderMin {
		return nil, ErrTruncated
	}
	if int(sectionCount) > maxSections {
		return nil, ErrSectionLimit
	}
	if sectionCount == 0 {
		return []Section{}, nil
	}
	tableBytes := uint64(sectionCount) * uint64(sectionSize)
	if sectionOffset > uint64(len(data)) || tableBytes > uint64(len(data))-sectionOffset {
		return nil, ErrTruncated
	}
	if stringIndex >= sectionCount {
		return nil, ErrTruncated
	}

	stringTable, err := readELFStringTable(data, sectionOffset, uint64(sectionSize), sectionCount, stringIndex)
	if err != nil {
		return nil, err
	}

	sections := make([]Section, 0, sectionCount)
	for i := 0; i < int(sectionCount); i++ {
		offset := sectionOffset + uint64(i)*uint64(sectionSize)
		header := data[offset : offset+uint64(sectionSize)]
		nameOffset := binary.LittleEndian.Uint32(header[0:4])
		if nameOffset == 0 {
			continue
		}
		name := cStringInTable(stringTable, nameOffset)
		if name == "" {
			continue
		}

		declaredOffset := binary.LittleEndian.Uint64(header[24:32])
		declaredSize := binary.LittleEndian.Uint64(header[32:40])
		rawSize := clampToUint32(declaredSize)
		truncated := false
		var remaining uint64
		if declaredOffset < uint64(len(data)) {
			remaining = uint64(len(data)) - declaredOffset
		}
		if declaredSize > remaining {
			rawSize = clampToUint32(remaining)
			truncated = true
		}
		sections = append(sections, Section{
			Name:        name,
			VirtualSize: clampToUint32(declaredSize),
			RawSize:     rawSize,
			Offset:      clampToUint32(declaredOffset),
			Truncated:   truncated,
		})
	}
	sortSections(sections)
	return sections, nil
}

func readELFStringTable(data []byte, sectionOffset, sectionSize uint64, sectionCount, index uint16) ([]byte, error) {
	readSection := func(i uint16) (uint64, uint64, bool) {
		if i >= sectionCount {
			return 0, 0, false
		}
		offset := sectionOffset + uint64(i)*sectionSize
		if offset > uint64(len(data)) || elfSectionHeaderMin > uint64(len(data))-offset {
			return 0, 0, false
		}
		header := data[offset : offset+elfSectionHeaderMin]
		return binary.LittleEndian.Uint64(header[24:32]), binary.LittleEndian.Uint64(header[32:40]), true
	}

	tableOffset, tableSize, ok := readSection(index)
	if !ok {
		return nil, ErrTruncated
	}
	if tableSize == 0 && index > 0 {
		tableOffset, tableSize, ok = readSection(index - 1)
		if !ok {
			return nil, ErrTruncated
		}
	}
	if tableSize == 0 {
		return nil, nil
	}
	if tableOffset > uint64(len(data)) || tableSize > uint64(len(data))-tableOffset {
		return nil, ErrTruncated
	}
	return data[tableOffset : tableOffset+tableSize], nil
}

func readImportDirectory(data []byte, layout peLayout) (uint32, uint32, bool, error) {
	optionalEnd := layout.optionalOffset + uint64(layout.optionalSize)
	if optionalEnd < layout.optionalOffset || optionalEnd > uint64(len(data)) {
		return 0, 0, false, ErrTruncated
	}

	var directoryOffset uint64
	var countOffset uint64
	switch layout.optionalMagic {
	case 0x10b:
		directoryOffset = 96
		countOffset = 92
	case 0x20b:
		directoryOffset = 112
		countOffset = 108
	default:
		return 0, 0, false, ErrUnsupportedFormat
	}
	if layout.optionalOffset > optionalEnd || countOffset+4 > optionalEnd-layout.optionalOffset {
		return 0, 0, false, ErrTruncated
	}
	directoryCount := binary.LittleEndian.Uint32(data[layout.optionalOffset+countOffset : layout.optionalOffset+countOffset+4])
	if directoryCount == 0 {
		return 0, 0, false, nil
	}

	directoryStart := layout.optionalOffset + directoryOffset
	if directoryStart > optionalEnd || 8 > optionalEnd-directoryStart {
		return 0, 0, false, ErrTruncated
	}
	rva := binary.LittleEndian.Uint32(data[directoryStart : directoryStart+4])
	size := binary.LittleEndian.Uint32(data[directoryStart+4 : directoryStart+8])
	if rva != 0 {
		return rva, size, true, nil
	}

	// Some producers place the import directory in the slot immediately
	// following the first directory. Accept it only when it looks like a
	// real import descriptor, never merely because the slot is non-zero.
	if directoryCount >= 2 && directoryStart+16 <= optionalEnd {
		alternateRVA := binary.LittleEndian.Uint32(data[directoryStart+8 : directoryStart+12])
		alternateSize := binary.LittleEndian.Uint32(data[directoryStart+12 : directoryStart+16])
		if alternateRVA != 0 && importDescriptorLooksValid(data, layout, alternateRVA) {
			return alternateRVA, alternateSize, true, nil
		}
	}
	return 0, 0, false, nil
}

func importDescriptorLooksValid(data []byte, layout peLayout, rva uint32) bool {
	entries, err := readPESectionsForImportCheck(data)
	if err != nil {
		return false
	}
	offset, ok := mapRVAToOffset(entries, rva)
	if !ok || offset > uint64(len(data)) || 20 > uint64(len(data))-offset {
		return false
	}
	descriptor := data[offset : offset+20]
	if allZero(descriptor) {
		return false
	}
	nameRVA := binary.LittleEndian.Uint32(descriptor[12:16])
	thunkRVA := binary.LittleEndian.Uint32(descriptor[0:4])
	if thunkRVA == 0 {
		thunkRVA = binary.LittleEndian.Uint32(descriptor[16:20])
	}
	if nameRVA == 0 || thunkRVA == 0 {
		return false
	}
	nameOffset, ok := mapRVAToOffset(entries, nameRVA)
	if !ok {
		return false
	}
	if _, err := readCString(data, nameOffset); err != nil {
		return false
	}
	_, ok = mapRVAToOffset(entries, thunkRVA)
	return ok
}

func readPESectionsForImportCheck(data []byte) ([]peSectionEntry, error) {
	layout, err := parsePELayout(data)
	if err != nil {
		return nil, err
	}
	tableBytes := uint64(layout.sectionCount) * peSectionSize
	if layout.sectionOffset > uint64(len(data)) || tableBytes > uint64(len(data))-layout.sectionOffset {
		return nil, ErrTruncated
	}
	entries := make([]peSectionEntry, 0, layout.sectionCount)
	for i := 0; i < int(layout.sectionCount); i++ {
		offset := layout.sectionOffset + uint64(i)*peSectionSize
		entry := data[offset : offset+peSectionSize]
		entries = append(entries, peSectionEntry{
			Section: Section{
				VirtualSize: binary.LittleEndian.Uint32(entry[8:12]),
				RawSize:     binary.LittleEndian.Uint32(entry[16:20]),
				Offset:      binary.LittleEndian.Uint32(entry[20:24]),
			},
			VirtualAddress: binary.LittleEndian.Uint32(entry[12:16]),
		})
	}
	return entries, nil
}

func (b *BinaryFingerprinter) readThunks(data []byte, sections []peSectionEntry, library string, thunkRVA uint32, pointerSize int, seen map[string]Import) error {
	thunkOffset, ok := mapRVAToOffset(sections, thunkRVA)
	if !ok {
		return ErrTruncated
	}

	for {
		if thunkOffset > uint64(len(data)) || uint64(pointerSize) > uint64(len(data))-thunkOffset {
			return ErrTruncated
		}

		var value uint64
		if pointerSize == 8 {
			value = binary.LittleEndian.Uint64(data[thunkOffset : thunkOffset+8])
		} else {
			value = uint64(binary.LittleEndian.Uint32(data[thunkOffset : thunkOffset+4]))
		}
		if value == 0 {
			break
		}

		var imp Import
		imp.Library = library
		ordinalMask := uint64(1) << 31
		if pointerSize == 8 {
			ordinalMask = uint64(1) << 63
		}
		if value&ordinalMask != 0 {
			imp.Ordinal = uint16(value)
			imp.Symbol = "#" + strconv.FormatUint(uint64(imp.Ordinal), 10)
		} else {
			nameRVA := uint32(value & 0x7fffffff)
			nameOffset, ok := mapRVAToOffset(sections, nameRVA)
			if !ok || nameOffset > uint64(len(data)) || 2 > uint64(len(data))-nameOffset {
				return ErrTruncated
			}
			symbol, err := readCString(data, nameOffset+2)
			if err != nil {
				return err
			}
			imp.Symbol = symbol
		}

		key := imp.Library + "\x00" + imp.Symbol
		if _, exists := seen[key]; !exists {
			if len(seen) >= b.opts.MaxImports {
				return ErrImportLimit
			}
			seen[key] = imp
		}
		thunkOffset += uint64(pointerSize)
	}
	return nil
}

func mapRVAToOffset(sections []peSectionEntry, rva uint32) (uint64, bool) {
	for _, section := range sections {
		size := section.VirtualSize
		if section.RawSize > size {
			size = section.RawSize
		}
		if size == 0 || rva < section.VirtualAddress {
			continue
		}
		delta := rva - section.VirtualAddress
		if uint64(delta) < uint64(size) {
			return uint64(section.Offset) + uint64(delta), true
		}
	}
	return 0, false
}

func readCString(data []byte, offset uint64) (string, error) {
	if offset >= uint64(len(data)) {
		return "", ErrTruncated
	}
	end := bytes.IndexByte(data[offset:], 0)
	if end < 0 {
		return "", ErrTruncated
	}
	return string(data[offset : offset+uint64(end)]), nil
}

func cStringInTable(table []byte, offset uint32) string {
	if uint64(offset) >= uint64(len(table)) {
		return ""
	}
	end := bytes.IndexByte(table[offset:], 0)
	if end < 0 {
		return string(table[offset:])
	}
	return string(table[offset : uint64(offset)+uint64(end)])
}

func sortSections(sections []Section) {
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Name != sections[j].Name {
			return sections[i].Name < sections[j].Name
		}
		return sections[i].RawSize > sections[j].RawSize
	})
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func clampToUint32(value uint64) uint32 {
	const maxUint32 = uint64(^uint32(0))
	if value > maxUint32 {
		return ^uint32(0)
	}
	return uint32(value)
}

func featureDigest(feature Feature) string {
	canonical := struct {
		Format   Format
		Sections []Section
		Imports  []Import
		Fuzzy    uint64
	}{
		Format:   feature.Format,
		Sections: feature.Sections,
		Imports:  feature.Imports,
		Fuzzy:    feature.Fuzzy,
	}
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}

func cloneFeature(feature Feature) Feature {
	clone := feature
	if feature.Sections != nil {
		clone.Sections = make([]Section, len(feature.Sections))
		copy(clone.Sections, feature.Sections)
	}
	if feature.Imports != nil {
		clone.Imports = make([]Import, len(feature.Imports))
		copy(clone.Imports, feature.Imports)
	}
	return clone
}
