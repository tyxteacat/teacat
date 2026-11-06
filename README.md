# TeaCat — Local LLM Runtime by Tyxen

**TeaCat** (also searched as *TeaCat Tyxen*, *TeaCat by Tyxen* or *Tea Cat AI*) is
a free, open-source local LLM runtime created by **Tyxen**. It downloads GGUF
models, runs them on your own CPU with a transformer engine written from
scratch in Go, and serves both an Ollama-compatible API and an
OpenAI-compatible chat API.

No llama.cpp, no cgo, no external inference libraries. The GGUF parser,
quantization kernels, tokenizer, transformer forward pass and sampler are all in
this repository.

```
teacat pull smollm2:135m
teacat run smollm2:135m "Write a haiku about Go"
teacat serve
```

- **Project:** TeaCat
- **Author:** Tyxen
- **Repository:** https://github.com/tyxteacat/teacat
- **Website:** `site/` (GitHub Pages ready)
- **License:** MIT

Maintainers / next AI: see **[HANDOFF.md](HANDOFF.md)** for current state, publishing steps and continuation notes.

## Features

- **GGUF model loader** — v2/v3 metadata, tensor table, alignment handling.
- **Quantization support** — F32, F16, BF16, Q8_0, Q4_0, Q4_1, Q5_0, Q5_1,
  Q2_K, Q3_K, Q4_K, Q5_K, Q6_K, IQ4_NL.
- **Tokenizers** — SentencePiece/unigram (LLaMA 2) and byte-level BPE
  (GPT-2 / Qwen / Llama 3 / SmolLM).
- **Transformer engine** — RMSNorm, RoPE, grouped-query attention with KV
  cache, SwiGLU FFN, tied or untied output head.
- **Chat templates** — ChatML, Llama 3, Llama 2, Phi-3, Gemma, Command-R, with
  a plain-transcript fallback.
- **Samplers** — temperature, top-k, top-p, min-p, repetition penalty, seeds,
  greedy mode.
- **Model store** — content-addressed blobs and manifests under `~/.teacat`.
- **Puller** — anonymous pulls from `registry.ollama.ai` (OCI) with SHA-256
  verification, direct GGUF URLs, and local file import.
- **Two APIs** — Ollama (`/api/chat`, `/api/generate`, `/api/tags`, ...) and
  OpenAI (`/v1/chat/completions`, `/v1/models`), both with streaming.
- **Web dashboard** — a built-in local UI at `http://127.0.0.1:11434/` for
  chatting, streaming, pulling and deleting models, and tuning sampling. It is
  embedded in the binary and works offline.

## Requirements

- Go 1.22+ (developed with Go 1.27)
- Windows, Linux or macOS on x86-64 or ARM64
- RAM roughly equal to the model file size (F16 weights are expanded to F32 at
  load for speed, so F16 models need ~2× file size)

TeaCat is pure Go with no cgo, so it cross-compiles to every target without a
toolchain per platform. The same source is built and tested on Windows, Linux
and macOS in CI.

## Install

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/tyxteacat/teacat/main/install.ps1 | iex
```

The installer downloads the latest `teacat.exe` for your architecture, verifies
its SHA-256 checksum, installs it to `%LOCALAPPDATA%\TeaCat\bin`, and adds that
folder to your user `PATH`. Open a new terminal afterwards.

To pass options, run the script block directly:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/tyxteacat/teacat/main/install.ps1))) -Version v0.2.0 -InstallDir C:\tools\teacat -NoPath
```

### Linux / macOS

