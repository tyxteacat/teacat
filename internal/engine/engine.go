// Package engine drives token-by-token generation on top of a loaded model.
package engine

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tyxteacat/teacat/internal/model"
	"github.com/tyxteacat/teacat/internal/sampler"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

// Options controls one generation.
type Options struct {
	NumCtx     int
	NumPredict int
	Stop       []string
	Sampler    sampler.Params
}

// Stats reports what happened during a generation.
type Stats struct {
	PromptTokens   int
	EvalTokens     int
	PromptDuration time.Duration
	EvalDuration   time.Duration
	StopReason     string
	Text           string
}

// ChatPrompt renders chat messages with the model's template.
func ChatPrompt(m *model.Model, messages []tokenizer.Message, system string) string {
	tmpl := ""
	if m.GGUF != nil {
		tmpl, _ = m.GGUF.String("tokenizer.chat_template")
	}
	return tokenizer.RenderTemplate(tmpl, messages, system)
}

// HasChatTemplate reports whether the model ships a chat template.
func HasChatTemplate(m *model.Model) bool {
	if m.GGUF == nil {
		return false
	}
	tmpl, ok := m.GGUF.String("tokenizer.chat_template")
	return ok && strings.TrimSpace(tmpl) != ""
}

// EncodeChatPrompt tokenizes a rendered chat prompt, adding BOS only when the
// template did not already do so.
func EncodeChatPrompt(m *model.Model, prompt string) []int {
	addSpecial := !HasChatTemplate(m)
	return m.Tok.Encode(prompt, addSpecial)
}

// Generate runs inference over prompt tokens and calls emit for each text
// chunk. If emit returns false generation stops. It is safe to call from a
// single goroutine per loaded model.
func Generate(ctx context.Context, m *model.Model, prompt []int, opt Options, emit func(string) bool) (Stats, error) {
	if opt.NumCtx <= 0 || opt.NumCtx > m.Cfg.Ctx {
		opt.NumCtx = m.Cfg.Ctx
	}
	if len(prompt) >= opt.NumCtx {
		// Keep the most recent context.
		prompt = prompt[len(prompt)-opt.NumCtx+1:]
	}
	var stats Stats
	seq := m.NewSequence(opt.NumCtx)

	start := time.Now()
	var logits []float32
	for _, id := range prompt {
		logits = seq.Forward(id)
	}
	stats.PromptTokens = len(prompt)
	stats.PromptDuration = time.Since(start)
	if logits == nil {
		return stats, nil
	}

	smp := sampler.New(opt.Sampler)
	recent := make([]int, 0, len(prompt)+opt.NumPredict)
	recent = append(recent, prompt...)

	maxNew := opt.NumPredict
	if maxNew <= 0 {
		maxNew = opt.NumCtx - len(prompt)
	}
	if maxNew < 0 {
		maxNew = 0
	}

	stream := &tokenStream{}
	var generated strings.Builder
	stats.StopReason = "length"
	evalStart := time.Now()
	stopped := false

	for i := 0; i < maxNew; i++ {
		if err := ctx.Err(); err != nil {
			stats.StopReason = "cancelled"
			stopped = true
			break
		}
		id := smp.Sample(logits, recent)
		if id == m.Tok.EOS {
			stats.StopReason = "stop"
			stopped = true
			break
		}
		recent = append(recent, id)
		stats.EvalTokens++

		chunk := stream.push(m.Tok.Decode([]int{id}))
		if chunk != "" {
			generated.WriteString(chunk)
			stats.Text += chunk
			if emit != nil && !emit(chunk) {
				stats.StopReason = "cancelled"
				stopped = true
				break
			}
		}
		if containsStop(generated.String(), opt.Stop) {
			stats.StopReason = "stop"
			stopped = true
			break
		}
		if seq.Pos >= opt.NumCtx {
			stats.StopReason = "length"
			stopped = true
			break
		}
		logits = seq.Forward(id)
	}
	stats.EvalDuration = time.Since(evalStart)

	if tail := stream.flush(); tail != "" && !stopped {
		stats.Text += tail
		if emit != nil {
			emit(tail)
		}
	}
	return stats, nil
}

func containsStop(text string, stops []string) bool {
	for _, s := range stops {
		if s != "" && strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// tokenStream buffers incomplete UTF-8 sequences between token decodes.
type tokenStream struct {
	pending []byte
}

func (t *tokenStream) push(s string) string {
	t.pending = append(t.pending, []byte(s)...)
	cut := len(t.pending)
	for i := len(t.pending) - 1; i >= 0 && i >= len(t.pending)-4; i-- {
		if utf8.RuneStart(t.pending[i]) {
			if !utf8.FullRune(t.pending[i:]) {
				cut = i
			}
			break
		}
	}
	out := string(t.pending[:cut])
	t.pending = append([]byte(nil), t.pending[cut:]...)
	return out
}

func (t *tokenStream) flush() string {
	out := string(t.pending)
	t.pending = nil
	return out
}
