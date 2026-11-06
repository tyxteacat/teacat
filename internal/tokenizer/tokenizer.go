// Package tokenizer implements the two tokenizer families used by GGUF
// language models: SentencePiece (llama) and byte-level BPE (gpt2 / qwen /
// llama3 style).
package tokenizer

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/tyxteacat/teacat/internal/gguf"
)

const negInf = float32(-math.MaxFloat32)

// Token types from the GGUF spec.
const (
	typeNormal     = 1
	typeUnknown    = 2
	typeControl    = 3
	typeUserDef    = 4
	typeUnused     = 5
	typeByte       = 6
)

// Tokenizer converts between text and token ids.
type Tokenizer struct {
	Vocab  []string
	Scores []float32
	Types  []int32
	Model  string

	ids    map[string]int
	merges map[[2]string]int
	pre    string

	BOS, EOS, UNK int
	AddBOS        bool
	AddEOS        bool

	specials []specialToken
	b2u      map[byte]rune
	u2b      map[rune]byte

	byteToken map[byte]int
}

type specialToken struct {
	text string
	id   int
}

var bpeSplitRE = regexp.MustCompile(`'(?:s|t|re|ve|m|ll|d)| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+`)

// New builds a tokenizer from GGUF metadata.
func New(m *gguf.Model) (*Tokenizer, error) {
	tokens, ok := m.Strings("tokenizer.ggml.tokens")
	if !ok {
		return nil, fmt.Errorf("tokenizer: missing tokenizer.ggml.tokens metadata")
	}
	t := &Tokenizer{
		Vocab:  tokens,
		ids:    make(map[string]int, len(tokens)),
		merges: make(map[[2]string]int),
		BOS:    -1,
		EOS:    -1,
		UNK:    -1,
		byteToken: make(map[byte]int),
	}
	for i, s := range tokens {
		if _, exists := t.ids[s]; !exists {
			t.ids[s] = i
		}
	}

	t.Model, _ = m.String("tokenizer.ggml.model")
	t.pre, _ = m.String("tokenizer.ggml.pre")
	if t.Model == "" {
		t.Model = "llama"
	}

	if s, ok := m.Float32s("tokenizer.ggml.scores"); ok {
		t.Scores = s
	} else {
		t.Scores = make([]float32, len(tokens))
	}
	if ty, ok := m.Int32s("tokenizer.ggml.token_type"); ok {
		t.Types = ty
	} else {
		t.Types = make([]int32, len(tokens))
		for i := range t.Types {
			t.Types[i] = typeNormal
		}
	}

	if merges, ok := m.Strings("tokenizer.ggml.merges"); ok {
		for rank, line := range merges {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) != 2 {
				continue
			}
			t.merges[[2]string{parts[0], parts[1]}] = rank
		}
	}

	if v, ok := m.Int("tokenizer.ggml.bos_token_id"); ok {
		t.BOS = v
	}
	if v, ok := m.Int("tokenizer.ggml.eos_token_id"); ok {
		t.EOS = v
	}
	if v, ok := m.Int("tokenizer.ggml.unknown_token_id"); ok {
		t.UNK = v
	}
	if v, ok := m.Meta["tokenizer.ggml.add_bos_token"]; ok {
		if b, ok := v.(bool); ok {
			t.AddBOS = b
		}
	}
	if v, ok := m.Meta["tokenizer.ggml.add_eos_token"]; ok {
		if b, ok := v.(bool); ok {
			t.AddEOS = b
		}
	}

	for i, s := range tokens {
		ty := t.Types[i]
		if ty == typeByte || (strings.HasPrefix(s, "<0x") && strings.HasSuffix(s, ">") && len(s) == 6) {
			if ty == typeByte || t.byteToken == nil {
				var b byte
				if _, err := fmt.Sscanf(s, "<0x%02X>", &b); err == nil {
					t.byteToken[b] = i
				}
			}
		}
		if ty == typeControl || ty == typeUserDef || (strings.HasPrefix(s, "<|") && strings.HasSuffix(s, "|>")) {
			t.specials = append(t.specials, specialToken{text: s, id: i})
		}
	}
	sort.Slice(t.specials, func(i, j int) bool {
		return len(t.specials[i].text) > len(t.specials[j].text)
	})

	t.b2u, t.u2b = byteUnicodeMaps()
	return t, nil
}

