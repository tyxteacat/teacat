package tokenizer

import (
	"strings"
	"testing"
)

func TestRenderChatML(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "how are you?"},
	}
	got := RenderTemplate("<|im_start|>", msgs, "be nice")
	want := "<|im_start|>system\nbe nice<|im_end|>\n" +
		"<|im_start|>user\nhi<|im_end|>\n" +
		"<|im_start|>assistant\nhello<|im_end|>\n" +
		"<|im_start|>user\nhow are you?<|im_end|>\n" +
		"<|im_start|>assistant\n"
	if got != want {
		t.Fatalf("chatml mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRenderLlama3(t *testing.T) {
	got := RenderTemplate("start_header_id", []Message{{Role: "user", Content: "hi"}}, "")
	if !strings.HasPrefix(got, "<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\nhi<|eot_id|>") {
		t.Fatalf("unexpected llama3 prompt: %q", got)
	}
	if !strings.HasSuffix(got, "<|start_header_id|>assistant<|end_header_id|>\n\n") {
		t.Fatalf("missing assistant header: %q", got)
	}
}

func TestRenderPlain(t *testing.T) {
	got := RenderTemplate("", []Message{{Role: "user", Content: "hi"}}, "")
	if got != "User: hi\nAssistant: " {
		t.Fatalf("plain prompt = %q", got)
	}
}
