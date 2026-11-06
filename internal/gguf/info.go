package gguf

import "strings"

// QuickInfo holds cheap summary information read from a GGUF header without
// loading tensor payloads.
type QuickInfo struct {
	Name    string
	Arch    string
	Params  int64
	Quant   string
	Layers  int
	Ctx     int
	Vocab   int
}

// ReadQuickInfo parses only metadata and the tensor table of a GGUF file.
func ReadQuickInfo(path string) (QuickInfo, error) {
	m, err := OpenMeta(path)
	if err != nil {
		return QuickInfo{}, err
	}
	var info QuickInfo
	info.Name, _ = m.String("general.name")
	info.Arch, _ = m.String("general.architecture")
	if info.Arch != "" {
		info.Layers, _ = m.Int(info.Arch + ".block_count")
		info.Ctx, _ = m.Int(info.Arch + ".context_length")
	}
	if toks, ok := m.Strings("tokenizer.ggml.tokens"); ok {
		info.Vocab = len(toks)
	}

	counts := map[uint32]int{}
	for _, t := range m.Tensors {
		info.Params += int64(t.NElements())
		if len(t.Dims) >= 2 {
			counts[t.Type]++
		}
	}
	best := 0
	for typ, n := range counts {
		if n > best {
			best = n
			info.Quant = QuantName(typ)
		}
	}
	return info, nil
}

// QuantName returns the short quantization label used in model listings.
func QuantName(t uint32) string {
	name := TypeName(t)
	return strings.TrimPrefix(name, "TYPE_")
}
