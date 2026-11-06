package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/tyxteacat/teacat/internal/engine"
	"github.com/tyxteacat/teacat/internal/gguf"
	"github.com/tyxteacat/teacat/internal/model"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

func main() {
	path := os.Args[1]
	g, err := gguf.Open(path)
	if err != nil {
		panic(err)
	}
	tok, err := tokenizer.New(g)
	if err != nil {
		panic(err)
	}
	fmt.Println("tokenizer.model =", tok.Model, " pre =", tok.Pre(), " merges =", tok.MergeCount(), " bpe =", tok.IsBPE())
	fmt.Println("bos =", tok.BOS, " eos =", tok.EOS, " addBOS =", tok.AddBOS)
	fmt.Println("tokens 0..10:", tok.Vocab[:10])
	fmt.Println("tokens 100..110:", tok.Vocab[100:110])

	tests := []string{"Hello world", "The capital of France is", "What is the capital of France?", "  spaces  and\nnewlines\t!"}
	for _, s := range tests {
		ids := tok.Encode(s, false)
		back := tok.Decode(ids)
		fmt.Printf("\n%q\n  ids  = %v\n  back = %q  roundtrip=%v\n", s, ids, back, back == s)
	}

	mm, err := model.Load(path)
	if err != nil {
		panic(err)
	}
	prompt := engine.ChatPrompt(mm, []tokenizer.Message{{Role: "user", Content: "What is the capital of France?"}}, "")
	fmt.Printf("\nprompt:\n%s\n", prompt)
	ids := engine.EncodeChatPrompt(mm, prompt)
	fmt.Printf("prompt ids (%d): %v\n", len(ids), ids)
	fmt.Printf("prompt decode: %q\n", mm.Tok.Decode(ids))

	for _, p := range []string{"The capital of France is", "2 + 2 =", "Once upon a time"} {
		ids := mm.Tok.Encode(p, false)
		seq := mm.NewSequence(256)
		var logits []float32
		for _, id := range ids {
			logits = seq.Forward(id)
		}
		type pair struct {
			id int
			v  float32
		}
		top := make([]pair, 0, len(logits))
		for i, v := range logits {
			top = append(top, pair{i, v})
		}
		sort.Slice(top, func(a, b int) bool { return top[a].v > top[b].v })
		fmt.Printf("\n%q ->\n", p)
		for i := 0; i < 8; i++ {
			fmt.Printf("   %8.3f  %6d  %q\n", top[i].v, top[i].id, mm.Tok.DecodeToken(top[i].id))
		}
		if p == "The capital of France is" {
			fmt.Printf("   greedy continuation:")
			cur := logits
			for i := 0; i < 12; i++ {
				best := 0
				for j := range cur {
					if cur[j] > cur[best] {
						best = j
					}
				}
				fmt.Printf("%s", mm.Tok.Decode([]int{best}))
				cur = seq.Forward(best)
			}
			fmt.Println()
		}
	}
}
