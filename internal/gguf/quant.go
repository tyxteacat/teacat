package gguf

import (
	"encoding/binary"
	"math"
	"runtime"
	"sync"
	"unsafe"
)

// BlockInfo describes how a ggml type packs elements.
type BlockInfo struct {
	Block int // elements per block
	Size  int // bytes per block
}

var typeTable = map[uint32]BlockInfo{
	TypeF32:     {1, 4},
	TypeF16:     {1, 2},
	TypeQ4_0:    {32, 18},
	TypeQ4_1:    {32, 20},
	TypeQ5_0:    {32, 22},
	TypeQ5_1:    {32, 24},
	TypeQ8_0:    {32, 34},
	TypeQ8_1:    {32, 36},
	TypeQ2_K:    {256, 84},
	TypeQ3_K:    {256, 110},
	TypeQ4_K:    {256, 144},
	TypeQ5_K:    {256, 176},
	TypeQ6_K:    {256, 210},
	TypeQ8_K:    {256, 292},
	TypeI8:      {1, 1},
	TypeI16:     {1, 2},
	TypeI32:     {1, 4},
	TypeI64:     {1, 8},
	TypeF64:     {1, 8},
	TypeBF16:    {1, 2},
	TypeIQ4_NL:  {32, 18},
	TypeIQ4_XS:  {256, 136},
	TypeMXFP4:   {32, 17},
}

// TypeInfo reports the block layout for a ggml type.
func TypeInfo(t uint32) (BlockInfo, bool) {
	bi, ok := typeTable[t]
	return bi, ok
}

// RowBytes is the number of bytes in one row of the tensor.
func (t *Tensor) RowBytes() int {
	bi, ok := TypeInfo(t.Type)
	if !ok {
		return 0
	}
	return t.Cols() / bi.Block * bi.Size
}

func f16tof32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	var bits uint32
	switch {
	case exp == 0 && mant == 0:
		bits = sign
	case exp == 0:
		e := uint32(127 - 15 + 1)
		for mant&0x400 == 0 {
			mant <<= 1
			e--
		}
		mant &= 0x3ff
		bits = sign | (e << 23) | (mant << 13)
	case exp == 0x1f:
		bits = sign | 0x7f800000 | (mant << 13)
	default:
		bits = sign | ((exp + 127 - 15) << 23) | (mant << 13)
	}
	return math.Float32frombits(bits)
}

func bf16tof32(h uint16) float32 {
	return math.Float32frombits(uint32(h) << 16)
}

func f32At(b []byte, i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b[i:])) }
func f16At(b []byte, i int) float32 { return f16tof32(binary.LittleEndian.Uint16(b[i:])) }

// asF32 reinterprets a little-endian F32 byte slice as float32s. Go heap
// allocations are at least 8-byte aligned, so this is safe on amd64/arm64.
func asF32(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), len(b)/4)
}

func getScaleMinK4(j int, q []byte, d, m *uint8) {
	if j < 4 {
		*d = q[j] & 63
		*m = q[j+4] & 63
	} else {
		*d = (q[j+4] & 0xF) | ((q[j-4] >> 6) << 4)
		*m = (q[j+4] >> 4) | ((q[j] >> 6) << 4)
	}
}

