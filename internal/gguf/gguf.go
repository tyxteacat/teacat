// Package gguf implements a reader for the GGUF model file format.
//
// GGUF is the container used by llama.cpp and Ollama. It stores a header,
// a key/value metadata block and a list of tensors, each of which may be
// stored in a quantized format.
package gguf

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// Magic is the little-endian "GGUF" marker at the start of every file.
const Magic uint32 = 0x46554747

// Tensor type identifiers (ggml_type).
const (
	TypeF32      uint32 = 0
	TypeF16      uint32 = 1
	TypeQ4_0     uint32 = 2
	TypeQ4_1     uint32 = 3
	TypeQ5_0     uint32 = 6
	TypeQ5_1     uint32 = 7
	TypeQ8_0     uint32 = 8
	TypeQ8_1     uint32 = 9
	TypeQ2_K     uint32 = 10
	TypeQ3_K     uint32 = 11
	TypeQ4_K     uint32 = 12
	TypeQ5_K     uint32 = 13
	TypeQ6_K     uint32 = 14
	TypeQ8_K     uint32 = 15
	TypeIQ2_XXS  uint32 = 16
	TypeIQ2_XS   uint32 = 17
	TypeIQ3_XXS  uint32 = 18
	TypeIQ1_S    uint32 = 19
	TypeIQ4_NL   uint32 = 20
	TypeIQ3_S    uint32 = 21
	TypeIQ2_S    uint32 = 22
	TypeIQ4_XS   uint32 = 23
	TypeI8       uint32 = 24
	TypeI16      uint32 = 25
	TypeI32      uint32 = 26
	TypeI64      uint32 = 27
	TypeF64      uint32 = 28
	TypeIQ1_M    uint32 = 29
	TypeBF16     uint32 = 30
	TypeTQ1_0    uint32 = 34
	TypeTQ2_0    uint32 = 35
	TypeMXFP4    uint32 = 39
)

// Metadata value types.
const (
	metaUint8   = 0
	metaInt8    = 1
	metaUint16  = 2
	metaInt16   = 3
	metaUint32  = 4
	metaInt32   = 5
	metaFloat32 = 6
	metaBool    = 7
	metaString  = 8
	metaArray   = 9
	metaUint64  = 10
	metaInt64   = 11
	metaFloat64 = 12
)

// Tensor is a single named tensor inside a GGUF file. Dims follow the GGUF
// convention: Dims[0] is the fastest-varying (row length) dimension.
type Tensor struct {
	Name   string
	Dims   []uint64
	Type   uint32
	Offset uint64
	Data   []byte
}

// NElements returns the total number of scalar elements in the tensor.
func (t *Tensor) NElements() int {
	n := 1
	for _, d := range t.Dims {
		n *= int(d)
	}
	return n
}

// Cols is the row length (contiguous dimension).
func (t *Tensor) Cols() int {
	if len(t.Dims) == 0 {
		return 0
	}
	return int(t.Dims[0])
}

// Rows is the number of rows, i.e. everything after the first dimension.
func (t *Tensor) Rows() int {
	n := 1
	for _, d := range t.Dims[1:] {
		n *= int(d)
	}
	return n
}

// Model is a parsed GGUF file.
type Model struct {
	Version   uint32
	Meta      map[string]any
	Tensors   map[string]*Tensor
	Alignment int
	Path      string
	FileSize  int64
}

type binReader struct {
	r   *bufio.Reader
	off int64
}

func (b *binReader) read(p []byte) error {
	n, err := io.ReadFull(b.r, p)
	b.off += int64(n)
	return err
}

func (b *binReader) bytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if err := b.read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (b *binReader) u8() (uint8, error) {
	var v uint8
	err := binary.Read(b.r, binary.LittleEndian, &v)
	b.off++
	return v, err
}

func (b *binReader) i8() (int8, error) {
	v, err := b.u8()
	return int8(v), err
}

func (b *binReader) u16() (uint16, error) {
	buf, err := b.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(buf), nil
}

func (b *binReader) i16() (int16, error) {
	v, err := b.u16()
	return int16(v), err
}

func (b *binReader) u32() (uint32, error) {
	buf, err := b.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf), nil
}

func (b *binReader) i32() (int32, error) {
	v, err := b.u32()
	return int32(v), err
}

func (b *binReader) u64() (uint64, error) {
	buf, err := b.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf), nil
}

func (b *binReader) i64() (int64, error) {
	v, err := b.u64()
	return int64(v), err
}

func (b *binReader) f32() (float32, error) {
	v, err := b.u32()
	return math.Float32frombits(v), err
}

func (b *binReader) f64() (float64, error) {
	v, err := b.u64()
	return math.Float64frombits(v), err
}

