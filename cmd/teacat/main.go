package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tyxteacat/teacat/internal/brand"
	"github.com/tyxteacat/teacat/internal/engine"
	"github.com/tyxteacat/teacat/internal/gguf"
	"github.com/tyxteacat/teacat/internal/model"
	"github.com/tyxteacat/teacat/internal/registry"
	"github.com/tyxteacat/teacat/internal/sampler"
	"github.com/tyxteacat/teacat/internal/server"
	"github.com/tyxteacat/teacat/internal/store"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

const version = brand.Version

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "ui", "dashboard":
		err = cmdServe(append([]string{"--open"}, os.Args[2:]...))
	case "pull":
		err = cmdPull(os.Args[2:])
	case "list", "ls":
		err = cmdList(os.Args[2:])
	case "rm", "delete":
		err = cmdRm(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "run", "chat":
		err = cmdRun(os.Args[2:])
	case "create":
		err = cmdCreate(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("TeaCat " + version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`TeaCat - run local language models

TeaCat is a local LLM runtime created by Tyxen.

Usage:
  teacat <command> [arguments]

Commands:
  serve                 Start the local API server + web dashboard
  ui                    Start the server and open the dashboard in a browser
  pull <model>          Download a model (e.g. smollm2:135m)
  list                  List installed models
  run <model> [prompt]  Chat with a model (interactive if no prompt)
  show <model>          Show model details
  rm <model>            Remove an installed model
  create <name> -f FILE Import a local .gguf file
  version               Print the version

Examples:
  teacat pull smollm2:135m
  teacat run smollm2:135m "Write a haiku about Go"
  teacat ui
  teacat serve
  teacat create mymodel:latest -f .\model.gguf

Project: ` + brand.Repo + `
`)
}

func openStore() (*store.Store, error) {
	return store.Open()
}

// openBrowser opens url with the platform's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// ---- serve ----------------------------------------------------------------

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "bind address")
	port := fs.Int("port", 11434, "port")
	ctx := fs.Int("ctx", 2048, "default context length")
	preload := fs.String("model", "", "model to preload")
	open := fs.Bool("open", false, "open the dashboard in a browser")
	fs.Parse(args)

	st, err := openStore()
	if err != nil {
		return err
	}
	s := &server.Server{
		Store:   st,
		Ctx:     *ctx,
		Version: version,
		Logf: func(format string, a ...any) {
			fmt.Fprintf(os.Stderr, "[teacat] "+format+"\n", a...)
		},
	}
	if *preload != "" {
		fmt.Fprintf(os.Stderr, "[teacat] preloading %s\n", *preload)
		if err := s.Preload(*preload); err != nil {
			return err
		}
	}
	addr := fmt.Sprintf("%s:%d", *host, *port)
	browseHost := *host
	if browseHost == "0.0.0.0" || browseHost == "::" || browseHost == "[::]" {
		browseHost = "127.0.0.1"
	}
	dashboard := fmt.Sprintf("http://%s:%d/", browseHost, *port)
	fmt.Printf("TeaCat %s listening on http://%s\n", version, addr)
	fmt.Printf("  Dashboard:  %s\n", dashboard)
	fmt.Printf("  Ollama API: http://%s:%d/api/chat\n", browseHost, *port)
	fmt.Printf("  OpenAI API: http://%s:%d/v1/chat/completions\n", browseHost, *port)
	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(dashboard); err != nil {
				fmt.Fprintf(os.Stderr, "[teacat] could not open browser: %v\n", err)
			}
		}()
	}
	return s.Listen(addr)
}

// ---- pull -----------------------------------------------------------------

func cmdPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	asURL := fs.Bool("url", false, "treat the argument as a direct GGUF URL")
	name := fs.String("name", "", "local name when importing a URL or file")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat pull <model[:tag]> | teacat pull --url <gguf-url>")
	}
	arg := fs.Arg(0)
	st, err := openStore()
	if err != nil {
		return err
	}

	switch {
	case *asURL || strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://"):
		base := filepath.Base(arg)
		base = strings.TrimSuffix(base, ".gguf")
		if base == "" || base == "." {
			base = "model"
		}
		ref := base
		if *name != "" {
			ref = *name
		}
		fmt.Printf("downloading %s\n", arg)
		m, err := registry.ImportURL(st, ref, arg, progressPrinter())
		if err != nil {
			return err
		}
		fmt.Printf("\nsuccess: %s (%s)\n", m.Ref(), humanSize(m.Size))
		return nil
	case strings.HasSuffix(strings.ToLower(arg), ".gguf"):
		if _, err := os.Stat(arg); err == nil {
			ref := *name
			if ref == "" {
				ref = strings.TrimSuffix(filepath.Base(arg), filepath.Ext(arg))
			}
			fmt.Printf("importing %s\n", arg)
			m, err := st.ImportFile(firstPart(ref), tagPart(ref), arg, "file")
			if err != nil {
				return err
			}
			fmt.Printf("success: %s (%s)\n", m.Ref(), humanSize(m.Size))
			return nil
		}
	}

	fmt.Printf("pulling %s\n", arg)
	m, err := registry.Pull(st, arg, progressPrinter())
	if err != nil {
		return err
	}
	fmt.Printf("\nsuccess: %s (%s)\n", m.Ref(), humanSize(m.Size))
	return nil
}

func progressPrinter() registry.Progress {
	last := time.Time{}
	lastPct := -1
	return func(what string, done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == lastPct && time.Since(last) < 200*time.Millisecond {
			return
		}
		lastPct = pct
		last = time.Now()
		const width = 30
		filled := pct * width / 100
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", width-filled)
		fmt.Printf("\r  [%s] %3d%%  %s / %s", bar, pct, humanSize(done), humanSize(total))
	}
}

// ---- list / rm / show -----------------------------------------------------

func cmdList(args []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	models, err := st.List()
	if err != nil {
		return err
	}
	if len(models) == 0 {
		fmt.Println("no models installed. try: teacat pull smollm2:135m")
		return nil
	}
	fmt.Printf("%-28s %10s  %s\n", "NAME", "SIZE", "MODIFIED")
	for _, m := range models {
		fmt.Printf("%-28s %10s  %s\n", m.Ref(), humanSize(m.Size), m.ModifiedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

func cmdRm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat rm <model>")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	for _, ref := range fs.Args() {
		if err := st.Remove(ref); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", ref)
	}
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat show <model>")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	m, err := st.Get(fs.Arg(0))
	if err != nil {
		return err
	}
	g, err := gguf.OpenMeta(m.GGUF)
	if err == nil {
		arch, _ := g.String("general.architecture")
		layers, _ := g.Int(arch + ".block_count")
		embd, _ := g.Int(arch + ".embedding_length")
		heads, _ := g.Int(arch + ".attention.head_count")
		kv, _ := g.Int(arch + ".attention.head_count_kv")
		ffn, _ := g.Int(arch + ".feed_forward_length")
		ctx, _ := g.Int(arch + ".context_length")
		rope, _ := g.Float(arch + ".rope.freq_base")
		fmt.Printf("name:       %s\n", m.Ref())
		fmt.Printf("size:       %s\n", humanSize(m.Size))
		fmt.Printf("digest:     %s\n", m.Digest)
		fmt.Printf("architecture: %s\n", arch)
		fmt.Printf("layers:     %d\n", layers)
		fmt.Printf("hidden:     %d\n", embd)
		fmt.Printf("heads:      %d (kv %d)\n", heads, kv)
		fmt.Printf("ffn:        %d\n", ffn)
		fmt.Printf("context:    %d\n", ctx)
		fmt.Printf("rope base:  %.1f\n", rope)
		if m.Params > 0 {
			fmt.Printf("params:     %.1fM\n", float64(m.Params)/1e6)
		}
		if m.Quant != "" {
			fmt.Printf("quant:      %s\n", m.Quant)
		}
		tokCount := 0
		if ts, ok := g.Strings("tokenizer.ggml.tokens"); ok {
			tokCount = len(ts)
		}
		fmt.Printf("vocab:      %d\n", tokCount)
	}
	if m.Template != "" {
		fmt.Printf("\nchat template:\n%s\n", strings.TrimSpace(m.Template))
	}
	if len(m.Parameters) > 0 {
		fmt.Printf("\nparameters:\n")
		for k, v := range m.Parameters {
			fmt.Printf("  %s %v\n", k, v)
		}
	}
	return nil
}

// ---- create ---------------------------------------------------------------

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	file := fs.String("f", "", "path to a .gguf file")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat create <name> -f model.gguf")
	}
	if *file == "" {
		return fmt.Errorf("missing -f <model.gguf>")
	}
	ref := fs.Arg(0)
	st, err := openStore()
	if err != nil {
		return err
	}
	fmt.Printf("importing %s as %s\n", *file, ref)
	m, err := st.ImportFile(firstPart(ref), tagPart(ref), *file, "file")
	if err != nil {
		return err
	}
	fmt.Printf("success: %s (%s)\n", m.Ref(), humanSize(m.Size))
	return nil
}

