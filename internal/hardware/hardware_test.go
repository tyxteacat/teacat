package hardware

import "testing"

func TestNeedBytes(t *testing.T) {
	const size = uint64(1000 << 20)
	cases := []struct {
		quant string
		want  uint64
	}{
		{"F16", size * 2},
		{"BF16", size * 2},
		{"", size * 2},
		{"Q4_K", size},
		{"Q5_0", size},
	}
	for _, c := range cases {
		got := NeedBytes(size, c.quant)
		if got < c.want {
			t.Errorf("NeedBytes(%q) = %d, want at least %d", c.quant, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		512:             "512 B",
		1024:            "1.0 KB",
		1536:            "1.5 KB",
		2 << 20:         "2.0 MB",
		uint64(3) << 30: "3.0 GB",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
