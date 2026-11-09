// Package model implements a from-scratch transformer inference engine for
// GGUF models with the LLaMA architecture family (LLaMA, Qwen, SmolLM, etc).
package model

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/tyxteacat/teacat/internal/gguf"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

// Config holds the architecture parameters read from GGUF metadata.
type Config struct {
	Arch      string
	Name      string
	NLayer    int
	NEmbd     int
	NHead     int
	NHeadKV   int
	HeadDim   int
	NFF       int
	Vocab     int
	Ctx       int
	Eps       float32
	RopeBase  float32
	RopeScale float32

	QDim  int
	KVDim int
	Group int
}

// Layer holds the weights of one transformer block.
type Layer struct {
	AttnNorm *gguf.Tensor
	FFNNorm  *gguf.Tensor

	Wq, Wk, Wv, Wo *gguf.Tensor
	Bq, Bk, Bv     *gguf.Tensor

	WGate, WUp, WDown *gguf.Tensor

	attnNorm []float32
	ffnNorm  []float32
}

// Model is a loaded, ready-to-run model.
type Model struct {
	Cfg  Config
	Tok  *tokenizer.Tokenizer
	GGUF *gguf.Model

	TokEmb  *gguf.Tensor
	OutNorm *gguf.Tensor
	Out     *gguf.Tensor
	Layers  []Layer

	outNorm   []float32
	invFreq   []float32
	Path      string
	LowMemory bool

	mu sync.Mutex
}

// LoadOptions controls how a model is loaded into memory.
type LoadOptions struct {
	// LowMemory keeps F16/BF16 weights in their packed form instead of
	// expanding them to F32. This roughly halves RAM use for F16 models at the
	// cost of slower matvec reads.
	LowMemory bool
}

// Load parses a GGUF file and builds an inference model.
func Load(path string) (*Model, error) {
	return LoadWithOptions(path, LoadOptions{})
}