// ---- run ------------------------------------------------------------------

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ctxLen := fs.Int("ctx", 2048, "context length")
	maxTokens := fs.Int("n", 256, "maximum tokens to generate")
	temp := fs.Float64("temp", 0.7, "temperature")
	topP := fs.Float64("top-p", 0.9, "nucleus sampling probability")
	topK := fs.Int("top-k", 40, "top-k sampling")
	repeat := fs.Float64("repeat-penalty", 1.1, "repetition penalty")
	system := fs.String("system", "", "system prompt")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat run <model> [prompt]")
	}
	ref := fs.Arg(0)
	prompt := strings.Join(fs.Args()[1:], " ")

	st, err := openStore()
	if err != nil {
		return err
	}
	manifest, err := st.Get(ref)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "loading %s ...\n", manifest.Ref())
	start := time.Now()
	m, err := model.Load(manifest.GGUF)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "loaded in %s (%s)\n\n", time.Since(start).Round(time.Millisecond), m.Info())

	opts := engine.Options{
		NumCtx:     *ctxLen,
		NumPredict: *maxTokens,
		Sampler: sampler.Params{
			Temperature:   float32(*temp),
			TopK:          *topK,
			TopP:          float32(*topP),
			RepeatPenalty: float32(*repeat),
			RepeatLastN:   64,
		},
	}
	applyManifestParams(&opts, manifest.Parameters)
	sys := *system
	if sys == "" {
		sys = manifest.System
	}

	if prompt != "" {
		msgs := []tokenizer.Message{{Role: "user", Content: prompt}}
		runOnce(m, msgs, sys, opts)
		return nil
	}

	fmt.Printf("Chatting with %s. Type /bye to exit, /clear to reset.\n\n", manifest.Ref())
	var history []tokenizer.Message
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print(">>> ")
		if !scanner.Scan() {
			fmt.Println()
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/bye", "/exit", "/quit":
			return nil
		case "/clear":
			history = nil
			fmt.Println("(history cleared)")
			continue
		}
		history = append(history, tokenizer.Message{Role: "user", Content: line})
		text := runOnce(m, history, sys, opts)
		history = append(history, tokenizer.Message{Role: "assistant", Content: text})
	}
}

func runOnce(m *model.Model, msgs []tokenizer.Message, system string, opts engine.Options) string {
	prompt := engine.ChatPrompt(m, msgs, system)
	ids := engine.EncodeChatPrompt(m, prompt)
	start := time.Now()
	stats, err := engine.Generate(context.Background(), m, ids, opts, func(chunk string) bool {
		fmt.Print(chunk)
		return true
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		return ""
	}
	fmt.Println()
	elapsed := time.Since(start).Seconds()
	tps := 0.0
	if stats.EvalDuration > 0 {
		tps = float64(stats.EvalTokens) / stats.EvalDuration.Seconds()
	}
	fmt.Fprintf(os.Stderr, "[%d prompt, %d generated, %.1f tok/s, %.1fs]\n\n",
		stats.PromptTokens, stats.EvalTokens, tps, elapsed)
	return stats.Text
}

func applyManifestParams(opt *engine.Options, params map[string]any) {
	if params == nil {
		return
	}
	get := func(k string) (float64, bool) {
		v, ok := params[k]
		if !ok {
			return 0, false
		}
		switch x := v.(type) {
		case float64:
			return x, true
		case int64:
			return float64(x), true
		case int:
			return float64(x), true
		}
		return 0, false
	}
	if v, ok := get("temperature"); ok {
		opt.Sampler.Temperature = float32(v)
	}
	if v, ok := get("top_p"); ok {
		opt.Sampler.TopP = float32(v)
	}
	if v, ok := get("top_k"); ok {
		opt.Sampler.TopK = int(v)
	}
	if v, ok := get("repeat_penalty"); ok {
		opt.Sampler.RepeatPenalty = float32(v)
	}
	if v, ok := get("num_ctx"); ok && int(v) > 0 {
		opt.NumCtx = int(v)
	}
}

func firstPart(ref string) string {
	name, _ := store.SplitRef(ref)
	return name
}

func tagPart(ref string) string {
	_, tag := store.SplitRef(ref)
	return tag
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
