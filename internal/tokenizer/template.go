package tokenizer

import "strings"

// RenderTemplate renders a chat conversation into a prompt string using the
// model's chat template. GGUF stores templates as Jinja source; rather than
// implementing a full Jinja engine we detect the common families (ChatML,
// Llama 3, Llama 2, Phi-3, Gemma) and fall back to a plain transcript.
func RenderTemplate(tmpl string, messages []Message, system string) string {
	if system != "" && (len(messages) == 0 || messages[0].Role != "system") {
		messages = append([]Message{{Role: "system", Content: system}}, messages...)
	}

	switch {
	case strings.Contains(tmpl, "<|im_start|>") || strings.Contains(tmpl, "im_start"):
		return renderChatML(messages)
	case strings.Contains(tmpl, "start_header_id"):
		return renderLlama3(messages)
	case strings.Contains(tmpl, "[INST]"):
		return renderLlama2(messages)
	case strings.Contains(tmpl, "<|user|>"):
		return renderPhi3(messages)
	case strings.Contains(tmpl, "<start_of_turn>"):
		return renderGemma(messages)
	case strings.Contains(tmpl, "<|turn>"):
		return renderCommandR(messages)
	}
	return renderPlain(messages)
}

func renderChatML(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("<|im_start|>")
		b.WriteString(m.Role)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("<|im_end|>\n")
	}
	b.WriteString("<|im_start|>assistant\n")
	return b.String()
}

func renderLlama3(msgs []Message) string {
	var b strings.Builder
	b.WriteString("<|begin_of_text|>")
	for _, m := range msgs {
		b.WriteString("<|start_header_id|>")
		b.WriteString(m.Role)
		b.WriteString("<|end_header_id|>\n\n")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("<|eot_id|>")
	}
	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	return b.String()
}

func renderLlama2(msgs []Message) string {
	var b strings.Builder
	system := ""
	var turns []Message
	for _, m := range msgs {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		turns = append(turns, m)
	}
	first := true
	for i := 0; i < len(turns); i++ {
		m := turns[i]
		if m.Role != "user" {
			continue
		}
		b.WriteString("<s>[INST] ")
		if first {
			if system != "" {
				b.WriteString("<<SYS>>\n")
				b.WriteString(system)
				b.WriteString("\n<</SYS>>\n\n")
			}
			first = false
		}
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString(" [/INST]")
		if i+1 < len(turns) && turns[i+1].Role == "assistant" {
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(turns[i+1].Content))
			b.WriteString(" </s>")
			i++
		}
	}
	return b.String()
}

func renderPhi3(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("<|")
		b.WriteString(m.Role)
		b.WriteString("|>\n")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("<|end|>\n")
	}
	b.WriteString("<|assistant|>\n")
	return b.String()
}

func renderGemma(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("<start_of_turn>user\n")
				b.WriteString(strings.TrimSpace(m.Content))
				b.WriteString("<end_of_turn>\n")
			}
			continue
		}
		b.WriteString("<start_of_turn>")
		b.WriteString(role)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("<end_of_turn>\n")
	}
	b.WriteString("<start_of_turn>model\n")
	return b.String()
}

func renderCommandR(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("<|")
		b.WriteString(m.Role)
		b.WriteString("|>")
		b.WriteString(strings.TrimSpace(m.Content))
	}
	b.WriteString("<|assistant|>")
	return b.String()
}

func renderPlain(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "system":
			b.WriteString(strings.TrimSpace(m.Content))
			b.WriteString("\n\n")
		case "user":
			b.WriteString("User: ")
			b.WriteString(strings.TrimSpace(m.Content))
			b.WriteString("\n")
		case "assistant":
			b.WriteString("Assistant: ")
			b.WriteString(strings.TrimSpace(m.Content))
			b.WriteString("\n")
		}
	}
	b.WriteString("Assistant: ")
	return b.String()
}