// LoadWithOptions parses a GGUF file and builds an inference model with the
// given memory/speed trade-off.
func LoadWithOptions(path string, opt LoadOptions) (*Model, error) {
	g, err := gguf.Open(path)
	if err != nil {
		return nil, err
	}
	tok, err := tokenizer.New(g)
	if err != nil {
		return nil, err
	}
	m := &Model{Tok: tok, GGUF: g, Path: path, Cfg: Config{}, LowMemory: opt.LowMemory}

	arch, _ := g.String("general.architecture")
	if arch == "" {
		arch = "llama"
	}
	m.Cfg.Arch = arch
	m.Cfg.Name, _ = g.String("general.name")

	m.Cfg.NLayer = metaInt(g, arch+".block_count", 0)
	m.Cfg.NEmbd = metaInt(g, arch+".embedding_length", 0)
	m.Cfg.NHead = metaInt(g, arch+".attention.head_count", 0)
	m.Cfg.NHeadKV = metaInt(g, arch+".attention.head_count_kv", m.Cfg.NHead)
	m.Cfg.NFF = metaInt(g, arch+".feed_forward_length", 0)
	m.Cfg.Ctx = metaInt(g, arch+".context_length", 2048)
	m.Cfg.Eps = float32(metaFloat(g, arch+".attention.layer_norm_rms_epsilon", 1e-5))
	m.Cfg.RopeBase = float32(metaFloat(g, arch+".rope.freq_base", 10000))
	m.Cfg.RopeScale = float32(metaFloat(g, arch+".rope.scaling.factor", 1))
	m.Cfg.Vocab = len(tok.Vocab)

	if m.Cfg.NLayer == 0 || m.Cfg.NEmbd == 0 || m.Cfg.NHead == 0 {
		return nil, fmt.Errorf("model: incomplete architecture metadata (layers=%d embd=%d heads=%d)",
			m.Cfg.NLayer, m.Cfg.NEmbd, m.Cfg.NHead)
	}
	if m.Cfg.NHeadKV == 0 {
		m.Cfg.NHeadKV = m.Cfg.NHead
	}
	m.Cfg.HeadDim = metaInt(g, arch+".attention.key_length", m.Cfg.NEmbd/m.Cfg.NHead)
	if m.Cfg.HeadDim <= 0 {
		m.Cfg.HeadDim = m.Cfg.NEmbd / m.Cfg.NHead
	}
	m.Cfg.QDim = m.Cfg.NHead * m.Cfg.HeadDim
	m.Cfg.KVDim = m.Cfg.NHeadKV * m.Cfg.HeadDim
	m.Cfg.Group = m.Cfg.NHead / m.Cfg.NHeadKV
	if m.Cfg.Group == 0 {
		m.Cfg.Group = 1
	}
	if m.Cfg.NFF == 0 {
		m.Cfg.NFF = int(float64(m.Cfg.NEmbd) * 8 / 3)
	}

	t, err := tensor(g, "token_embd.weight")
	if err != nil {
		return nil, err
	}
	m.TokEmb = t

	if t, err = tensor(g, "output_norm.weight"); err == nil {
		m.OutNorm = t
		m.outNorm = t.Dequantize()
	} else {
		m.outNorm = make([]float32, m.Cfg.NEmbd)
		for i := range m.outNorm {
			m.outNorm[i] = 1
		}
	}
	if t, err = tensor(g, "output.weight"); err == nil {
		m.Out = t
	} else {
		m.Out = m.TokEmb
	}

	m.invFreq = make([]float32, m.Cfg.HeadDim/2)
	for i := range m.invFreq {
		m.invFreq[i] = float32(1.0 / math.Pow(float64(m.Cfg.RopeBase), float64(2*i)/float64(m.Cfg.HeadDim)))
	}

	m.Layers = make([]Layer, m.Cfg.NLayer)
	for i := 0; i < m.Cfg.NLayer; i++ {
		p := fmt.Sprintf("blk.%d.", i)
		l := &m.Layers[i]
		if l.AttnNorm, err = tensor(g, p+"attn_norm.weight"); err != nil {
			return nil, err
		}
		if l.FFNNorm, err = tensor(g, p+"ffn_norm.weight"); err != nil {
			return nil, err
		}
		if l.Wq, err = tensor(g, p+"attn_q.weight"); err != nil {
			return nil, err
		}
		if l.Wk, err = tensor(g, p+"attn_k.weight"); err != nil {
			return nil, err
		}
		if l.Wv, err = tensor(g, p+"attn_v.weight"); err != nil {
			return nil, err
		}
		if l.Wo, err = tensor(g, p+"attn_output.weight"); err != nil {
			return nil, err
		}
		l.WGate, _ = optTensor(g, p+"ffn_gate.weight")
		l.WUp, _ = optTensor(g, p+"ffn_up.weight")
		if l.WDown, err = tensor(g, p+"ffn_down.weight"); err != nil {
			return nil, err
		}
		if l.WGate == nil || l.WUp == nil {
			return nil, fmt.Errorf("model: layer %d missing ffn_gate/ffn_up (non-LLaMA architecture %q not supported)", i, arch)
		}
		l.Bq, _ = optTensor(g, p+"attn_q.bias")
		l.Bk, _ = optTensor(g, p+"attn_k.bias")
		l.Bv, _ = optTensor(g, p+"attn_v.bias")
		l.attnNorm = l.AttnNorm.Dequantize()
		l.ffnNorm = l.FFNNorm.Dequantize()
	}

	if err := m.validate(); err != nil {
		return nil, err
	}
	if !opt.LowMemory {
		g.ConvertHalfToF32()
	}
	return m, nil
}

func (m *Model) validate() error {
	c := m.Cfg
	check := func(name string, t *gguf.Tensor, rows, cols int) error {
		if t.Rows() != rows || t.Cols() != cols {
			return fmt.Errorf("model: tensor %s has shape %d x %d, expected %d x %d", name, t.Rows(), t.Cols(), rows, cols)
		}
		return nil
	}
	if err := check("token_embd", m.TokEmb, c.Vocab, c.NEmbd); err != nil {
		return err
	}
	if err := check("output", m.Out, c.Vocab, c.NEmbd); err != nil {
		return err
	}
	for i := range m.Layers {
		l := &m.Layers[i]
		p := fmt.Sprintf("blk.%d", i)
		if err := check(p+".attn_q", l.Wq, c.QDim, c.NEmbd); err != nil {
			return err
		}
		if err := check(p+".attn_k", l.Wk, c.KVDim, c.NEmbd); err != nil {
			return err
		}
		if err := check(p+".attn_v", l.Wv, c.KVDim, c.NEmbd); err != nil {
			return err
		}
		if err := check(p+".attn_output", l.Wo, c.NEmbd, c.QDim); err != nil {
			return err
		}
		if err := check(p+".ffn_gate", l.WGate, c.NFF, c.NEmbd); err != nil {
			return err
		}
		if err := check(p+".ffn_up", l.WUp, c.NFF, c.NEmbd); err != nil {
			return err
		}
		if err := check(p+".ffn_down", l.WDown, c.NEmbd, c.NFF); err != nil {
			return err
		}
		for _, t := range []*gguf.Tensor{l.Wq, l.Wk, l.Wv, l.Wo, l.WGate, l.WUp, l.WDown} {
			if _, ok := gguf.TypeInfo(t.Type); !ok {
				return fmt.Errorf("model: tensor %s uses unsupported quantization %s", t.Name, gguf.TypeName(t.Type))
			}
		}
	}
	return nil
}

