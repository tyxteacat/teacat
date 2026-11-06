// Package sampler turns logits into the next token id.
package sampler

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// Params controls token sampling.
type Params struct {
	Temperature   float32
	TopK          int
	TopP          float32
	MinP          float32
	RepeatPenalty float32
	RepeatLastN   int
	Seed          int64
}

// Default returns practical defaults similar to Ollama's.
func Default() Params {
	return Params{
		Temperature:   0.7,
		TopK:          40,
		TopP:          0.9,
		MinP:          0.0,
		RepeatPenalty: 1.1,
		RepeatLastN:   64,
		Seed:          time.Now().UnixNano(),
	}
}

// Sampler holds RNG state so a generation is reproducible with a seed.
type Sampler struct {
	P    Params
	rng  *rand.Rand
	scratch []float32
}

// New creates a sampler from params, filling zero values with defaults.
func New(p Params) *Sampler {
	d := Default()
	if p.TopK == 0 {
		p.TopK = d.TopK
	}
	if p.TopP == 0 {
		p.TopP = d.TopP
	}
	if p.RepeatPenalty == 0 {
		p.RepeatPenalty = d.RepeatPenalty
	}
	if p.RepeatLastN == 0 {
		p.RepeatLastN = d.RepeatLastN
	}
	if p.Seed == 0 {
		p.Seed = time.Now().UnixNano()
	}
	return &Sampler{P: p, rng: rand.New(rand.NewSource(p.Seed))}
}

// Sample returns the next token id given logits and recently generated tokens.
func (s *Sampler) Sample(logits []float32, recent []int) int {
	if len(logits) == 0 {
		return 0
	}
	n := len(logits)
	if cap(s.scratch) < n {
		s.scratch = make([]float32, n)
	}
	work := s.scratch[:n]
	copy(work, logits)

	if s.P.RepeatPenalty > 1 && len(recent) > 0 {
		start := 0
		if s.P.RepeatLastN > 0 && len(recent) > s.P.RepeatLastN {
			start = len(recent) - s.P.RepeatLastN
		}
		for _, id := range recent[start:] {
			if id < 0 || id >= n {
				continue
			}
			if work[id] > 0 {
				work[id] /= s.P.RepeatPenalty
			} else {
				work[id] *= s.P.RepeatPenalty
			}
		}
	}

	if s.P.Temperature <= 0 {
		best := 0
		for i := 1; i < n; i++ {
			if work[i] > work[best] {
				best = i
			}
		}
		return best
	}

	for i := range work {
		work[i] /= s.P.Temperature
	}

	// Candidate indices, sorted by logit descending, bounded by TopK.
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return work[idx[a]] > work[idx[b]] })
	if s.P.TopK > 0 && s.P.TopK < len(idx) {
		idx = idx[:s.P.TopK]
	}

	mx := work[idx[0]]
	var sum float32
	for _, i := range idx {
		sum += float32(exp(float64(work[i] - mx)))
	}
	if sum <= 0 {
		return idx[0]
	}
	type cand struct {
		id   int
		p    float32
	}
	cands := make([]cand, 0, len(idx))
	var cum float32
	for _, i := range idx {
		p := float32(exp(float64(work[i]-mx))) / sum
		if p < s.P.MinP {
			continue
		}
		cands = append(cands, cand{i, p})
		cum += p
		if cum >= s.P.TopP {
			break
		}
	}
	if len(cands) == 0 {
		return idx[0]
	}
	if cum <= 0 {
		return cands[0].id
	}
	r := s.rng.Float32() * cum
	var acc float32
	for _, c := range cands {
		acc += c.p
		if r <= acc {
			return c.id
		}
	}
	return cands[len(cands)-1].id
}

func exp(x float64) float64 { return math.Exp(x) }