// dequantBlock writes the floats of one block into dst and returns the count.
func dequantBlock(t uint32, blk []byte, dst []float32) int {
	switch t {
	case TypeF32:
		dst[0] = f32At(blk, 0)
		return 1
	case TypeF16:
		dst[0] = f16At(blk, 0)
		return 1
	case TypeBF16:
		dst[0] = bf16tof32(binary.LittleEndian.Uint16(blk))
		return 1
	case TypeQ8_0:
		d := f16At(blk, 0)
		for i := 0; i < 32; i++ {
			dst[i] = d * float32(int8(blk[2+i]))
		}
		return 32
	case TypeQ4_0:
		d := f16At(blk, 0)
		for i := 0; i < 16; i++ {
			dst[i] = d * float32(int(blk[2+i]&0xF)-8)
			dst[i+16] = d * float32(int(blk[2+i]>>4)-8)
		}
		return 32
	case TypeQ4_1:
		d := f16At(blk, 0)
		m := f16At(blk, 2)
		for i := 0; i < 16; i++ {
			dst[i] = d*float32(blk[4+i]&0xF) + m
			dst[i+16] = d*float32(blk[4+i]>>4) + m
		}
		return 32
	case TypeQ5_0:
		d := f16At(blk, 0)
		qh := binary.LittleEndian.Uint32(blk[2:])
		for i := 0; i < 16; i++ {
			v0 := int(blk[6+i]&0xF) | int((qh>>uint(i))&1)<<4
			v1 := int(blk[6+i]>>4) | int((qh>>uint(i+16))&1)<<4
			dst[i] = d * float32(v0-16)
			dst[i+16] = d * float32(v1-16)
		}
		return 32
	case TypeQ5_1:
		d := f16At(blk, 0)
		m := f16At(blk, 2)
		qh := binary.LittleEndian.Uint32(blk[4:])
		for i := 0; i < 16; i++ {
			v0 := int(blk[8+i]&0xF) | int((qh>>uint(i))&1)<<4
			v1 := int(blk[8+i]>>4) | int((qh>>uint(i+16))&1)<<4
			dst[i] = d*float32(v0) + m
			dst[i+16] = d*float32(v1) + m
		}
		return 32
	case TypeQ2_K:
		d := f16At(blk, 64)
		dmin := f16At(blk, 66)
		scales := blk[0:16]
		qs := blk[16:80]
		y := 0
		xi := 0
		for n := 0; n < 256; n += 128 {
			shift := uint(0)
			for j := 0; j < 4; j++ {
				sc := scales[xi]
				xi++
				dl := d * float32(sc&0xF)
				ml := dmin * float32(sc>>4)
				for l := 0; l < 16; l++ {
					dst[y] = dl*float32((qs[l]>>shift)&3) - ml
					y++
				}
				sc = scales[xi]
				xi++
				dl = d * float32(sc&0xF)
				ml = dmin * float32(sc>>4)
				for l := 0; l < 16; l++ {
					dst[y] = dl*float32((qs[l+16]>>shift)&3) - ml
					y++
				}
				shift += 2
			}
			qs = qs[32:]
		}
		return 256
	case TypeQ3_K:
		kmask1 := uint32(0x03030303)
		kmask2 := uint32(0x0f0f0f0f)
		var aux [4]uint32
		for i := 0; i < 3; i++ {
			aux[i] = binary.LittleEndian.Uint32(blk[96+i*4:])
		}
		tmp := aux[2]
		aux[2] = ((aux[0] >> 4) & kmask2) | (((tmp >> 4) & kmask1) << 4)
		aux[3] = ((aux[1] >> 4) & kmask2) | (((tmp >> 6) & kmask1) << 4)
		aux[0] = (aux[0] & kmask2) | (((tmp) & kmask1) << 4)
		aux[1] = (aux[1] & kmask2) | (((tmp >> 2) & kmask1) << 4)
		var scales [16]int8
		for i := 0; i < 16; i++ {
			scales[i] = int8(byte(aux[i/4] >> (uint(i%4) * 8)))
		}
		dAll := f16At(blk, 108)
		hm := blk[0:32]
		qs := blk[32:96]
		y := 0
		is := 0
		m := byte(1)
		for n := 0; n < 256; n += 128 {
			shift := uint(0)
			for j := 0; j < 4; j++ {
				dl := dAll * float32(scales[is]-32)
				is++
				for l := 0; l < 16; l++ {
					q := int8((qs[l] >> shift) & 3)
					if hm[l]&m == 0 {
						q -= 4
					}
					dst[y] = dl * float32(q)
					y++
				}
				dl = dAll * float32(scales[is]-32)
				is++
				for l := 0; l < 16; l++ {
					q := int8((qs[l+16] >> shift) & 3)
					if hm[l+16]&m == 0 {
						q -= 4
					}
					dst[y] = dl * float32(q)
					y++
				}
				shift += 2
				m <<= 1
			}
			qs = qs[32:]
		}
		return 256
	case TypeQ4_K:
		d := f16At(blk, 0)
		dmin := f16At(blk, 2)
		scales := blk[4:16]
		qs := blk[16:144]
		y := 0
		is := 0
		var sc, m uint8
		for j := 0; j < 256; j += 64 {
			getScaleMinK4(is, scales, &sc, &m)
			d1 := d * float32(sc)
			m1 := dmin * float32(m)
			getScaleMinK4(is+1, scales, &sc, &m)
			d2 := d * float32(sc)
			m2 := dmin * float32(m)
			for l := 0; l < 32; l++ {
				dst[y+l] = d1*float32(qs[l]&0xF) - m1
				dst[y+32+l] = d2*float32(qs[l]>>4) - m2
			}
			y += 64
			qs = qs[32:]
			is += 2
		}
		return 256
	case TypeQ5_K:
		d := f16At(blk, 0)
		dmin := f16At(blk, 2)
		scales := blk[4:16]
		qh := blk[16:48]
		qs := blk[48:176]
		y := 0
		is := 0
		var sc, m uint8
		u1 := byte(1)
		u2 := byte(2)
		for j := 0; j < 256; j += 64 {
			getScaleMinK4(is, scales, &sc, &m)
			d1 := d * float32(sc)
			m1 := dmin * float32(m)
			getScaleMinK4(is+1, scales, &sc, &m)
			d2 := d * float32(sc)
			m2 := dmin * float32(m)
			for l := 0; l < 32; l++ {
				v0 := int(qs[l]&0xF)
				if qh[l]&u1 != 0 {
					v0 += 16
				}
				v1 := int(qs[l] >> 4)
				if qh[l]&u2 != 0 {
					v1 += 16
				}
				dst[y+l] = d1*float32(v0) - m1
				dst[y+32+l] = d2*float32(v1) - m2
			}
			y += 64
			qs = qs[32:]
			is += 2
			u1 <<= 2
			u2 <<= 2
		}
		return 256
	case TypeQ6_K:
		d := f16At(blk, 208)
		ql := blk[0:128]
		qh := blk[128:192]
		sc := blk[192:208]
		y := 0
		for n := 0; n < 256; n += 128 {
			for l := 0; l < 32; l++ {
				is := l / 16
				q1 := int8((ql[l]&0xF)|(((qh[l]>>0)&3)<<4)) - 32
				q2 := int8((ql[l+32]&0xF)|(((qh[l]>>2)&3)<<4)) - 32
				q3 := int8((ql[l]>>4)|(((qh[l]>>4)&3)<<4)) - 32
				q4 := int8((ql[l+32]>>4)|(((qh[l]>>6)&3)<<4)) - 32
				dst[y+l] = d * float32(int8(sc[is+0])) * float32(q1)
				dst[y+l+32] = d * float32(int8(sc[is+2])) * float32(q2)
				dst[y+l+64] = d * float32(int8(sc[is+4])) * float32(q3)
				dst[y+l+96] = d * float32(int8(sc[is+6])) * float32(q4)
			}
			y += 128
			ql = ql[64:]
			qh = qh[32:]
			sc = sc[8:]
		}
		return 256
	case TypeIQ4_NL:
		// IQ4_NL uses a lookup table of non-linear 4-bit values.
		d := f16At(blk, 0)
		for i := 0; i < 16; i++ {
			dst[i] = d * float32(iq4nl[blk[2+i]&0xF])
			dst[i+16] = d * float32(iq4nl[blk[2+i]>>4])
		}
		return 32
	}
	return 0
}

