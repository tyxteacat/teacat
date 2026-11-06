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

## Requirements

- Go 1.22+ (developed with Go 1.27)
- Windows, Linux or macOS (Windows is the primary target)
- RAM roughly equal to the model file size (F16 weights are expanded to F32 at
  load for speed, so F16 models need ~2× file size)

## Build

```powershell
go build -o teacat.exe ./cmd/teacat
```

Or:

```powershell
.\build.ps1
```

The Go module path is `github.com/tyxteacat/teacat`; update it if you publish the
repository under a different account or host.

## Quick start

```powershell
# Download a model from the Ollama registry
.\teacat.exe pull qwen2.5:0.5b

# One-shot chat
.\teacat.exe run qwen2.5:0.5b "Explain goroutines in two sentences"

# Interactive chat
.\teacat.exe run qwen2.5:0.5b

# Local API server on http://127.0.0.1:11434
.\teacat.exe serve --model qwen2.5:0.5b
```

## CLI

| Command | Description |
| --- | --- |
| `teacat serve [--port 11434] [--host 127.0.0.1] [--ctx 2048] [--model NAME]` | Start the API server |
| `teacat pull <name[:tag]>` | Download from the Ollama registry |
| `teacat pull --url <gguf-url> [--name name:tag]` | Download a direct GGUF URL |
| `teacat create <name[:tag]> -f model.gguf` | Import a local GGUF file |
| `teacat list` | List installed models |
| `teacat show <model>` | Show architecture, context, template |
| `teacat run <model> [prompt]` | Chat, streaming to the terminal |
| `teacat rm <model>` | Delete a model and unreferenced blobs |

`run` flags: `--ctx`, `--n` (max tokens), `--temp`, `--top-p`, `--top-k`,
`--repeat-penalty`, `--system`.

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
`GET /api/version`.

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
