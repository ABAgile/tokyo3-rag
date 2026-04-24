# RAG

A self-hosted retrieval-augmented generation service for company knowledge. Answers natural-language questions by searching two distinct knowledge sources — unstructured documents (PDF, markdown) and source code (Go, Ruby, Clojure) — then assembling the most relevant context for an LLM to reason over.

## Contents

- [Design](#design)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Embedding Providers](#embedding-providers)
- [LLM Providers](#llm-providers)
- [Ingest Pipeline](#ingest-pipeline)
  - [Documents](#documents)
  - [Code](#code)
- [Retrieval](#retrieval)
- [Configuration](#configuration)
- [HTTP API](#http-api)
- [CLI Reference](#cli-reference)
  - [query](#rag-query)
  - [ingest](#rag-ingest)
  - [jobs](#rag-jobs)
- [Technical Documentation](#technical-documentation)

---

## Design

The service combines two retrieval strategies optimised for their respective content types:

**Documents** use hybrid dense + sparse search with RRF fusion. Each document is chunked into overlapping 2048-character windows, embedded into a vector, and stored in PostgreSQL via pgvector. At query time, a dense cosine-similarity search (HNSW index) runs in parallel with a PostgreSQL full-text search (`tsvector`). The two ranked lists are merged with Reciprocal Rank Fusion (k=60).

**Code** uses AST-based graph construction plus dense vector search. Source files are parsed into nodes (functions, methods, classes, modules) and directed edges (calls, imports, inherits). Nodes are embedded and stored alongside the graph adjacency table. At query time, dense search finds the entry-point node, then a recursive BFS CTE traverses up to `RAG_GRAPH_DEPTH` hops of call/import edges to recover the full call chain — context that pure vector search cannot reconstruct.

The two result sets are merged with a final RRF pass and injected as context into the LLM prompt. The LLM never touches the database; it only sees assembled plain text.

```
┌──────────────────────────────────────────────────────────────┐
│  RAG Service                                                 │
│                                                              │
│  Ingest                Retrieval              Generation     │
│  ┌──────────┐         ┌────────────────┐     ┌───────────┐  │
│  │ Document │         │ Dense (vector) │     │  Claude / │  │
│  │ chunker  │──embed──│ Sparse (FTS)   │─RRF─│  OpenAI / │  │
│  └──────────┘         └────────────────┘     │  Gemini / │  │
│  ┌──────────┐         ┌────────────────┐     │  Ollama   │  │
│  │ Code AST │──embed──│ Dense (vector) │─RRF─│           │  │
│  │ parser   │──edges──│ Graph BFS      │     └───────────┘  │
│  └──────────┘         └────────────────┘                    │
└──────────────────────────────────────────────────────────────┘
                              │
               ┌──────────────┴──────────────┐
               │         PostgreSQL           │
               │  pgvector HNSW (docs+code)  │
               │  tsvector GIN  (doc FTS)    │
               │  code_edges adjacency table │
               └─────────────────────────────┘
```

Code parsing is entirely deterministic — no LLM is involved in graph construction:

| Language | Parser |
|---|---|
| Go | `go/ast` (stdlib) |
| Ruby | Line-based state machine with frame stack |
| Clojure | S-expression bracket scanner |

---

## Installation

Requires Go 1.26 or later and PostgreSQL with the `pgvector` extension.

```sh
# Server daemon
go install github.com/abagile/tokyo3-rag/cmd/ragd@latest

# CLI client
go install github.com/abagile/tokyo3-rag/cmd/rag@latest
```

---

## Quick Start

### 1. Start PostgreSQL with pgvector

```sh
docker run -d \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 \
  pgvector/pgvector:pg16
```

### 2. Start the server

**Voyage AI embeddings + Anthropic generation:**

```sh
RAG_DATABASE_URL=postgres://postgres:postgres@localhost/rag \
VOYAGE_API_KEY=<key> \
LLM_API_KEY=<anthropic-key> \
ragd
```

**Fully local with Ollama (no external API calls):**

```sh
ollama pull nomic-embed-text
ollama pull bge-m3
ollama pull gemma3

RAG_DATABASE_URL=postgres://postgres:postgres@localhost/rag \
EMBED_PROVIDER=ollama \
LLM_PROVIDER=ollama \
LLM_API_KEY=ignored \
ragd
```

The server runs on `:8080` by default and runs database migrations automatically on startup.

### 3. Ingest content

```sh
# Ingest a Go codebase
rag ingest /path/to/your/repo --type code --language go

# Ingest markdown docs
rag ingest /path/to/docs --type markdown

# Ingest PDFs
rag ingest /path/to/reports --type pdf
```

### 4. Query

```sh
rag query "how does token authentication work?"
rag query "what does the UserRepository class do?" --type code
rag query "what is our data retention policy?" --type doc
```

---

## Embedding Providers

Set `EMBED_PROVIDER` to select the embedding backend. The default is `voyage`.

### Voyage AI (`EMBED_PROVIDER=voyage`)

Cloud API. Two models are used automatically based on content type:

| Model | Dimension | Used for |
|---|---|---|
| `voyage-3-large` | 1024 | PDF / markdown chunks |
| `voyage-code-3` | 1536 | Code nodes |

Requires `VOYAGE_API_KEY`. Pricing is per token (~$0.02–0.18 per million tokens).

### Ollama (`EMBED_PROVIDER=ollama`)

Fully local. No API key required. Recommended models:

| Variable | Default | Purpose |
|---|---|---|
| `OLLAMA_DOC_MODEL` | `nomic-embed-text` | Documents and doc queries |
| `OLLAMA_CODE_MODEL` | `bge-m3` | Code nodes and code queries |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama server URL |

`bge-m3` is the strongest general-purpose local embedding model and handles mixed code/prose better than `nomic-embed-text` alone.

**Note:** switching embedding providers after ingesting data requires wiping and re-ingesting everything — stored vector dimensions are model-specific and cannot be mixed.

---

## LLM Providers

Set `LLM_PROVIDER` to select the generation backend. The default is `anthropic`.

| Provider | `LLM_PROVIDER` | Default model | Notes |
|---|---|---|---|
| Anthropic Claude | `anthropic` | `claude-sonnet-4-6` | Streaming via Anthropic Messages API |
| OpenAI | `openai` | `gpt-4o` | Streaming via Chat Completions API |
| Google Gemini | `gemini` | `gemini-2.0-flash` | Uses Google's OpenAI-compatible endpoint |
| Ollama (local) | `ollama` | `gemma3` | Fully local; `LLM_API_KEY` is ignored |

`LLM_API_KEY` is required for all providers except `ollama`. Override the model with `LLM_MODEL`. Ollama shares `OLLAMA_BASE_URL` with the embed provider.

---

## Ingest Pipeline

All ingest operations are asynchronous — the API returns a job ID immediately. Track progress with `rag jobs list` or `GET /api/v1/ingest/jobs`.

Re-ingesting the same path is safe. Files are skipped if their SHA-256 hash matches the last successful ingest, so only changed files are reprocessed.

### Documents

Supported source types: `pdf`, `markdown`.

Text is extracted from the file, split into overlapping 2048-character chunks (256-character overlap), embedded, and stored with a `tsvector` index for hybrid search.

For a directory path, all files in the directory (non-recursive) are processed. Subdirectories are skipped.

### Code

Supported languages: `go`, `ruby`, `clojure`. Pass an empty `language` to auto-detect by file extension (`.go`, `.rb`/`.rake`/`.gemspec`, `.clj`/`.cljs`/`.cljc`/`.edn`).

Each source file is parsed into typed nodes:

| Node type | Go | Ruby | Clojure |
|---|---|---|---|
| `function` | `func` (top-level) | `def` | `defn`, `defmacro`, `defmulti` |
| `method` | `func` with receiver | `def` inside class/module | — |
| `class` | `struct`/`interface` TypeSpec | `class` | `defrecord`, `deftype` |
| `module` | package declaration | `module` | `ns` |
| `interface` | `interface` TypeSpec | — | `defprotocol`, `definterface` |

Call edges are extracted where available (Go `ast.CallExpr`; Ruby/Clojure require edges are tracked as `import` edges). All nodes in a file are embedded in a single batch before being written to the database.

---

## Retrieval

The query router classifies each query before searching:

| Signal | Classification |
|---|---|
| Contains `.go`, `.rb`, `.clj`, `func`, `defn`, `class`, camelCase, `snake_case_name` | `code` |
| Contains `document`, `wiki`, `guide`, `policy`, `report`, `runbook` | `doc` |
| Neither, or both | `both` (searches all sources) |

Override the classification with `--type` in the CLI or `query_type` in the API.

**Graph BFS depth** — after the initial dense code search, call/import edges are traversed up to `RAG_GRAPH_DEPTH` hops (default: 2). A flat 0.9× score discount is applied to all graph-expanded neighbours. Increase depth for deeply nested call chains; decrease it to reduce noise.

---

## Configuration

### Server environment variables

**Required:**

| Variable | Description |
|---|---|
| `RAG_DATABASE_URL` | PostgreSQL DSN with pgvector installed, e.g. `postgres://user:pass@host/dbname` |
| `LLM_API_KEY` | API key for the LLM provider (not required when `LLM_PROVIDER=ollama`) |

**Embedding provider (`EMBED_PROVIDER`, default: `voyage`):**

| Variable | Default | Description |
|---|---|---|
| `EMBED_PROVIDER` | `voyage` | `voyage` or `ollama` |
| `VOYAGE_API_KEY` | — | Required when `EMBED_PROVIDER=voyage` |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama server URL |
| `OLLAMA_DOC_MODEL` | `nomic-embed-text` | Model for document/query embedding |
| `OLLAMA_CODE_MODEL` | `bge-m3` | Model for code/code-query embedding |

**LLM provider (`LLM_PROVIDER`, default: `anthropic`):**

| Variable | Default | Description |
|---|---|---|
| `LLM_PROVIDER` | `anthropic` | `anthropic`, `openai`, `gemini`, or `ollama` |
| `LLM_MODEL` | _(provider default)_ | Model override |

**Optional:**

| Variable | Default | Description |
|---|---|---|
| `RAG_ADDR` | `:8080` | TCP listen address |
| `RAG_API_TOKEN` | — | Static bearer token for API auth. Empty = no auth (development only) |
| `RAG_GRAPH_DEPTH` | `2` | BFS hop depth for code graph expansion |
| `RAG_CONTEXT_RUNES` | `400000` | Max runes of retrieved context assembled into the LLM prompt |

### CLI configuration

| Variable | Description |
|---|---|
| `RAG_SERVER_URL` | Server base URL (default: `http://localhost:8080`) |
| `RAG_API_TOKEN` | Bearer token (can also be passed with `--token`) |

---

## HTTP API

All endpoints except `/api/v1/health` require `Authorization: Bearer <RAG_API_TOKEN>` when `RAG_API_TOKEN` is set.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/health` | Liveness check — returns `{"status":"ok"}` |
| `POST` | `/api/v1/query` | Ask a question; streams SSE tokens |
| `POST` | `/api/v1/ingest` | Queue a file or directory for ingestion |
| `GET` | `/api/v1/ingest/jobs` | List all ingest jobs |
| `GET` | `/api/v1/ingest/jobs/{id}` | Get a single ingest job |
| `DELETE` | `/api/v1/nodes/{id}` | Remove a code node and its edges |

### POST /api/v1/query

```json
{ "query": "how does token auth work?", "query_type": "both" }
```

`query_type`: `doc`, `code`, or `both` (default: auto-classified from query text).

**Response** — Server-Sent Events stream:

```
data: {"type":"token","content":"Token auth is implemented..."}
data: {"type":"token","content":" in middleware.go"}
data: {"type":"sources","sources":[{"path":"internal/api/middleware.go","line_start":42,"line_end":78}]}
data: {"type":"done"}
```

### POST /api/v1/ingest

```json
{ "path": "/repos/myapp", "source_type": "code", "language": "go" }
```

`source_type`: `code`, `pdf`, or `markdown`. `language`: `go`, `ruby`, `clojure` (code only; empty = auto-detect).

**Response** — `202 Accepted`:

```json
{ "job_id": "550e8400-...", "status": "accepted" }
```

---

## CLI Reference

### `rag query`

Ask a question and stream the answer.

```sh
rag query <question> [--type doc|code|both]
```

```sh
rag query "how does the payment flow work?"
rag query "what does ProcessOrder do?" --type code
rag query "what is the incident response policy?" --type doc
```

Output: streamed answer followed by a source list with file paths and line ranges.

### `rag ingest`

Queue a file or directory for ingestion.

```sh
rag ingest <path> [--type code|pdf|markdown] [--language go|ruby|clojure]
```

```sh
# Auto-detect language by extension
rag ingest /repos/myapp --type code

# Force a specific language
rag ingest /repos/myapp --type code --language ruby

# Ingest a directory of PDFs
rag ingest /docs/reports --type pdf
```

Returns the assigned job ID immediately. The ingest runs in the background.

### `rag jobs`

Manage ingest jobs.

#### `rag jobs list`

```sh
rag jobs list
```

Returns a JSON array of all ingest jobs with their status (`pending`, `done`, `error`), source path, and file hash.

---

## Technical Documentation

| Document | Contents |
|---|---|
| [`docs/intro.md`](docs/intro.md) | ELI5 data flow — ingest and query pipelines explained without jargon |
