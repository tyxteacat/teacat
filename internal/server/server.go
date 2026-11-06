// Package server exposes a local HTTP API compatible with Ollama's REST API
// and the OpenAI chat completions API.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tyxteacat/teacat/internal/brand"
	"github.com/tyxteacat/teacat/internal/engine"
	"github.com/tyxteacat/teacat/internal/model"
	"github.com/tyxteacat/teacat/internal/registry"
	"github.com/tyxteacat/teacat/internal/sampler"
	"github.com/tyxteacat/teacat/internal/store"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

// Server is the local inference daemon.
type Server struct {
	Store   *store.Store
	Ctx     int
	Version string
	Logf    func(format string, args ...any)

	mu       sync.Mutex
	ref      string
	model    *model.Model
	manifest store.Manifest
	loadedAt time.Time
}

// Handler builds the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/show", s.handleShow)
	mux.HandleFunc("/api/pull", s.handlePull)
	mux.HandleFunc("/api/delete", s.handleDelete)
	mux.HandleFunc("/api/ps", s.handlePS)
	mux.HandleFunc("/api/generate", s.handleGenerate)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/v1/models", s.handleOpenAIModels)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChat)
	return withCORS(mux)
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "%s is running.\n", brand.Name)
	fmt.Fprintf(w, "%s\n%s\n\n", brand.Tagline, brand.Repo)
	fmt.Fprint(w, "API:\n  GET  /api/tags\n  POST /api/chat\n  POST /api/generate\n  POST /v1/chat/completions\n")
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.Version})
}

// ---- model lifecycle -------------------------------------------------------

func (s *Server) resolveRef(ref string) (store.Manifest, error) {
	if ref != "" {
		return s.Store.Get(ref)
	}
	models, err := s.Store.List()
	if err != nil || len(models) == 0 {
		return store.Manifest{}, fmt.Errorf("no models installed; run: teacat pull smollm2:135m")
	}
	return models[0], nil
}

func (s *Server) ensureLoaded(ref string) (*model.Model, store.Manifest, error) {
	manifest, err := s.resolveRef(ref)
	if err != nil {
		return nil, store.Manifest{}, err
	}
	if s.model != nil && s.ref == manifest.Ref() {
		return s.model, s.manifest, nil
	}
	s.logf("loading model %s ...", manifest.Ref())
	start := time.Now()
	m, err := model.Load(manifest.GGUF)
	if err != nil {
		return nil, store.Manifest{}, err
	}
	s.logf("loaded %s in %s", manifest.Ref(), time.Since(start).Round(time.Millisecond))
	s.model = m
	s.ref = manifest.Ref()
	s.manifest = manifest
	s.loadedAt = time.Now()
	return m, manifest, nil
}

// Preload loads a model into memory so the first request is fast.
func (s *Server) Preload(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.ensureLoaded(ref)
	return err
}