func metaInt(g *gguf.Model, key string, def int) int {
	if v, ok := g.Int(key); ok {
		return v
	}
	return def
}

func metaFloat(g *gguf.Model, key string, def float64) float64 {
	if v, ok := g.Float(key); ok {
		return v
	}
	return def
}

func tensor(g *gguf.Model, name string) (*gguf.Tensor, error) {
	t, ok := g.Tensors[name]
	if !ok {
		return nil, fmt.Errorf("model: missing tensor %q", name)
	}
	return t, nil
}

func optTensor(g *gguf.Model, name string) (*gguf.Tensor, bool) {
	t, ok := g.Tensors[name]
	return t, ok
}

// Info describes the model for `TeaCat show`.
func (m *Model) Info() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name:      %s\n", m.Cfg.Name)
	fmt.Fprintf(&b, "arch:      %s\n", m.Cfg.Arch)
	fmt.Fprintf(&b, "layers:    %d\n", m.Cfg.NLayer)
	fmt.Fprintf(&b, "hidden:    %d\n", m.Cfg.NEmbd)
	fmt.Fprintf(&b, "heads:     %d (kv %d, dim %d)\n", m.Cfg.NHead, m.Cfg.NHeadKV, m.Cfg.HeadDim)
	fmt.Fprintf(&b, "ffn:       %d\n", m.Cfg.NFF)
	fmt.Fprintf(&b, "vocab:     %d\n", m.Cfg.Vocab)
	fmt.Fprintf(&b, "context:   %d\n", m.Cfg.Ctx)
	fmt.Fprintf(&b, "rope base: %.1f\n", m.Cfg.RopeBase)
	return b.String()
}

// Buffers are the reusable scratch buffers for one sequence.
type Buffers struct {
	x, h, q, k, v, attn, o, gate, up []float32
	logits                           []float32
	scores                           []float32
}

// Sequence is a single generation stream with its own KV cache.
type Sequence struct {
	m    *Model
	Pos  int
	Ctx  int
	K, V [][]float32
	b    *Buffers
}

// NewSequence creates a sequence with a KV cache for up to ctx tokens.
func (m *Model) NewSequence(ctx int) *Sequence {
	if ctx <= 0 || ctx > m.Cfg.Ctx {
		ctx = m.Cfg.Ctx
	}
	c := m.Cfg
	s := &Sequence{m: m, Ctx: ctx, K: make([][]float32, c.NLayer), V: make([][]float32, c.NLayer)}
	for i := range s.K {
		s.K[i] = make([]float32, ctx*c.KVDim)
		s.V[i] = make([]float32, ctx*c.KVDim)
	}
	s.b = &Buffers{
		x:      make([]float32, c.NEmbd),
		h:      make([]float32, c.NEmbd),
		q:      make([]float32, c.QDim),
		k:      make([]float32, c.KVDim),
		v:      make([]float32, c.KVDim),
		attn:   make([]float32, c.QDim),
		o:      make([]float32, c.NEmbd),
		gate:   make([]float32, c.NFF),
		up:     make([]float32, c.NFF),
		logits: make([]float32, c.Vocab),
		scores: make([]float32, ctx),
	}
	return s
}

// Reset clears the KV cache so the sequence can be reused.
func (s *Sequence) Reset() {
	s.Pos = 0
}

