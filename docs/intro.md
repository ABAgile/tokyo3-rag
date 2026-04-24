# RAG Service — Data Flow and Concepts

## The Big Picture

Think of it as a **very smart librarian** who has read every document and every line of code in your company, and can answer questions by pulling out the most relevant pages before writing an answer.

There are two separate workflows: **ingest** (reading and filing everything) and **query** (answering a question).

---

## Ingest — "Read and file everything"

```
Your files
    │
    ▼
┌─────────┐     ┌──────────┐     ┌──────────────┐     ┌──────────┐
│  Parse  │────▶│  Chunk   │────▶│   Embed      │────▶│  Store   │
│         │     │          │     │  (Ollama /   │     │(Postgres)│
│ go/ast  │     │ 2048 rune│     │   Voyage)    │     │          │
│ regex   │     │ windows  │     │              │     │          │
└─────────┘     └──────────┘     └──────────────┘     └──────────┘
```

**Parse** — read files and extract meaningful units:
- Documents (PDF/markdown): just extract raw text
- Code: use AST/regex to find functions, classes, methods — each becomes one node. Also record edges ("function A calls function B")

**Chunk** — documents are too long to embed whole, so slice them into overlapping ~2048-character windows. Code nodes are already naturally sized (one function = one chunk).

**Embed** — send each chunk/node to Ollama or Voyage. They return a list of ~1000 numbers (a "vector") that captures the *meaning* of that text. Similar-meaning texts get similar numbers. This is the magic that makes "how does auth work?" match "JWT validation middleware" even though the words don't overlap.

**Store** — save the text + its vector into PostgreSQL. The vector goes into a special `pgvector` column with an HNSW index so lookups are fast.

---

## Query — "Answer a question"

```
User question
      │
      ▼
 ┌─────────┐
 │ Router  │  ← "is this about code, docs, or both?"
 └────┬────┘
      │
   ┌──┴──────────────────────────────────────┐
   │                                         │
   ▼                                         ▼
┌──────────────────┐               ┌──────────────────────┐
│   Doc retrieval  │               │   Code retrieval     │
│                  │               │                      │
│ Dense (vectors)  │               │ Dense (vectors)      │
│ +                │               │ +                    │
│ Sparse (keywords)│               │ Graph BFS            │
│ → RRF merge      │               │ (follow call edges)  │
└────────┬─────────┘               └──────────┬───────────┘
         │                                    │
         └──────────────┬─────────────────────┘
                        │  RRF merge
                        ▼
               ┌─────────────────┐
               │  Top ~10 chunks │
               └────────┬────────┘
                        │
                        ▼
               ┌─────────────────┐
               │ Stuff into LLM  │
               │ prompt (Claude/ │
               │ GPT/Gemini)     │
               └────────┬────────┘
                        │
                        ▼
               streamed answer + sources
```

**Router** — quick heuristic: does the question contain `func`, `defn`, a camelCase word, `.go`? → code search. Contains "document", "wiki", "policy"? → doc search. Otherwise → search both.

**Dense retrieval** — embed the question the same way ingest did, then ask pgvector "which stored vectors are nearest to this one?" Fast cosine similarity via the HNSW index. Finds semantically similar chunks even if no exact words match.

**Sparse retrieval** — old-school keyword search using PostgreSQL full-text search (`tsvector`). Great for exact names, error codes, acronyms that dense search can miss.

**RRF (Reciprocal Rank Fusion)** — both dense and sparse return ranked lists. RRF merges them: a chunk that ranks #2 in both lists beats one that ranks #1 in only one list. Simple formula, works well.

**Graph BFS** — code only. Dense search finds the entry-point function. Then follow the "calls" and "imports" edges in `code_edges` table up to 2 hops — pulling in callees and callers. This recovers the full call chain that pure vector search would miss.

**Stuff into LLM prompt** — the top results (text content, not vectors) are concatenated into the prompt as context: *"Here are relevant pieces of code/docs. Answer this question: ..."* The LLM never touches the database — it only sees this assembled text.

**SSE streaming** — the LLM response is streamed token-by-token back to the client via Server-Sent Events, so the user sees words appearing in real time instead of waiting for the full answer.

---

## The key insight

Vectors are never shown to humans or the LLM — they're purely an index mechanism, like the card catalogue in a library. The actual text behind those vectors is what gets read and answered.
