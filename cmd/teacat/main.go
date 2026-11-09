package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tyxteacat/teacat/internal/brand"
	"github.com/tyxteacat/teacat/internal/engine"
	"github.com/tyxteacat/teacat/internal/gguf"
	"github.com/tyxteacat/teacat/internal/hardware"
	"github.com/tyxteacat/teacat/internal/model"
	"github.com/tyxteacat/teacat/internal/registry"
	"github.com/tyxteacat/teacat/internal/sampler"
	"github.com/tyxteacat/teacat/internal/server"
	"github.com/tyxteacat/teacat/internal/store"
	"github.com/tyxteacat/teacat/internal/tokenizer"
)

const version = brand.Version

func main() {
	if n := envInt("TEACAT_THREADS"); n > 0 {
		setThreads(n)
	}
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
	case "chat":
		err = cmdChat(os.Args[2:])
	case "info", "system":
		err = cmdInfo(os.Args[2:])
	case "pull":
		err = cmdPull(os.Args[2:])
	case "list", "ls":
		err = cmdList(os.Args[2:])
	case "rm", "delete":
		err = cmdRm(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "run":
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
  chat [model]          Open the modern browser chat (pulls the model if needed)
  pull <model>          Download a model (e.g. smollm2:135m)
  list                  List installed models
  run <model> [prompt]  Chat in the terminal (interactive if no prompt)
  info                  Show CPU, RAM, GPU, threads and model memory fit
  show <model>          Show model details
  rm <model>            Remove an installed model
  create <name> -f FILE Import a local .gguf file
  version               Print the version

Examples:
  teacat pull smollm2:135m
  teacat chat smollm2:135m           # modern chat UI in your browser
  teacat run smollm2:135m            # interactive >>> chat in the terminal
  teacat run --web smollm2:135m      # same as teacat chat
  teacat run smollm2:135m "Write a haiku about Go"
  teacat info                        # see what hardware TeaCat can use
  teacat serve
  teacat create mymodel:latest -f .\model.gguf
  teacat create .\model.gguf

Pull only downloads a model. To talk to it, use "teacat chat <model>" for the
browser interface, or "teacat run <model>" for the terminal.

Project: ` + brand.Repo + `
`)
}

func openStore() (*store.Store, error) {
	return store.Open()
}

func envInt(name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return 0
	}
	return v
}

// setThreads caps the Go scheduler at a sane number of logical CPUs.
func setThreads(n int) int {
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if max := runtime.NumCPU(); n > max {
		n = max
	}
	runtime.GOMAXPROCS(n)
	return n
}

func defaultThreads() int {
	return runtime.GOMAXPROCS(0)
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
	threads := fs.Int("threads", defaultThreads(), "CPU threads to use")
	lowMem := fs.Bool("low-mem", false, "keep packed weights to save RAM")
	fs.Parse(args)
	nthreads := setThreads(*threads)

	st, err := openStore()
	if err != nil {
		return err
	}
	s := &server.Server{
		Store:     st,
		Ctx:       *ctx,
		Version:   version,
		LowMemory: *lowMem,
		Logf: func(format string, a ...any) {
			fmt.Fprintf(os.Stderr, "[teacat] "+format+"\n", a...)
		},
	}
	hw := hardware.Detect()
	fmt.Fprintf(os.Stderr, "[teacat] %s, %d threads\n", hw.Summary(), nthreads)
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
	if *preload != "" {
		dashboard += "?model=" + url.QueryEscape(*preload)
	}
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

// ---- chat (browser UI) ----------------------------------------------------

// cmdChat starts the server and opens the dashboard chat, preloading the model.
func cmdChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "bind address")
	port := fs.Int("port", 11434, "port")
	ctx := fs.Int("ctx", 2048, "default context length")
	threads := fs.Int("threads", defaultThreads(), "CPU threads to use")
	lowMem := fs.Bool("low-mem", false, "keep packed weights to save RAM")
	fs.Parse(args)
	model := ""
	if fs.NArg() > 0 {
		model = fs.Arg(0)
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	if model != "" {
		if _, err := ensureModel(st, model); err != nil {
			return err
		}
	}
	return startWebChat(*host, *port, *ctx, model, *threads, *lowMem)
}

func startWebChat(host string, port, ctx int, model string, threads int, lowMem bool) error {
	args := []string{
		"--open",
		"--host", host,
		"--port", strconv.Itoa(port),
		"--ctx", strconv.Itoa(ctx),
		"--threads", strconv.Itoa(threads),
	}
	if lowMem {
		args = append(args, "--low-mem")
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	return cmdServe(args)
}

// ---- info -----------------------------------------------------------------

type modelReport struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Quant   string `json:"quant,omitempty"`
	Need    uint64 `json:"need_bytes"`
	Fits    bool   `json:"fits"`
	HasChat bool   `json:"chat_template"`
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	fs.Parse(args)

	info := hardware.Detect()
	st, err := openStore()
	if err != nil {
		return err
	}
	models, _ := st.List()
	rows := make([]modelReport, 0, len(models))
	for _, m := range models {
		need := hardware.NeedBytes(uint64(m.Size), m.Quant)
		fits := true
		if info.RAMAvailable > 0 {
			fits = need <= info.RAMAvailable
		}
		rows = append(rows, modelReport{
			Name:    m.Ref(),
			Size:    m.Size,
			Quant:   m.Quant,
			Need:    need,
			Fits:    fits,
			HasChat: strings.TrimSpace(m.Template) != "",
		})
	}

	if *asJSON {
		payload := map[string]any{
			"version":  version,
			"hardware": info,
			"models":   rows,
		}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("TeaCat %s - hardware\n\n", version)
	fmt.Printf("OS         %s/%s\n", info.OS, info.Arch)
	fmt.Printf("CPU        %s\n", info.CPU)
	fmt.Printf("Cores      %d logical\n", info.Cores)
	fmt.Printf("Threads    %d (Go runtime)\n", info.GOMAXPROCS)
	if info.RAMAvailable > 0 {
		fmt.Printf("Memory     %s total, %s available\n", hardware.HumanBytes(info.RAMTotal), hardware.HumanBytes(info.RAMAvailable))
	} else {
		fmt.Printf("Memory     %s total\n", hardware.HumanBytes(info.RAMTotal))
	}
	if len(info.GPUs) == 0 {
		fmt.Println("GPU        none detected")
	} else {
		for i, g := range info.GPUs {
			label := "GPU        "
			if i > 0 {
				label = "           "
			}
			vendor := ""
			if g.Vendor != "" {
				vendor = " (" + g.Vendor + ")"
			}
			fmt.Printf("%s%s%s\n", label, g.Name, vendor)
		}
	}
	fmt.Printf("Backend    %s\n", info.Backend)
	fmt.Printf("Store      %s\n", st.Home)

	fmt.Println("\nInstalled models:")
	if len(rows) == 0 {
		fmt.Println("  no models yet. try: teacat pull qwen2.5:0.5b")
	}
	for _, r := range rows {
		status := ""
		switch {
		case info.RAMAvailable == 0:
		case r.Fits:
			status = "fits in available RAM"
		default:
			status = "may not fit in available RAM"
		}
		chat := "chat"
		if !r.HasChat {
			chat = "base"
		}
		fmt.Printf("  %-24s %8s  %-5s  need ~%-7s  %-7s  %s\n",
			r.Name, humanSize(r.Size), r.Quant, hardware.HumanBytes(r.Need), chat, status)
	}

	if len(info.GPUs) > 0 {
		fmt.Println("\nNote: a GPU was detected, but this build runs inference on the CPU.")
	}
	fmt.Println("\nTeaCat uses all logical CPU cores and switches to low-memory loading")
	fmt.Println("automatically when a model would not fit in available RAM.")
	fmt.Println("For reliable chat output use an instruct model such as qwen2.5:0.5b.")
	return nil
}

// ---- pull -----------------------------------------------------------------

func cmdPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	asURL := fs.Bool("url", false, "treat the argument as a direct GGUF URL")
	name := fs.String("name", "", "local name when importing a URL or file")
	fs.Parse(flexibleArgs(args, map[string]bool{"-url": true, "--url": true, "-name": true, "--name": true}))
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat pull <model[:tag]> | teacat pull --url <gguf-url>")
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("pull accepts exactly one model, got extra argument %q\n   did you mean: teacat run %s", fs.Arg(1), fs.Arg(0))
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
		printPullSuccess(m)
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
			printPullSuccess(m)
			return nil
		}
	}

	fmt.Printf("pulling %s\n", arg)
	m, err := registry.Pull(st, arg, progressPrinter())
	if err != nil {
		return err
	}
	printPullSuccess(m)
	return nil
}

// printPullSuccess reports a finished download and shows how to start chatting.
func printPullSuccess(m store.Manifest) {
	fmt.Printf("\nsuccess: %s (%s)\n", m.Ref(), humanSize(m.Size))
	fmt.Printf("start chatting: teacat run %s\n", m.Ref())
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

// flexibleArgs moves flags (and their values) before positional arguments so
// commands accept both "teacat create NAME -f FILE" and "-f FILE NAME".
func flexibleArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if valueFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	file := fs.String("f", "", "path to a .gguf file")
	fs.Parse(flexibleArgs(args, map[string]bool{"-f": true, "--f": true, "-file": true, "--file": true}))
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat create <name> -f model.gguf\n       teacat create model.gguf")
	}
	ref := fs.Arg(0)
	if *file == "" {
		// Allow "teacat create C:\path\model.gguf" as a shortcut.
		if strings.EqualFold(filepath.Ext(ref), ".gguf") {
			*file = ref
			ref = strings.TrimSuffix(filepath.Base(ref), filepath.Ext(ref))
		} else {
			return fmt.Errorf("missing -f <model.gguf>")
		}
	}
	if abs, err := filepath.Abs(*file); err == nil {
		*file = abs
	}
	if _, err := os.Stat(*file); err != nil {
		return fmt.Errorf("model file not found: %s\n   check the path first:  Test-Path \"%s\"\n   or import by copying:  teacat pull \"C:\\path\\to\\model.gguf\"", *file, *file)
	}
	if !strings.EqualFold(filepath.Ext(*file), ".gguf") {
		fmt.Fprintf(os.Stderr, "warning: %s does not end in .gguf; TeaCat only supports GGUF files\n", *file)
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	fmt.Printf("importing %s as %s\n", *file, ref)
	fmt.Printf("copying into %s\n", st.BlobsDir())
	m, err := st.ImportFile(firstPart(ref), tagPart(ref), *file, "file")
	if err != nil {
		return err
	}
	fmt.Printf("success: %s (%s)\n", m.Ref(), humanSize(m.Size))
	fmt.Printf("start chatting: teacat run %s\n", m.Ref())
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
	web := fs.Bool("web", false, "open the browser chat instead of the terminal")
	host := fs.String("host", "127.0.0.1", "bind address for --web")
	port := fs.Int("port", 11434, "port for --web")
	threads := fs.Int("threads", defaultThreads(), "CPU threads to use")
	lowMem := fs.Bool("low-mem", false, "keep packed weights to save RAM")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: teacat run <model> [prompt]")
	}
	ref := fs.Arg(0)
	prompt := strings.Join(fs.Args()[1:], " ")
	nthreads := setThreads(*threads)

	st, err := openStore()
	if err != nil {
		return err
	}
	if *web {
		if _, err := ensureModel(st, ref); err != nil {
			return err
		}
		return startWebChat(*host, *port, *ctxLen, ref, nthreads, *lowMem)
	}
	manifest, err := ensureModel(st, ref)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "loading %s ...\n", manifest.Ref())
	low := *lowMem || hardware.ShouldUseLowMemory(uint64(manifest.Size), manifest.Quant)
	if low {
		fmt.Fprintln(os.Stderr, "low-memory mode: keeping packed weights (saves RAM, a little slower)")
	}
	start := time.Now()
	m, err := model.LoadWithOptions(manifest.GGUF, model.LoadOptions{LowMemory: low})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "loaded in %s on %d threads (%s)\n", time.Since(start).Round(time.Millisecond), nthreads, m.Info())
	if manifest.Params > 0 && manifest.Params < 300_000_000 && prompt == "" {
		fmt.Fprintf(os.Stderr, "note: %s is a very small model and may ramble. For better chat try qwen2.5:0.5b.\n\n", manifest.Ref())
	} else {
		fmt.Fprintln(os.Stderr)
	}

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

	fmt.Printf("Chatting with %s. Type /bye to exit, /clear to reset, /help for commands.\n\n", manifest.Ref())
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
		case "/help", "/?":
			printChatHelp()
			continue
		}
		if line == `"""` {
			var ok bool
			line, ok = readMultiline(scanner)
			if !ok {
				fmt.Println()
				return nil
			}
			if line == "" {
				continue
			}
		}
		history = append(history, tokenizer.Message{Role: "user", Content: line})
		text := runOnce(m, history, sys, opts)
		history = append(history, tokenizer.Message{Role: "assistant", Content: text})
	}
}

// ensureModel returns an installed model, pulling it when missing and repairing
// a manifest whose blob was moved or deleted.
func ensureModel(st *store.Store, ref string) (store.Manifest, error) {
	manifest, err := st.Get(ref)
	if err == nil {
		if _, statErr := os.Stat(manifest.GGUF); statErr != nil {
			fmt.Fprintf(os.Stderr, "%s is missing its model file, pulling it again...\n", ref)
			manifest, err = registry.Pull(st, ref, progressPrinter())
			fmt.Fprintln(os.Stderr)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		// Ollama-style behavior: running a model that is not installed pulls
		// it first, then opens the chat.
		fmt.Fprintf(os.Stderr, "%s is not installed, pulling it now...\n", ref)
		manifest, err = registry.Pull(st, ref, progressPrinter())
		fmt.Fprintln(os.Stderr)
	}
	return manifest, err
}

// readMultiline reads lines until a closing `"""` and joins them with newlines.
func readMultiline(scanner *bufio.Scanner) (string, bool) {
	var lines []string
	for {
		fmt.Print("... ")
		if !scanner.Scan() {
			return strings.Join(lines, "\n"), false
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == `"""` {
			return strings.Join(lines, "\n"), true
		}
		lines = append(lines, line)
	}
}

func printChatHelp() {
	fmt.Print(`
Chat commands:
  /bye, /exit, /quit   exit the chat
  /clear               forget the conversation history
  /help, /?            show this help
  """                  start a multi-line message (finish with """)

Type anything else to send it to the model.
`)
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