var iq4nl = [16]int8{-127, -104, -83, -65, -49, -35, -22, -10, 1, 13, 25, 38, 53, 69, 89, 113}

// ConvertHalfToF32 replaces F16/BF16 tensors with F32 copies. This trades
// memory for a much faster matmul path (no per-element half conversion).
func (m *Model) ConvertHalfToF32() {
	for _, t := range m.Tensors {
		if t.Type != TypeF16 && t.Type != TypeBF16 {
			continue
		}
		n := t.NElements()
		out := make([]byte, n*4)
		half := t.Type == TypeF16
		for i := 0; i < n; i++ {
			var f float32
			if half {
				f = f16At(t.Data, i*2)
			} else {
				f = bf16tof32(binary.LittleEndian.Uint16(t.Data[i*2:]))
			}
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
		}
		t.Type = TypeF32
		t.Data = out
	}
}

// DequantRow expands one row of the tensor into dst.
func (t *Tensor) DequantRow(row int, dst []float32) {
	bi, ok := TypeInfo(t.Type)
	if !ok {
		panic("gguf: unsupported tensor type " + TypeName(t.Type))
	}
	rowBytes := t.RowBytes()
	data := t.Data[row*rowBytes : (row+1)*rowBytes]
	cols := t.Cols()
	if bi.Block == 1 {
		switch t.Type {
		case TypeF32:
			copy(dst[:cols], asF32(data))
		case TypeF16:
			for i := 0; i < cols; i++ {
				dst[i] = f16At(data, i*2)
			}
		case TypeBF16:
			for i := 0; i < cols; i++ {
				dst[i] = bf16tof32(binary.LittleEndian.Uint16(data[i*2:]))
			}
		}
		return
	}
	for off, i := 0, 0; i < cols; off, i = off+bi.Size, i+bi.Block {
		dequantBlock(t.Type, data[off:off+bi.Size], dst[i:])
	}
}

// Dequantize expands the whole tensor to float32 (used for small vectors).
func (t *Tensor) Dequantize() []float32 {
	out := make([]float32, t.NElements())
	n := t.Cols()
	for r := 0; r < t.Rows(); r++ {
		t.DequantRow(r, out[r*n:(r+1)*n])
	}
	return out
}