// IsBPE reports whether the vocabulary uses byte-level BPE merges.
func (t *Tokenizer) IsBPE() bool {
	return len(t.merges) > 0 && (t.Model == "gpt2" || t.Model == "qwen2" || strings.HasPrefix(t.Model, "gpt"))
}

// Encode converts text to token ids. When addSpecial is true, BOS/EOS are
// added according to the model metadata.
func (t *Tokenizer) Encode(text string, addSpecial bool) []int {
	var ids []int
	if addSpecial && t.AddBOS && t.BOS >= 0 {
		ids = append(ids, t.BOS)
	}
	ids = append(ids, t.encodeText(text)...)
	if addSpecial && t.AddEOS && t.EOS >= 0 {
		ids = append(ids, t.EOS)
	}
	return ids
}

func (t *Tokenizer) encodeText(text string) []int {
	if text == "" {
		return nil
	}
	var out []int
	i := 0
	for i < len(text) {
		matched := false
		for _, sp := range t.specials {
			if strings.HasPrefix(text[i:], sp.text) {
				out = append(out, sp.id)
				i += len(sp.text)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		next := len(text)
		for _, sp := range t.specials {
			if j := strings.Index(text[i:], sp.text); j >= 0 && i+j < next {
				next = i + j
			}
		}
		seg := text[i:next]
		if t.IsBPE() {
			out = append(out, t.encodeBPE(seg)...)
		} else {
			out = append(out, t.encodeSPM(seg)...)
		}
		i = next
	}
	return out
}

func (t *Tokenizer) encodeSPM(text string) []int {
	t2 := text
	if !strings.HasPrefix(t2, " ") {
		t2 = " " + t2
	}
	t2 = strings.ReplaceAll(t2, " ", "▁")
	b := []byte(t2)
	n := len(b)
	if n == 0 {
		return nil
	}
	dp := make([]float32, n+1)
	width := make([]int32, n+1)
	tokID := make([]int32, n+1)
	for i := 1; i <= n; i++ {
		dp[i] = negInf
	}
	maxPiece := 32
	for i := 0; i < n; i++ {
		if dp[i] == negInf {
			continue
		}
		limit := maxPiece
		if n-i < limit {
			limit = n - i
		}
		for l := 1; l <= limit; l++ {
			id, ok := t.ids[string(b[i:i+l])]
			if !ok {
				continue
			}
			ty := int32(typeNormal)
			if int(id) < len(t.Types) {
				ty = t.Types[id]
			}
			if ty == typeUnused || ty == typeUnknown {
				continue
			}
			score := float32(0)
			if int(id) < len(t.Scores) {
				score = t.Scores[id]
			}
			if ty == typeByte {
				score -= 8
			}
			if dp[i]+score > dp[i+l] {
				dp[i+l] = dp[i] + score
				width[i+l] = int32(l)
				tokID[i+l] = int32(id)
			}
		}
		// Always allow a single-byte fallback so a valid path exists.
		bid, ok := t.byteToken[b[i]]
		if !ok {
			bid, ok = t.ids[fmt.Sprintf("<0x%02X>", b[i])]
		}
		if ok && dp[i]-12 > dp[i+1] {
			dp[i+1] = dp[i] - 12
			width[i+1] = 1
			tokID[i+1] = int32(bid)
		}
	}

	if dp[n] == negInf {
		return t.encodeBytes(b)
	}
	var rev []int
	for p := n; p > 0; {
		w := int(width[p])
		if w <= 0 {
			break
		}
		rev = append(rev, int(tokID[p]))
		p -= w
	}
	out := make([]int, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

func (t *Tokenizer) encodeBytes(b []byte) []int {
	out := make([]int, 0, len(b))
	for _, c := range b {
		if id, ok := t.byteToken[c]; ok {
			out = append(out, id)
		}
	}
	return out
}

func (t *Tokenizer) encodeBPE(text string) []int {
	var out []int
	for _, word := range bpeSplitRE.FindAllString(text, -1) {
		out = append(out, t.bpeWord(word)...)
	}
	return out
}

func (t *Tokenizer) bpeWord(word string) []int {
	if word == "" {
		return nil
	}
	raw := []byte(word)
	syms := make([]string, len(raw))
	for i, b := range raw {
		syms[i] = string(t.b2u[b])
	}
	for len(syms) > 1 {
		bestRank := math.MaxInt
		bestI := -1
		for i := 0; i+1 < len(syms); i++ {
			if r, ok := t.merges[[2]string{syms[i], syms[i+1]}]; ok && r < bestRank {
				bestRank = r
				bestI = i
			}
		}
		if bestI < 0 {
			break
		}
		syms[bestI] += syms[bestI+1]
		syms = append(syms[:bestI+1], syms[bestI+2:]...)
	}
	ids := make([]int, 0, len(syms))
	for _, s := range syms {
		if id, ok := t.ids[s]; ok {
			ids = append(ids, id)
			continue
		}
		// Fall back to individual byte symbols.
		for _, b := range []byte(t.decodeBPEString(s)) {
			if id, ok := t.ids[string(t.b2u[b])]; ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (t *Tokenizer) decodeBPEString(s string) string {
	var b strings.Builder
	for _, r := range s {
		if c, ok := t.u2b[r]; ok {
			b.WriteByte(c)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Decode turns token ids back into text. Control/special tokens are omitted.
func (t *Tokenizer) Decode(ids []int) string {
	var b strings.Builder
	bpe := t.IsBPE()
	for _, id := range ids {
		if id < 0 || id >= len(t.Vocab) {
			continue
		}
		ty := int32(typeNormal)
		if id < len(t.Types) {
			ty = t.Types[id]
		}
		if ty == typeControl || ty == typeUnused {
			continue
		}
		s := t.Vocab[id]
		if ty == typeByte || (strings.HasPrefix(s, "<0x") && strings.HasSuffix(s, ">") && len(s) == 6) {
			var c byte
			if _, err := fmt.Sscanf(s, "<0x%02X>", &c); err == nil {
				b.WriteByte(c)
				continue
			}
		}
		if bpe {
			b.WriteString(t.decodeBPEString(s))
		} else {
			b.WriteString(strings.ReplaceAll(s, "▁", " "))
		}
	}
	return b.String()
}

// DecodeToken returns the raw text of a single token.
func (t *Tokenizer) DecodeToken(id int) string {
	if id < 0 || id >= len(t.Vocab) {
		return ""
	}
	return t.Vocab[id]
}

// ApplyChatTemplate renders messages using a heuristic template selection
// based on the model's chat_template metadata.
func (t *Tokenizer) ApplyChatTemplate(m *gguf.Model, messages []Message, system string) string {
	tmpl, _ := m.String("tokenizer.chat_template")
	return RenderTemplate(tmpl, messages, system)
}

// Message is a chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (t *Tokenizer) Pre() string     { return t.pre }
func (t *Tokenizer) MergeCount() int { return len(t.merges) }

func byteUnicodeMaps() (map[byte]rune, map[rune]byte) {
	bs := make([]int, 0, 256)
	for b := 0x21; b <= 0x7e; b++ {
		bs = append(bs, b)
	}
	for b := 0xa1; b <= 0xac; b++ {
		bs = append(bs, b)
	}
	for b := 0xae; b <= 0xff; b++ {
		bs = append(bs, b)
	}
	cs := append([]int(nil), bs...)
	n := 0
	seen := make(map[int]bool, len(bs))
	for _, b := range bs {
		seen[b] = true
	}
	for b := 0; b < 256; b++ {
		if !seen[b] {
			bs = append(bs, b)
			cs = append(cs, 256+n)
			n++
		}
	}
	b2u := make(map[byte]rune, 256)
	u2b := make(map[rune]byte, 256)
	for i := range bs {
		b2u[byte(bs[i])] = rune(cs[i])
		u2b[rune(cs[i])] = byte(bs[i])
	}
	return b2u, u2b
}
