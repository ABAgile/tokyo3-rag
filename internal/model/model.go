package model

import "time"

type DocChunk struct {
	ID         string
	SourcePath string
	SourceType string // "pdf" | "markdown" | "wiki"
	ChunkIndex int
	Content    string
	TokenCount int
	Embedding  []float32
	Metadata   map[string]any
	IndexedAt  time.Time
}

type CodeNode struct {
	ID        string
	RepoPath  string
	Language  string // "go" | "ruby" | "clojure"
	NodeType  string // "function" | "method" | "class" | "module" | "interface"
	Name      string
	Qualified string // e.g. "pkg.ReceiverType.MethodName"
	Content   string
	LineStart int
	LineEnd   int
	Embedding []float32
	IndexedAt time.Time
}

type CodeEdge struct {
	ID         string
	FromNodeID string
	ToNodeID   string
	EdgeType   string // "call" | "import" | "inherits" | "implements"
	CreatedAt  time.Time
}

type IngestJob struct {
	ID         string
	SourcePath string
	SourceType string // "code" | "doc"
	Language   string
	FileHash   string
	Status     string // "pending" | "done" | "error"
	ErrorMsg   string
	IndexedAt  *time.Time
	CreatedAt  time.Time
}