// MatVec computes out = tensor * x, where x has Cols() entries and out has Rows().
func (t *Tensor) MatVec(x, out []float32) {
	rows, cols := t.Rows(), t.Cols()
	if len(x) < cols || len(out) < rows {
		panic("gguf: MatVec dimension mismatch")
	}
	workers := 1
	if rows*cols >= 1<<19 && rows >= 16 {
		workers = runtime.GOMAXPROCS(0)
		if workers > rows/8 {
			workers = rows / 8
		}
		if workers < 1 {
			workers = 1
		}
	}
	if workers == 1 {
		t.matVecRange(x, out, 0, rows)
		return
	}
	chunk := (rows + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < rows; start += chunk {
		end := start + chunk
		if end > rows {
			end = rows
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			t.matVecRange(x, out, s, e)
		}(start, end)
	}
	wg.Wait()
}

func (t *Tensor) matVecRange(x, out []float32, start, end int) {
	rowBytes := t.RowBytes()
	cols := t.Cols()
	switch t.Type {
	case TypeF32:
		for row := start; row < end; row++ {
			rowData := asF32(t.Data[row*rowBytes : (row+1)*rowBytes])
			var sum0, sum1, sum2, sum3 float32
			i := 0
			for ; i+4 <= cols; i += 4 {
				sum0 += rowData[i] * x[i]
				sum1 += rowData[i+1] * x[i+1]
				sum2 += rowData[i+2] * x[i+2]
				sum3 += rowData[i+3] * x[i+3]
			}
			for ; i < cols; i++ {
				sum0 += rowData[i] * x[i]
			}
			out[row] = sum0 + sum1 + sum2 + sum3
		}
	case TypeF16:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for i := 0; i < cols; i++ {
				sum += f16At(rowData, i*2) * x[i]
			}
			out[row] = sum
		}
	case TypeBF16:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for i := 0; i < cols; i++ {
				sum += bf16tof32(binary.LittleEndian.Uint16(rowData[i*2:])) * x[i]
			}
			out[row] = sum
		}
	case TypeQ8_0:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for off, i := 0, 0; i < cols; off, i = off+34, i+32 {
				d := f16At(rowData, off)
				var s float32
				for j := 0; j < 32; j++ {
					s += float32(int8(rowData[off+2+j])) * x[i+j]
				}
				sum += d * s
			}
			out[row] = sum
		}
	case TypeQ4_0:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for off, i := 0, 0; i < cols; off, i = off+18, i+32 {
				d := f16At(rowData, off)
				var s float32
				for j := 0; j < 16; j++ {
					b := rowData[off+2+j]
					s += float32(int(b&0xF)-8) * x[i+j]
					s += float32(int(b>>4)-8) * x[i+16+j]
				}
				sum += d * s
			}
			out[row] = sum
		}
	case TypeQ4_1:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for off, i := 0, 0; i < cols; off, i = off+20, i+32 {
				d := f16At(rowData, off)
				m := f16At(rowData, off+2)
				var s float32
				for j := 0; j < 16; j++ {
					b := rowData[off+4+j]
					s += (d*float32(b&0xF) + m) * x[i+j]
					s += (d*float32(b>>4) + m) * x[i+16+j]
				}
				sum += s
			}
			out[row] = sum
		}
	case TypeQ5_0:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for off, i := 0, 0; i < cols; off, i = off+22, i+32 {
				d := f16At(rowData, off)
				qh := binary.LittleEndian.Uint32(rowData[off+2:])
				var s float32
				for j := 0; j < 16; j++ {
					b := rowData[off+6+j]
					v0 := int(b&0xF) | int((qh>>uint(j))&1)<<4
					v1 := int(b>>4) | int((qh>>uint(j+16))&1)<<4
					s += float32(v0-16) * x[i+j]
					s += float32(v1-16) * x[i+16+j]
				}
				sum += d * s
			}
			out[row] = sum
		}
	case TypeQ5_1:
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			for off, i := 0, 0; i < cols; off, i = off+24, i+32 {
				d := f16At(rowData, off)
				m := f16At(rowData, off+2)
				qh := binary.LittleEndian.Uint32(rowData[off+4:])
				var s float32
				for j := 0; j < 16; j++ {
					b := rowData[off+8+j]
					v0 := int(b&0xF) | int((qh>>uint(j))&1)<<4
					v1 := int(b>>4) | int((qh>>uint(j+16))&1)<<4
					s += (d*float32(v0) + m) * x[i+j]
					s += (d*float32(v1) + m) * x[i+16+j]
				}
				sum += s
			}
			out[row] = sum
		}
	default:
		var buf [256]float32
		for row := start; row < end; row++ {
			rowData := t.Data[row*rowBytes : (row+1)*rowBytes]
			var sum float32
			bi, _ := TypeInfo(t.Type)
			for off, i := 0, 0; i < cols; off, i = off+bi.Size, i+bi.Block {
				n := dequantBlock(t.Type, rowData[off:off+bi.Size], buf[:])
				for j := 0; j < n; j++ {
					sum += buf[j] * x[i+j]
				}
			}
			out[row] = sum
		}
	}
}