// Forward runs one token through the network at the current position and
// returns the logits for the next token.
func (s *Sequence) Forward(token int) []float32 {
	m := s.m
	c := m.Cfg
	b := s.b
	if s.Pos >= s.Ctx {
		return nil
	}
	pos := s.Pos

	m.TokEmb.DequantRow(token, b.x)

	for li := range m.Layers {
		l := &m.Layers[li]
		rmsnorm(b.h, b.x, l.attnNorm, c.Eps)

		l.Wq.MatVec(b.h, b.q)
		l.Wk.MatVec(b.h, b.k)
		l.Wv.MatVec(b.h, b.v)
		if l.Bq != nil {
			addBias(b.q, l.Bq)
		}
		if l.Bk != nil {
			addBias(b.k, l.Bk)
		}
		if l.Bv != nil {
			addBias(b.v, l.Bv)
		}

		rope(b.q, pos, c.NHead, c.HeadDim, m.invFreq)
		rope(b.k, pos, c.NHeadKV, c.HeadDim, m.invFreq)

		cache := s.K[li]
		copy(cache[pos*c.KVDim:(pos+1)*c.KVDim], b.k)
		cacheV := s.V[li]
		copy(cacheV[pos*c.KVDim:(pos+1)*c.KVDim], b.v)

		attention(b, s, c, pos, cache, cacheV)

		l.Wo.MatVec(b.attn, b.o)
		add(b.x, b.o)

		rmsnorm(b.h, b.x, l.ffnNorm, c.Eps)
		l.WGate.MatVec(b.h, b.gate)
		l.WUp.MatVec(b.h, b.up)
		for i := range b.gate {
			g := b.gate[i]
			b.gate[i] = g / (1 + expf(-g)) * b.up[i]
		}
		l.WDown.MatVec(b.gate, b.o)
		add(b.x, b.o)
	}
	s.Pos++

	rmsnorm(b.h, b.x, m.outNorm, c.Eps)
	m.Out.MatVec(b.h, b.logits)
	return b.logits
}

func attention(b *Buffers, s *Sequence, c Config, pos int, keys, vals []float32) {
	scale := float32(1.0 / math.Sqrt(float64(c.HeadDim)))
	for hq := 0; hq < c.NHead; hq++ {
		kvh := hq / c.Group
		q := b.q[hq*c.HeadDim : (hq+1)*c.HeadDim]
		sc := b.scores[:pos+1]
		for j := 0; j <= pos; j++ {
			k := keys[j*c.KVDim+kvh*c.HeadDim : j*c.KVDim+(kvh+1)*c.HeadDim]
			var dot float32
			for d := 0; d < c.HeadDim; d++ {
				dot += q[d] * k[d]
			}
			sc[j] = dot * scale
		}
		softmax(sc)
		out := b.attn[hq*c.HeadDim : (hq+1)*c.HeadDim]
		for d := range out {
			out[d] = 0
		}
		for j := 0; j <= pos; j++ {
			p := sc[j]
			v := vals[j*c.KVDim+kvh*c.HeadDim : j*c.KVDim+(kvh+1)*c.HeadDim]
			for d := 0; d < c.HeadDim; d++ {
				out[d] += p * v[d]
			}
		}
	}
}

func rmsnorm(dst, x, w []float32, eps float32) {
	var ss float32
	for _, v := range x {
		ss += v * v
	}
	scale := 1 / sqrtf(ss/float32(len(x))+eps)
	for i := range x {
		dst[i] = x[i] * scale * w[i]
	}
}

func rope(v []float32, pos, heads, headDim int, invFreq []float32) {
	half := headDim / 2
	for h := 0; h < heads; h++ {
		base := h * headDim
		for i := 0; i < half; i++ {
			angle := float32(pos) * invFreq[i]
			cosA, sinA := cosf(angle), sinf(angle)
			x0 := v[base+i]
			x1 := v[base+i+half]
			v[base+i] = x0*cosA - x1*sinA
			v[base+i+half] = x0*sinA + x1*cosA
		}
	}
}

func softmax(v []float32) {
	if len(v) == 0 {
		return
	}
	mx := v[0]
	for _, x := range v {
		if x > mx {
			mx = x
		}
	}
	var sum float32
	for i, x := range v {
		e := expf(x - mx)
		v[i] = e
		sum += e
	}
	if sum == 0 {
		return
	}
	inv := 1 / sum
	for i := range v {
		v[i] *= inv
	}
}

func add(dst, src []float32) {
	for i := range src {
		dst[i] += src[i]
	}
}

func addBias(dst []float32, t *gguf.Tensor) {
	bias := t.Dequantize()
	for i := range dst {
		dst[i] += bias[i]
	}
}
