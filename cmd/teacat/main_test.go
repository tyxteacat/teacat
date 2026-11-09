package main

import (
	"reflect"
	"testing"
)

func TestFlexibleArgs(t *testing.T) {
	values := map[string]bool{"-f": true, "--file": true}
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"name:tag", "-f", "model.gguf"}, []string{"-f", "model.gguf", "name:tag"}},
		{[]string{"-f", "model.gguf", "name:tag"}, []string{"-f", "model.gguf", "name:tag"}},
		{[]string{"name:tag", "--file", "my model.gguf"}, []string{"--file", "my model.gguf", "name:tag"}},
		{[]string{"name:tag"}, []string{"name:tag"}},
	}
	for _, c := range cases {
		if got := flexibleArgs(c.in, values); !reflect.DeepEqual(got, c.want) {
			t.Errorf("flexibleArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
