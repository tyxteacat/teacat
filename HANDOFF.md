# TeaCat — Project Handoff

This document is a complete continuation guide for another AI or developer.
Read this first, then continue from **Next actions**.

Last updated: 2026-11-07
Project: **TeaCat**
Author / developer: **Tyxen**
Organization: **tyxteacat**

---

## 1. What TeaCat is

TeaCat is a local LLM runtime written from scratch in Go — an Ollama-style tool.
It downloads GGUF models, runs inference on the CPU with its own transformer
engine (no llama.cpp, no cgo), and exposes:

- an **Ollama-compatible REST API**
- an **OpenAI-compatible chat completions API**
- a CLI (`teacat pull`, `run`, `serve`, `list`, `show`, `rm`, `create`)

Status: **working and tested**. Both APIs stream correctly and generate
sensible text on real models.

---

## 2. Canonical identity (do not change without updating everywhere)

| Item | Value |
| --- | --- |
| Project name | `TeaCat` |
| Author credit | `Tyxen` |
| GitHub org | `tyxteacat` |
| Repository | `https://github.com/tyxteacat/teacat` |
| Website | `https://tyxteacat.github.io/teacat/` |
| Go module | `github.com/tyxteacat/teacat` |
| Binary | `teacat.exe` (Windows) / `teacat` |
| Model store | `~/.teacat` (env override: `TEACAT_HOME`) |
| HTTP User-Agent | `TeaCat/0.1.0 (+https://github.com/tyxteacat/teacat)` |
| License | MIT |

Brand constants live in `internal/brand/brand.go`. URLs also appear in:
`README.md`, `llms.txt`, `CITATION.cff`, `README.md`, `site/index.html`,
`site/robots.txt`, `site/sitemap.xml`, `site/README.md`, and `go.mod`.
If the org/domain changes, update all of them.

Search phrases intentionally used in the README/site: **TeaCat**, **TeaCat
Tyxen**, **TeaCat by Tyxen**, **Tea Cat AI**, **Tyxen**, **local LLM**, **Ollama
alternative**, **GGUF**, **OpenAI-compatible**.

---

## 3. Current repository state

- Local folder: `C:\Users\sgsat\OneDrive\Desktop\Crafter` (folder name is still
  the old "Crafter"; the project and binary are TeaCat — renaming the folder is
  optional and not required for the build).
