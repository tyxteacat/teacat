package store

import "testing"

func TestSplitRef(t *testing.T) {
	cases := map[string][2]string{
		"smollm2:135m":     {"smollm2", "135m"},
		"smollm2":          {"smollm2", "latest"},
		"user/model:v1":    {"user/model", "v1"},
		"user/model":       {"user/model", "latest"},
		"host:5000/m:tag":  {"host:5000/m", "tag"},
	}
	for in, want := range cases {
		name, tag := SplitRef(in)
		if name != want[0] || tag != want[1] {
			t.Errorf("SplitRef(%q) = %q,%q want %q,%q", in, name, tag, want[0], want[1])
		}
	}
}