func (b *binReader) str() (string, error) {
	n, err := b.u64()
	if err != nil {
		return "", err
	}
	if n > 1<<32 {
		return "", fmt.Errorf("gguf: string too large (%d)", n)
	}
	buf, err := b.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func (b *binReader) value(vtype uint32) (any, error) {
	switch vtype {
	case metaUint8:
		return b.u8()
	case metaInt8:
		return b.i8()
	case metaUint16:
		return b.u16()
	case metaInt16:
		return b.i16()
	case metaUint32:
		return b.u32()
	case metaInt32:
		return b.i32()
	case metaFloat32:
		return b.f32()
	case metaBool:
		v, err := b.u8()
		return v != 0, err
	case metaString:
		return b.str()
	case metaArray:
		elemType, err := b.u32()
		if err != nil {
			return nil, err
		}
		n, err := b.u64()
		if err != nil {
			return nil, err
		}
		if n > 1<<32 {
			return nil, fmt.Errorf("gguf: array too large (%d)", n)
		}
		arr := make([]any, int(n))
		for i := range arr {
			arr[i], err = b.value(elemType)
			if err != nil {
				return nil, err
			}
		}
		return arr, nil
	case metaUint64:
		return b.u64()
	case metaInt64:
		return b.i64()
	case metaFloat64:
		return b.f64()
	default:
		return nil, fmt.Errorf("gguf: unknown metadata value type %d", vtype)
	}
}

// Open parses the GGUF file at path and loads all tensor data into memory.
func Open(path string) (*Model, error) {
	return open(path, true)
}

// OpenMeta parses header and metadata only, skipping tensor payloads. This is
// fast even for multi-gigabyte files.
func OpenMeta(path string) (*Model, error) {
	return open(path, false)
}

func open(path string, loadData bool) (*Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}

	br := bufio.NewReaderSize(f, 1<<20)
	r := &binReader{r: br}

	magic, err := r.u32()
	if err != nil {
		return nil, fmt.Errorf("gguf: reading magic: %w", err)
	}
	if magic != Magic {
		return nil, fmt.Errorf("gguf: bad magic 0x%08x (not a GGUF file)", magic)
	}

	version, err := r.u32()
	if err != nil {
		return nil, err
	}
	if version < 2 || version > 3 {
		return nil, fmt.Errorf("gguf: unsupported version %d", version)
	}

	nTensors, err := r.u64()
	if err != nil {
		return nil, err
	}
	nKV, err := r.u64()
	if err != nil {
		return nil, err
	}
	if nKV > 1<<24 || nTensors > 1<<24 {
		return nil, fmt.Errorf("gguf: implausible counts (tensors=%d kv=%d)", nTensors, nKV)
	}

	m := &Model{
		Version: version,
		Meta:    make(map[string]any, nKV),
		Tensors: make(map[string]*Tensor, nTensors),
		Path:    path,
		FileSize: st.Size(),
	}

	for i := uint64(0); i < nKV; i++ {
		key, err := r.str()
		if err != nil {
			return nil, fmt.Errorf("gguf: reading metadata key: %w", err)
		}
		vtype, err := r.u32()
		if err != nil {
			return nil, err
		}
		val, err := r.value(vtype)
		if err != nil {
			return nil, fmt.Errorf("gguf: reading metadata %q: %w", key, err)
		}
		m.Meta[key] = val
	}

	infos := make([]*Tensor, 0, nTensors)
	for i := uint64(0); i < nTensors; i++ {
		name, err := r.str()
		if err != nil {
			return nil, err
		}
		nd, err := r.u32()
		if err != nil {
			return nil, err
		}
		if nd == 0 || nd > 8 {
			return nil, fmt.Errorf("gguf: tensor %q has invalid dimension count %d", name, nd)
		}
		dims := make([]uint64, nd)
		for d := range dims {
			dims[d], err = r.u64()
			if err != nil {
				return nil, err
			}
		}
		ttype, err := r.u32()
		if err != nil {
			return nil, err
		}
		offset, err := r.u64()
		if err != nil {
			return nil, err
		}
		infos = append(infos, &Tensor{Name: name, Dims: dims, Type: ttype, Offset: offset})
	}

	m.Alignment = 32
	if v, ok := m.Meta["general.alignment"]; ok {
		if n, ok := toInt(v); ok && n >= 1 {
			m.Alignment = n
		}
	}

	dataStart := alignUp(r.off, int64(m.Alignment))
	if dataStart > st.Size() {
		return nil, fmt.Errorf("gguf: tensor data starts past end of file")
	}

	for _, t := range infos {
		nbytes, err := tensorByteSize(t)
		if err != nil {
			return nil, err
		}
		start := dataStart + int64(t.Offset)
		if start < 0 || start+int64(nbytes) > st.Size() {
			return nil, fmt.Errorf("gguf: tensor %q data out of bounds", t.Name)
		}
		if loadData {
			t.Data = make([]byte, nbytes)
			if _, err := f.ReadAt(t.Data, start); err != nil {
				return nil, fmt.Errorf("gguf: reading tensor %q: %w", t.Name, err)
			}
		}
		m.Tensors[t.Name] = t
	}

	return m, nil
}