- Git initialized locally. Branch: `main`.
- Remote `origin`: `https://github.com/tyxteacat/teacat.git`
- Local git identity: `Tyxen <tyxteacat@users.noreply.github.com>`
- All work is committed locally. **Not pushed yet.**
- The GitHub org `tyxteacat` exists publicly but has **no public repositories**
  yet (`https://github.com/tyxteacat` shows "This organization has no public
  repositories").
- A Git Credential Manager OAuth prompt was shown to the user (account with org
  access to `tyxteacat`). The user needs to click **Authorize git-ecosystem**
  before any push works. This is the standard `git` credential flow — not a
  separate app.
- Git remote is configured. **Do not re-run commands that trigger OAuth blindly;
  check whether credentials already work first** (`git ls-remote origin`).

Check state:

```powershell
git status
git log --oneline -3
git remote -v
git ls-remote origin
```

Expected if auth is ready: `git ls-remote` lists refs or returns empty for an
empty/nonexistent repo. If it hangs or opens a browser, OAuth is not completed.

---

## 4. Next actions (in order)

### 4.1 Authorize Git (if not done)

The user must click **Authorize git-ecosystem** on the GitHub OAuth page that
Git Credential Manager shows. It grants read/write access, including the
`tyxteacat` organization. No token is written into this repository.

### 4.2 Create the repository

GitHub does not create repositories on push. Create `tyxteacat/teacat` first.

Option A — `gh` CLI (if installed and authenticated):

```powershell
gh auth status
gh repo create tyxteacat/teacat --public --source=. --remote=origin --push
```

If `origin` already exists, drop `--remote=origin` `--push` and push manually.

Option B — web UI:

1. Open `https://github.com/organizations/tyxteacat/repositories/new`
2. Name: `teacat`
3. Visibility: **Public**
4. Do **not** initialize with README / .gitignore / license
5. Create repository, then run:

```powershell
git push -u origin main
```

Option C — REST API (only if a token is available locally; never print or
commit it):

```powershell
$cred = "protocol=https`nhost=github.com`n`n" | git credential fill
# parse the password field into $token WITHOUT echoing it, then:
curl.exe -s -X POST https://api.github.com/orgs/tyxteacat/repos `
  -H "Authorization: Bearer $token" `
  -H "Accept: application/vnd.github+json" `
  -d '{"name":"teacat","private":false,"description":"TeaCat - local LLM runtime by Tyxen"}'
git push -u origin main
```

### 4.3 Repository metadata (important for search)

Set description and topics in **Settings → General** or via API:

Description:

```
TeaCat - local LLM runtime by Tyxen. Run GGUF models offline with Ollama + OpenAI compatible APIs, written from scratch in Go.
```

Topics:

```
teacat, tyxen, local-llm, gguf, ollama, openai-compatible, golang, inference-engine, llm-runtime
```

API equivalent:

```powershell
curl.exe -X PATCH https://api.github.com/repos/tyxteacat/teacat `
  -H "Authorization: Bearer $token" `
  -d '{"description":"TeaCat - local LLM runtime by Tyxen. Run GGUF models offline with Ollama + OpenAI compatible APIs, written from scratch in Go.","homepage":"https://tyxteacat.github.io/teacat/"}'

curl.exe -X PUT https://api.github.com/repos/tyxteacat/teacat/topics `
  -H "Authorization: Bearer $token" `
  -H "Accept: application/vnd.github+json" `
  -d '{"names":["teacat","tyxen","local-llm","gguf","ollama","openai-compatible","golang","inference-engine","llm-runtime"]}'
```

### 4.4 Enable GitHub Pages

Repository contains a static site in `site/` (with `.nojekyll`, `robots.txt`,
`sitemap.xml`, SEO meta tags and JSON-LD).

Web UI:

1. Repo → **Settings → Pages**
2. **Deploy from a branch**
3. Branch `main`, folder `/site`
4. Save

API:

```powershell
curl.exe -X POST https://api.github.com/repos/tyxteacat/teacat/pages `
  -H "Authorization: Bearer $token" `
  -H "Accept: application/vnd.github+json" `
  -d '{"source":{"branch":"main","path":"/site"}}'
```

Then verify:

```powershell
curl.exe -s https://tyxteacat.github.io/teacat/ | Select-String "TeaCat"
curl.exe -s https://tyxteacat.github.io/teacat/sitemap.xml
```

Pages may take 1–2 minutes to publish.

### 4.5 Get indexed by Google

Publishing alone does not index a new name. Do this after Pages is live:

1. Verify `https://tyxteacat.github.io/teacat/` in Google Search Console.
2. Also add it to Bing Webmaster Tools.
3. Submit `https://tyxteacat.github.io/teacat/sitemap.xml`.
4. Request indexing for the homepage.
5. Make external references (a post on X/Reddit/HN/dev.to) so "TeaCat Tyxen"
   starts appearing in real inbound links; this is the main ranking signal for a
   brand-new name.
6. Keep spelling consistent: `TeaCat` for the product, `Tyxen` for the author,
   `tyxteacat` for the org.

`site/README.md` repeats these steps for a human maintainer.

---

## 5. Build and test

Requirements: Go 1.22+ (developed with **Go 1.27.3**, installed via winget).

```powershell
go build ./...
go vet ./...
go test ./...
.\build.ps1                 # produces teacat.exe
.\teacat.exe version        # TeaCat 0.1.0
```

`build.ps1` runs:

```powershell
go build -trimpath -ldflags "-s -w" -o teacat.exe ./cmd/teacat
```

Tests:

- `internal/gguf/quant_test.go` — f16 conversion, Q4_0/Q8_0 dequant, F32 matvec,
  half→float conversion, type table
- `internal/sampler/sampler_test.go` — greedy, repetition penalty, seeded RNG
- `internal/store/store_test.go` — ref parsing
- `internal/tokenizer/template_test.go` — ChatML, Llama 3, plain templates

All currently pass. Keep them passing.

---

## 6. Verification / smoke test already performed

Environment: Windows, Intel i5-8265U, 8 threads, CPU-only.

Models installed in `~/.teacat` (moved from the old `~/.crafter`; manifests were
rewritten to the new absolute paths):

| Model | Size | Notes |
| --- | --- | --- |
| `qwen2.5:0.5b` | 379.4 MB | instruct; Q5_0-mixed quant; good answers |
| `smollm2:135m` | 258.3 MB | base model; F16 — expected to ramble in chat |

Verified outputs:

- SmolLM2 greedy: `"The capital of France is"` → ` Paris. Paris is the capital of France.`
- Qwen2.5 chat: asked capital of France → `The capital of France is Paris...`
- `teacat run qwen2.5:0.5b "Say hi in three words."` → `Hello, how can I help you today?`
- `POST /api/chat` (streaming and non-streaming) works.
- `POST /api/generate` works.
- `POST /v1/chat/completions` works, including SSE `data:` chunks and `[DONE]`.
- `GET /api/tags` returns size, digest, family, parameter size, quantization.

Measured speed on this laptop:

- SmolLM2-135M: ~14 tok/s generation, ~17 tok/s prompt
- Qwen2.5-0.5B: ~2 tok/s generation, ~2.5 tok/s prompt

Do not promise faster numbers without benchmarking.

---

## 7. Architecture map

```
cmd/teacat/main.go        CLI: serve, pull, list, run, show, rm, create
internal/brand/           name/author/version/repo/User-Agent constants
internal/gguf/            GGUF v2/v3 parser, quant kernels, info reader
internal/tokenizer/       SPM + byte-BPE tokenizers, chat templates
internal/model/           config, weights, forward pass, KV cache, RoPE, GQA
internal/sampler/         temperature/top-k/top-p/min-p/repetition
internal/engine/          generation loop, streaming detokenizer, stats
internal/store/           ~/.teacat blobs + JSON manifests
internal/registry/        registry.ollama.ai pull, URL import, SHA-256 check
internal/server/          Ollama API + OpenAI API, streaming
tools/debug/main.go       dev utility: tokenizer round-trip, logits, greedy
site/                     static landing page for GitHub Pages + SEO
```

### Key behavior

- **Load**: `gguf.Open` reads all tensors into memory. F16/BF16 tensors are
  expanded to F32 once (faster matvec); quantized tensors stay packed.
- **Matvec**: rows split across `GOMAXPROCS` workers. K-quants dequantize one
  256-element block at a time.
- **Sampling**: sampler applies repetition penalty, temperature, top-k, top-p,
  min-p; temperature 0 = greedy.
- **Chat prompt**: template detected from `tokenizer.chat_template` metadata
  (ChatML / Llama 3 / Llama 2 / Phi-3 / Gemma / Command-R / plain fallback).
- **Streaming**: `engine.Generate` holds back incomplete UTF-8 sequences between
  token decodes.
- **Server concurrency**: one loaded model; generation holds a mutex, so requests
  are serialized. `/api/tags` and `/api/version` do not need the lock.

---

## 8. Supported formats

- GGUF v2 and v3.
- Architectures: LLaMA-family (`llama`, `qwen2`, SmolLM, TinyLlama, Mistral,
  Gemma with caveats). Mixture-of-Experts is **not** supported.
- Quant types: F32, F16, BF16, Q8_0, Q4_0, Q4_1, Q5_0, Q5_1, Q2_K, Q3_K,
  Q4_K, Q5_K, Q6_K, IQ4_NL.
- OpenAI endpoint lives at `http://127.0.0.1:11434/v1` (any API key accepted).

---

## 9. Known gotchas

1. **`.gitignore`**: never add a bare `teacat` line. It matches the `cmd/teacat`
   directory and silently excludes the CLI source. Use `/teacat` and
   `/teacat.exe` (already correct). This bug happened once and was fixed.
2. **Absolute model paths** are stored in `~/.teacat/models/manifests/*.json`.
   If the store directory moves, rewrite those paths or re-pull.
3. **F16 memory**: F16 models temporarily use ~2× file size because weights are
   expanded to F32.
4. **One model at a time**; a long generation blocks other inference requests.
5. **No Jinja**: templates are detected heuristically, not executed.
6. **Windows PowerShell quoting**: when testing with `curl.exe`, pass JSON via
   `--data-binary "@file.json"`; PowerShell strips quotes on native args.
7. **Pushing to a non-existent repo** fails with "Repository not found" — create
   the repo first.
8. Do not commit tokens, credentials, or the `.teacat` model blobs.

---

## 10. Copy-paste prompt for the next AI

> You are continuing work on **TeaCat**, a from-scratch Go local LLM runtime by
> **Tyxen**, hosted at `https://github.com/tyxteacat/teacat` with a landing page
> in `site/` for `https://tyxteacat.github.io/teacat/`. The local repo is on
> branch `main`, remote `origin` is set, and everything is committed but not yet
> pushed. Read `HANDOFF.md` first. Determine whether Git credentials are
> authorized (`git ls-remote origin` must not hang). If the GitHub repo does not
> exist, create `tyxteacat/teacat` (public) without initializing it, then push
> `main`. Then set the repo description/topics, enable GitHub Pages from
> `main` `/site`, and verify the site and sitemap are live. Do not rename the
> product or change author credit. Do not commit secrets. After publishing,
> provide the user with the Search Console steps to get "TeaCat Tyxen" indexed.

---

## 11. Possible future work (not required for launch)

- SIMD/assembly matvec kernels for 2–4× speed
- mmap weights instead of heap-loading tensors
- GPU offload
- Mixture-of-Experts support
- Embeddings endpoint (`/api/embeddings`, `/v1/embeddings`)
- Tool calling / function calling
- Multi-model concurrent serving
- Publish prebuilt `teacat.exe` releases via GitHub Actions
- Add screenshots/GIF to the README and landing page

---

## 12. Repository file index

```
.gitignore
AUTHORS
CITATION.cff
LICENSE
README.md
HANDOFF.md              <- this file
build.ps1
go.mod
llms.txt
cmd/teacat/main.go
internal/brand/brand.go
internal/engine/engine.go
internal/gguf/{gguf.go,info.go,quant.go,quant_test.go}
internal/model/{model.go,math.go}
internal/registry/pull.go
internal/sampler/{sampler.go,sampler_test.go}
internal/server/server.go
internal/store/{store.go,store_test.go}
internal/tokenizer/{tokenizer.go,template.go,template_test.go}
site/{index.html,robots.txt,sitemap.xml,.nojekyll,README.md}
tools/debug/main.go
```

---

Maintained by **Tyxen**.