Download the binary for your platform from
[Releases](https://github.com/tyxteacat/teacat/releases), then:

```bash
chmod +x teacat-linux-amd64
sudo mv teacat-linux-amd64 /usr/local/bin/teacat
teacat version
```

Use `teacat-linux-arm64` on ARM Linux, `teacat-darwin-arm64` on Apple Silicon,
and `teacat-darwin-amd64` on Intel Macs.

## Build

Build for the machine you are on:

```powershell
# Windows (PowerShell)
.\build.ps1

# Linux / macOS
./build.sh
```

Or directly with Go:

```powershell
go build -trimpath -ldflags "-s -w" -o teacat.exe ./cmd/teacat   # Windows
go build -trimpath -ldflags "-s -w" -o teacat ./cmd/teacat       # Linux/macOS
```

Cross-compile every supported target into `dist/`:

```powershell
.\build.ps1 -All        # Windows
./build.sh --all        # Linux/macOS
```

That produces Windows, Linux and macOS binaries for both amd64 and arm64, plus
`dist/checksums.txt`. Tagged releases (`v*`) publish the same binaries
automatically through `.github/workflows/release.yml`.

The Go module path is `github.com/tyxteacat/teacat`; update it if you publish the
repository under a different account or host.

## Quick start

```powershell
# Windows
.\teacat.exe pull qwen2.5:0.5b
.\teacat.exe run qwen2.5:0.5b "Explain goroutines in two sentences"
.\teacat.exe run qwen2.5:0.5b
.\teacat.exe ui                  # server + dashboard in your browser
.\teacat.exe serve --model qwen2.5:0.5b
```

```bash
# Linux / macOS
./teacat pull qwen2.5:0.5b
./teacat run qwen2.5:0.5b "Explain goroutines in two sentences"
./teacat run qwen2.5:0.5b
./teacat ui                     # server + dashboard in your browser
./teacat serve --model qwen2.5:0.5b
```

Then open **http://127.0.0.1:11434/** for the dashboard. Chat streams into the
page, the model list is live, and pull progress is shown inline. `teacat ui`
opens it automatically.

Models are stored in `~/.teacat` on every platform. Set `TEACAT_HOME` to use a
different directory.

The local API listens on `http://127.0.0.1:11434` on every platform.

## CLI

| Command | Description |
| --- | --- |
| `teacat serve [--port 11434] [--host 127.0.0.1] [--ctx 2048] [--model NAME] [--open]` | Start the API server + dashboard |
| `teacat ui [--port 11434]` | Start the server and open the dashboard |
| `teacat pull <name[:tag]>` | Download from the Ollama registry |
| `teacat pull --url <gguf-url> [--name name:tag]` | Download a direct GGUF URL |
| `teacat create <name[:tag]> -f model.gguf` | Import a local GGUF file |
| `teacat list` | List installed models |
| `teacat show <model>` | Show architecture, context, template |
| `teacat run <model> [prompt]` | Chat, streaming to the terminal |
| `teacat rm <model>` | Delete a model and unreferenced blobs |

`run` flags: `--ctx`, `--n` (max tokens), `--temp`, `--top-p`, `--top-k`,
`--repeat-penalty`, `--system`.

## Web dashboard

`teacat serve` also serves a local dashboard:

- **Chat** — streaming replies, markdown-free safe rendering, stop button,
  per-response tokens/sec.
- **Models** — installed models with size/quantization, one-click selection,
  delete.
- **Pull** — download any Ollama-registry model with a live progress bar.
- **Settings** — system prompt, temperature, top-p, top-k, max tokens, saved in
  the browser.
- **API info** — copy-ready Ollama and OpenAI endpoint URLs.

The dashboard is embedded with `go:embed`, so there are no external assets and no
network access is required. `GET /api/info` describes the server, and
`GET /api/health` returns status, version and uptime.

## HTTP API

### Ollama-compatible

```powershell
curl http://127.0.0.1:11434/api/chat -d '{
  "model": "qwen2.5:0.5b",
  "messages": [{"role": "user", "content": "Hello!"}],
  "stream": false
}'
```

Endpoints: `GET /api/tags`, `POST /api/chat`, `POST /api/generate`,
`POST /api/show`, `POST /api/pull`, `DELETE /api/delete`, `GET /api/ps`,
`GET /api/version`, `GET /api/info`, `GET /api/health`.

### OpenAI-compatible

```powershell
curl http://127.0.0.1:11434/v1/chat/completions -d '{
  "model": "qwen2.5:0.5b",
  "messages": [{"role": "user", "content": "Hello!"}],
  "stream": true
}'
```

Point any OpenAI client at `http://127.0.0.1:11434/v1` with any API key.

## Supported models

Anything GGUF with a LLaMA-family architecture: LLaMA 2/3, Qwen2/2.5, SmolLM2,
TinyLlama, Mistral, Gemma (with caveats), and similar. The loader reads the
architecture name from metadata, so most `llama`-arch files work unchanged.
Mixture-of-experts architectures and non-LLaMA models are not supported yet.

## Project layout

```
cmd/teacat/           CLI entrypoint
internal/brand/       project name, author, links, User-Agent
internal/gguf/        GGUF parser, dequantization, quantized matvec kernels
internal/tokenizer/   SPM + BPE tokenizers, chat templates
internal/model/       transformer config, weights, forward pass, KV cache
internal/sampler/     token sampling
internal/engine/      generation loop and streaming detokenizer
internal/store/       on-disk model store (blobs + manifests)
internal/registry/    Ollama registry / direct URL puller
internal/server/      Ollama + OpenAI compatible HTTP API
internal/webui/       embedded local dashboard (single HTML file)
tools/debug/          developer inspection utility
site/                 static landing page for search engines / GitHub Pages
```

## How it works

1. **Load** — `gguf.Open` maps the header, metadata, and tensor table, then
   reads each tensor into memory. F16/BF16 tensors are expanded to F32 once so
   the hot loop avoids per-element conversion; quantized tensors stay packed.
2. **Tokenize** — the tokenizer is rebuilt from GGUF metadata (vocab, merges,
   token types, scores). Chat prompts are rendered with a template matching the
   model family.
3. **Prefill** — each prompt token runs through all layers, filling an f32 KV
   cache.
4. **Decode** — sample a token, feed it back through the network, stream the
   decoded text. KV cache growth keeps the per-token cost flat.

Matrix-vector products split rows across `GOMAXPROCS` workers. Quantized
kernels compute the dot product block by block without expanding the weights.

## Performance

This is a scalar pure-Go engine, so expect CPU-bound throughput below llama.cpp
(which uses SIMD kernels and hand-tuned assembly). Measured on an
8-thread i5-8265U laptop:

- SmolLM2-135M (F16 expanded to F32): ~14 tokens/sec generation, ~17 tok/s prompt
- Qwen2.5-0.5B (quantized): ~2 tokens/sec generation, ~2.5 tok/s prompt

Good next steps for speed: SIMD/assembly matvec kernels, keeping F16 packed
with a vectorized conversion, flash-attention style tiling, and mmap weights
instead of reading them into heap memory.

## Limitations

- CPU only; no GPU offload yet.
- One model loaded at a time; requests are serialized while generating.
- No embeddings, vision, tool calling, or Mixture-of-Experts.
- No Jinja engine: chat templates are matched by family.
- Prompt tokens beyond the context window are truncated from the left.

## About the author

TeaCat is designed and developed by **Tyxen**. Tyxen built TeaCat around a simple
idea: running modern language models locally should be a single command, with no
cloud account and no telemetry. The project ships its own inference engine so
the whole stack can be read, audited and extended by anyone.

Search terms: TeaCat, TeaCat Tyxen, Tyxen, TeaCat AI, Tea Cat, local LLM runtime,
Ollama alternative, GGUF inference, OpenAI-compatible local server.

## License

MIT — see [LICENSE](LICENSE).