func (s *Server) handlePS(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := []map[string]any{}
	if s.model != nil {
		models = append(models, map[string]any{
			"name":       s.ref,
			"model":      s.ref,
			"size":       s.manifest.Size,
			"digest":     s.manifest.Digest,
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// ---- tags / show / pull / delete ------------------------------------------

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	models, err := s.Store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		family := m.Arch
		if family == "" {
			family = "llama"
		}
		out = append(out, map[string]any{
			"name":        m.Ref(),
			"model":       m.Ref(),
			"modified_at": m.ModifiedAt.Format(time.RFC3339Nano),
			"size":        m.Size,
			"digest":      m.Digest,
			"details": map[string]any{
				"format":             "gguf",
				"family":             family,
				"parameter_size":     humanParams(m.Params),
				"quantization_level": m.Quant,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

func humanParams(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.0fM", float64(n)/1e6)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	m, err := s.Store.Get(req.Name)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":       m.Ref(),
		"model":      m.Ref(),
		"modified_at": m.ModifiedAt.Format(time.RFC3339Nano),
		"size":       m.Size,
		"digest":     m.Digest,
		"details":    map[string]any{"format": "gguf"},
		"template":   m.Template,
		"system":     m.System,
		"parameters": m.Parameters,
	})
}

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Stream *bool  `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	send := func(v any) {
		b, _ := json.Marshal(v)
		w.Write(append(b, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	}
	send(map[string]any{"status": "pulling manifest"})
	lastPct := -1
	m, err := registry.Pull(s.Store, req.Name, func(what string, done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == lastPct {
			return
		}
		lastPct = pct
		send(map[string]any{"status": "downloading", "digest": what, "total": total, "completed": done})
	})
	if err != nil {
		send(map[string]any{"error": err.Error()})
		return
	}
	send(map[string]any{"status": "success", "name": m.Ref()})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.Remove(req.Name); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if s.ref == req.Name {
		s.model = nil
		s.ref = ""
	}
	w.WriteHeader(http.StatusOK)
}

// ---- generation ------------------------------------------------------------

type chatMessage = tokenizer.Message

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model   string         `json:"model"`
		Prompt  string         `json:"prompt"`
		System  string         `json:"system"`
		Stream  *bool          `json:"stream"`
		Options map[string]any `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	stream := true
	if req.Stream != nil {
		stream = *req.Stream
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, manifest, err := s.ensureLoaded(req.Model)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	opt := buildOptions(m, manifest, req.Options, s.Ctx)
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + prompt
	}
	ids := m.Tok.Encode(prompt, true)
	created := time.Now().UTC().Format(time.RFC3339Nano)
	ref := manifest.Ref()

	flusher, _ := w.(http.Flusher)
	var written bool
	emit := func(chunk string) bool {
		if !stream {
			return true
		}
		if !written {
			w.Header().Set("Content-Type", "application/x-ndjson")
			written = true
		}
		writeLine(w, map[string]any{
			"model": ref, "created_at": created, "response": chunk, "done": false,
		})
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	stats, err := engine.Generate(r.Context(), m, ids, opt, emit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	final := map[string]any{
		"model":                ref,
		"created_at":           created,
		"response":             "",
		"done":                 true,
		"done_reason":          stats.StopReason,
		"prompt_eval_count":    stats.PromptTokens,
		"eval_count":           stats.EvalTokens,
		"prompt_eval_duration": stats.PromptDuration.Nanoseconds(),
		"eval_duration":        stats.EvalDuration.Nanoseconds(),
	}
	if !stream {
		final["response"] = stats.Text
		writeJSON(w, http.StatusOK, final)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	writeLine(w, final)
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string         `json:"model"`
		Messages []chatMessage  `json:"messages"`
		System   string         `json:"system"`
		Stream   *bool          `json:"stream"`
		Options  map[string]any `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	stream := true
	if req.Stream != nil {
		stream = *req.Stream
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, manifest, err := s.ensureLoaded(req.Model)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	opt := buildOptions(m, manifest, req.Options, s.Ctx)
	system := req.System
	if system == "" {
		system = manifest.System
	}
	prompt := engine.ChatPrompt(m, req.Messages, system)
	ids := engine.EncodeChatPrompt(m, prompt)
	created := time.Now().UTC().Format(time.RFC3339Nano)
	ref := manifest.Ref()

	flusher, _ := w.(http.Flusher)
	emit := func(chunk string) bool {
		if !stream {
			return true
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeLine(w, map[string]any{
			"model": ref, "created_at": created,
			"message": map[string]any{"role": "assistant", "content": chunk},
			"done":    false,
		})
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	stats, err := engine.Generate(r.Context(), m, ids, opt, emit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	final := map[string]any{
		"model":                ref,
		"created_at":           created,
		"message":              map[string]any{"role": "assistant", "content": ""},
		"done":                 true,
		"done_reason":          stats.StopReason,
		"prompt_eval_count":    stats.PromptTokens,
		"eval_count":           stats.EvalTokens,
		"prompt_eval_duration": stats.PromptDuration.Nanoseconds(),
		"eval_duration":        stats.EvalDuration.Nanoseconds(),
	}
	if !stream {
		final["message"] = map[string]any{"role": "assistant", "content": stats.Text}
		writeJSON(w, http.StatusOK, final)
		return
	}
	writeLine(w, final)
	if flusher != nil {
		flusher.Flush()
	}
}

// ---- OpenAI compatible ----------------------------------------------------

func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.Store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for i, m := range models {
		data = append(data, map[string]any{
			"id":       m.Ref(),
			"object":   "model",
			"created":  m.ModifiedAt.Unix(),
			"owned_by": "tyxen",
			"index":    i,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model       string          `json:"model"`
		Messages    []chatMessage   `json:"messages"`
		Stream      bool            `json:"stream"`
		MaxTokens   int             `json:"max_tokens"`
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
		Stop        any             `json:"stop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, manifest, err := s.ensureLoaded(req.Model)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	opts := map[string]any{}
	if req.MaxTokens > 0 {
		opts["num_predict"] = float64(req.MaxTokens)
	}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		opts["top_p"] = *req.TopP
	}
	if req.Stop != nil {
		opts["stop"] = req.Stop
	}
	opt := buildOptions(m, manifest, opts, s.Ctx)
	prompt := engine.ChatPrompt(m, req.Messages, manifest.System)
	ids := engine.EncodeChatPrompt(m, prompt)

	id := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created := time.Now().Unix()

	if !req.Stream {
		stats, err := engine.Generate(r.Context(), m, ids, opt, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": manifest.Ref(),
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": stats.Text},
				"finish_reason": finishReason(stats.StopReason),
			}},
			"usage": map[string]any{
				"prompt_tokens":     stats.PromptTokens,
				"completion_tokens": stats.EvalTokens,
				"total_tokens":      stats.PromptTokens + stats.EvalTokens,
			},
		})
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeSSE(w, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": manifest.Ref(),
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	})
	if flusher != nil {
		flusher.Flush()
	}
	emit := func(chunk string) bool {
		writeSSE(w, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": manifest.Ref(),
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": chunk}, "finish_reason": nil}},
		})
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	stats, err := engine.Generate(r.Context(), m, ids, opt, emit)
	if err != nil {
		return
	}
	writeSSE(w, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": manifest.Ref(),
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason(stats.StopReason)}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func finishReason(reason string) string {
	if reason == "stop" {
		return "stop"
	}
	return "length"
}

// ---- helpers ---------------------------------------------------------------

func buildOptions(m *model.Model, manifest store.Manifest, src map[string]any, defaultCtx int) engine.Options {
	opt := engine.Options{NumCtx: defaultCtx, Sampler: sampler.Default()}
	if opt.NumCtx <= 0 || opt.NumCtx > m.Cfg.Ctx {
		opt.NumCtx = m.Cfg.Ctx
	}
	apply := func(k string, v any) {
		switch strings.ToLower(k) {
		case "temperature":
			opt.Sampler.Temperature = float32(toFloat(v))
		case "top_k":
			opt.Sampler.TopK = int(toFloat(v))
		case "top_p":
			opt.Sampler.TopP = float32(toFloat(v))
		case "min_p":
			opt.Sampler.MinP = float32(toFloat(v))
		case "repeat_penalty":
			opt.Sampler.RepeatPenalty = float32(toFloat(v))
		case "repeat_last_n":
			opt.Sampler.RepeatLastN = int(toFloat(v))
		case "seed":
			opt.Sampler.Seed = int64(toFloat(v))
		case "num_predict":
			opt.NumPredict = int(toFloat(v))
		case "num_ctx":
			opt.NumCtx = int(toFloat(v))
		case "stop":
			opt.Stop = toStrings(v)
		}
	}
	for k, v := range manifest.Parameters {
		apply(k, v)
	}
	for k, v := range src {
		apply(k, v)
	}
	if opt.NumCtx > m.Cfg.Ctx {
		opt.NumCtx = m.Cfg.Ctx
	}
	return opt
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func toStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeLine(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.Write(append(b, '\n'))
}

func writeSSE(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// Listen serves the API and blocks.
func (s *Server) Listen(addr string) error {
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return httpServer.ListenAndServe()
}
