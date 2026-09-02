// Package knowledge defines the Knowledge Base / Document / Chunk domain:
// the ingestion side of RAG (docs/V0.1_SPEC.md §1-3). Retrieval (Hybrid
// Search, VectorStore) is a separate concern added in W4.
package knowledge

import (
	"context"
	"time"
)

type KnowledgeBase struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
}

type DocumentStatus string

const (
	DocumentStatusPending DocumentStatus = "pending"
	DocumentStatusReady   DocumentStatus = "ready"
	DocumentStatusFailed  DocumentStatus = "failed"
)

type Document struct {
	ID              string
	KnowledgeBaseID string
	Filename        string
	MimeType        string
	SizeBytes       int64
	Status          DocumentStatus
	Error           string
	ChunkCount      int
	CreatedAt       time.Time
}

// Chunk is one retrievable unit of a Document. Hash identifies its content
// + chunking + embedding-model configuration, so an unchanged chunk never
// triggers a redundant (and billable) embedding call on re-ingest
// (docs/V0.1_SPEC.md §3, docs/DESIGN_REVIEW.md).
type Chunk struct {
	ID         string
	DocumentID string
	Index      int
	Text       string
	TokenCount int
	Page       *int
	Heading    string
	Hash       string
}

// Page is one unit of text a Loader extracts from a source file — a PDF
// page, a CSV row, a JSON array element, or (for formats with no natural
// pagination) the whole document.
type Page struct {
	Number  int // 1-based; 0 when the format has no page concept
	Heading string
	Text    string
}

type FileMeta struct {
	Filename string
	MimeType string
}

// Loader extracts pages of plain text from a source file. Built-in loaders
// cover TXT/MD/HTML/CSV/JSON and best-effort PDF; an external converter
// (e.g. docling) can be added later as another Loader implementation
// without touching the ingestion usecase (docs/DESIGN_REVIEW.md F-5).
type Loader interface {
	// Supports reports whether this loader handles a file, based on its
	// name (extension) and/or declared MIME type.
	Supports(filename, mimeType string) bool
	Load(ctx context.Context, data []byte, meta FileMeta) ([]Page, error)
}

// Store persists knowledge bases, documents, chunks, and their embeddings.
type Store interface {
	// EnsureKnowledgeBase returns the KB with the given slug, creating it
	// (using name) if it doesn't exist yet.
	EnsureKnowledgeBase(ctx context.Context, name, slug string) (*KnowledgeBase, error)
	GetKnowledgeBase(ctx context.Context, id string) (*KnowledgeBase, error)
	ListKnowledgeBases(ctx context.Context) ([]KnowledgeBase, error)

	CreateDocument(ctx context.Context, d Document) error
	UpdateDocumentStatus(ctx context.Context, id string, status DocumentStatus, errMsg string) error
	GetDocument(ctx context.Context, id string) (*Document, error)
	ListDocuments(ctx context.Context, knowledgeBaseID string) ([]Document, error)

	// ReplaceChunks atomically swaps a document's chunks (used both for
	// first ingest and re-ingest).
	ReplaceChunks(ctx context.Context, documentID string, chunks []Chunk) error
	ListChunks(ctx context.Context, documentID string) ([]Chunk, error)

	// FindEmbeddingByHash looks up a previously computed embedding for the
	// same content+config+model, so it can be reused instead of re-calling
	// the embedding API.
	FindEmbeddingByHash(ctx context.Context, hash, model string) (vector []float32, found bool, err error)
	SaveEmbedding(ctx context.Context, chunkID, hash, model string, vector []float32) error
}
