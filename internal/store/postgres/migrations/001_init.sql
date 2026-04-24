CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doc_chunks (
    id          TEXT        PRIMARY KEY,
    source_path TEXT        NOT NULL,
    source_type TEXT        NOT NULL,
    chunk_index INTEGER     NOT NULL,
    content     TEXT        NOT NULL,
    token_count INTEGER     NOT NULL,
    embedding   VECTOR(1024),
    ts_content  TSVECTOR,
    metadata    JSONB,
    indexed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_path, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_doc_chunks_hnsw ON doc_chunks
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX IF NOT EXISTS idx_doc_chunks_fts ON doc_chunks USING GIN (ts_content);

CREATE TABLE IF NOT EXISTS code_nodes (
    id         TEXT        PRIMARY KEY,
    repo_path  TEXT        NOT NULL,
    language   TEXT        NOT NULL,
    node_type  TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    qualified  TEXT        NOT NULL,
    content    TEXT        NOT NULL,
    line_start INTEGER     NOT NULL,
    line_end   INTEGER     NOT NULL,
    embedding  VECTOR(1536),
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_path, language, qualified)
);

CREATE INDEX IF NOT EXISTS idx_code_nodes_hnsw ON code_nodes
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX IF NOT EXISTS idx_code_nodes_name ON code_nodes (name);

CREATE TABLE IF NOT EXISTS code_edges (
    id           TEXT PRIMARY KEY,
    from_node_id TEXT NOT NULL REFERENCES code_nodes(id) ON DELETE CASCADE,
    to_node_id   TEXT NOT NULL REFERENCES code_nodes(id) ON DELETE CASCADE,
    edge_type    TEXT NOT NULL,
    UNIQUE (from_node_id, to_node_id, edge_type)
);

CREATE INDEX IF NOT EXISTS idx_code_edges_from ON code_edges (from_node_id);
CREATE INDEX IF NOT EXISTS idx_code_edges_to   ON code_edges (to_node_id);

CREATE TABLE IF NOT EXISTS ingest_jobs (
    id          TEXT        PRIMARY KEY,
    source_path TEXT        NOT NULL UNIQUE,
    source_type TEXT        NOT NULL,
    language    TEXT,
    file_hash   TEXT        NOT NULL,
    status      TEXT        NOT NULL,
    error_msg   TEXT,
    indexed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ingest_jobs_status ON ingest_jobs (status);