func alignUp(n, a int64) int64 {
	return (n + a - 1) / a * a
}

func tensorByteSize(t *Tensor) (int, error) {
	info, ok := TypeInfo(t.Type)
	if !ok {
		return 0, fmt.Errorf("gguf: tensor %q has unsupported type %s (%d)", t.Name, TypeName(t.Type), t.Type)
	}
	cols := t.Cols()
	if cols%info.Block != 0 {
		return 0, fmt.Errorf("gguf: tensor %q cols %d not divisible by block %d", t.Name, cols, info.Block)
	}
	rowBytes := cols / info.Block * info.Size
	return rowBytes * t.Rows(), nil
}

// Uint returns a metadata value coerced to uint64.
func (m *Model) Uint(key string) (uint64, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return 0, false
	}
	n, ok := toInt(v)
	return uint64(n), ok
}

// Int returns a metadata value coerced to int.
func (m *Model) Int(key string) (int, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return 0, false
	}
	return toInt(v)
}

// Float returns a metadata value coerced to float64.
func (m *Model) Float(key string) (float64, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint8:
		return float64(x), true
	case int8:
		return float64(x), true
	}
	return 0, false
}

// String returns a metadata string value.
func (m *Model) String(key string) (string, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// Strings returns a metadata array coerced to a string slice.
func (m *Model) Strings(key string) ([]string, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// Float32s returns a metadata array coerced to []float32.
func (m *Model) Float32s(key string) ([]float32, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]float32, len(arr))
	for i, e := range arr {
		switch x := e.(type) {
		case float32:
			out[i] = x
		case float64:
			out[i] = float32(x)
		default:
			return nil, false
		}
	}
	return out, true
}

// Int32s returns a metadata array coerced to []int32.
func (m *Model) Int32s(key string) ([]int32, bool) {
	v, ok := m.Meta[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]int32, len(arr))
	for i, e := range arr {
		n, ok := toInt(e)
		if !ok {
			return nil, false
		}
		out[i] = int32(n)
	}
	return out, true
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case uint8:
		return int(x), true
	case int8:
		return int(x), true
	case uint16:
		return int(x), true
	case int16:
		return int(x), true
	case uint32:
		return int(x), true
	case int32:
		return int(x), true
	case uint64:
		return int(x), true
	case int64:
		return int(x), true
	case int:
		return x, true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

// TypeName returns the human readable ggml type name.
func TypeName(t uint32) string {
	switch t {
	case TypeF32:
		return "F32"
	case TypeF16:
		return "F16"
	case TypeQ4_0:
		return "Q4_0"
	case TypeQ4_1:
		return "Q4_1"
	case TypeQ5_0:
		return "Q5_0"
	case TypeQ5_1:
		return "Q5_1"
	case TypeQ8_0:
		return "Q8_0"
	case TypeQ2_K:
		return "Q2_K"
	case TypeQ3_K:
		return "Q3_K"
	case TypeQ4_K:
		return "Q4_K"
	case TypeQ5_K:
		return "Q5_K"
	case TypeQ6_K:
		return "Q6_K"
	case TypeBF16:
		return "BF16"
	case TypeIQ4_NL:
		return "IQ4_NL"
	case TypeIQ4_XS:
		return "IQ4_XS"
	case TypeMXFP4:
		return "MXFP4"
	}
	return fmt.Sprintf("type(%d)", t)
}

// Summary returns a short description of the model architecture.
func (m *Model) Summary() string {
	arch, _ := m.String("general.architecture")
	name, _ := m.String("general.name")
	layers, _ := m.Int(arch + ".block_count")
	embd, _ := m.Int(arch + ".embedding_length")
	return fmt.Sprintf("%s (%s): %d layers, %d hidden", name, arch, layers, embd)
}

// SortedTensorNames returns tensor names sorted alphabetically (debug helper).
func (m *Model) SortedTensorNames() []string {
	names := make([]string, 0, len(m.Tensors))
	for n := range m.Tensors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MetadataKeys returns metadata keys sorted alphabetically.
func (m *Model) MetadataKeys() []string {
	keys := make([]string, 0, len(m.Meta))
	for k := range m.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Describe prints a human readable dump of metadata and tensors.
func (m *Model) Describe(w io.Writer) {
	fmt.Fprintf(w, "GGUF v%d  %s\n", m.Version, m.Path)
	for _, k := range m.MetadataKeys() {
		if strings.HasSuffix(k, ".tokens") || strings.HasSuffix(k, ".merges") || k == "tokenizer.chat_template" {
			continue
		}
		fmt.Fprintf(w, "  %-55s %v\n", k, m.Meta[k])
	}
	fmt.Fprintf(w, "  tensors: %d\n", len(m.Tensors))
}
