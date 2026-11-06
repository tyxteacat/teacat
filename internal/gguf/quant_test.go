package gguf

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestF16ToF32(t *testing.T) {
	cases := []struct {
		in   uint16
		want float32
	}{
		{0x0000, 0},
		{0x3c00, 1},
		{0xc000, -2},
		{0x4000, 2},
		{0xbc00, -1},
		{0x3555, 0.33325195},
	}
	for _, c := range cases {
		if got := f16tof32(c.in); math.Abs(float64(got-c.want)) > 1e-5 {
			t.Errorf("f16tof32(%#04x) = %v, want %v", c.in, got, c.want)
		}
	}
	if !math.IsInf(float64(f16tof32(0x7c00)), 1) {
		t.Errorf("f16tof32(0x7c00) should be +Inf")
	}
}

func TestDequantQ4_0(t *testing.T) {
	// d = 1.0, all nibbles zero -> each value is -8.
	blk := make([]byte, 18)
	binary.LittleEndian.PutUint16(blk[0:], 0x3c00)
	dst := make([]float32, 32)
	n := dequantBlock(TypeQ4_0, blk, dst)
	if n != 32 {
		t.Fatalf("expected 32 values, got %d", n)
	}
	for i, v := range dst {
		if v != -8 {
			t.Fatalf("dst[%d] = %v, want -8", i, v)
		}
	}
}

func TestDequantQ8_0(t *testing.T) {
	blk := make([]byte, 34)
	binary.LittleEndian.PutUint16(blk[0:], 0x3c00) // d = 1
	blk[2] = 0x7f                                  // 127
	blk[3] = 0x80                                  // -128
	dst := make([]float32, 32)
	dequantBlock(TypeQ8_0, blk, dst)
	if dst[0] != 127 || dst[1] != -128 {
		t.Fatalf("got %v %v, want 127 -128", dst[0], dst[1])
	}
}

func TestMatVecF32(t *testing.T) {
	// 2 rows x 3 cols: [1 2 3; 4 5 6]
	buf := make([]byte, 6*4)
	for i, v := range []float32{1, 2, 3, 4, 5, 6} {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	tensor := &Tensor{Name: "t", Dims: []uint64{3, 2}, Type: TypeF32, Data: buf}
	out := make([]float32, 2)
	tensor.MatVec([]float32{1, 1, 1}, out)
	if out[0] != 6 || out[1] != 15 {
		t.Fatalf("got %v, want [6 15]", out)
	}
}

func TestConvertHalfToF32(t *testing.T) {
	data := make([]byte, 3*2)
	binary.LittleEndian.PutUint16(data[0:], 0x3c00) // 1
	binary.LittleEndian.PutUint16(data[2:], 0x4000) // 2
	binary.LittleEndian.PutUint16(data[4:], 0xc000) // -2
	m := &Model{Tensors: map[string]*Tensor{
		"t": {Name: "t", Dims: []uint64{3}, Type: TypeF16, Data: data},
	}}
	m.ConvertHalfToF32()
	term := m.Tensors["t"]
	if term.Type != TypeF32 || len(term.Data) != 12 {
		t.Fatalf("conversion failed: type=%d len=%d", term.Type, len(term.Data))
	}
	got := asF32(term.Data)
	if got[0] != 1 || got[1] != 2 || got[2] != -2 {
		t.Fatalf("got %v", got)
	}
}

func TestTypeInfo(t *testing.T) {
	if bi, ok := TypeInfo(TypeQ4_K); !ok || bi.Block != 256 || bi.Size != 144 {
		t.Fatalf("Q4_K info = %+v, ok=%v", bi, ok)
	}
	if bi, ok := TypeInfo(TypeQ6_K); !ok || bi.Block != 256 || bi.Size != 210 {
		t.Fatalf("Q6_K info = %+v, ok=%v", bi, ok)
	}
}
