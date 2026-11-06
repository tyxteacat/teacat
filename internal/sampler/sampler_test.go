package sampler

import "testing"

func TestGreedy(t *testing.T) {
	p := Default()
	p.Temperature = 0
	s := New(p)
	logits := []float32{0.1, 5, 0.2, 1}
	if got := s.Sample(logits, nil); got != 1 {
		t.Fatalf("greedy sample = %d, want 1", got)
	}
}

func TestRepeatPenaltyPushesTokenDown(t *testing.T) {
	p := Default()
	p.Temperature = 0
	p.RepeatPenalty = 2
	s := New(p)
	logits := []float32{2, 3}
	if got := s.Sample(logits, []int{1}); got != 0 {
		t.Fatalf("with penalty sample = %d, want 0 (penalized token 1)", got)
	}
}

func TestSeedDeterministic(t *testing.T) {
	p := Default()
	p.Temperature = 1
	p.TopK = 0
	p.TopP = 1
	p.Seed = 42
	a := New(p)
	b := New(p)
	logits := []float32{1, 2, 3, 2, 1, 0.5}
	for i := 0; i < 20; i++ {
		x := a.Sample(logits, nil)
		y := b.Sample(logits, nil)
		if x != y {
			t.Fatalf("seeded samplers diverged at step %d: %d vs %d", i, x, y)
		}
	}
}
