package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/adapter/vecenc"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

type KnowledgeStore struct {
	db *sql.DB
}

var _ knowledge.Store = (*KnowledgeStore)(nil)

func NewKnowledgeStore(db *sql.DB) *KnowledgeStore {
	return &KnowledgeStore{db: db}
}

func (s *KnowledgeStore) EnsureKnowledgeBase(ctx context.Context, name, slug string) (*knowledge.KnowledgeBase, error) {
	if kb, err := s.getKnowledgeBaseBySlug(ctx, slug); err == nil {
		return kb, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	kb := knowledge.KnowledgeBase{ID: uuid.NewString(), Name: name, Slug: slug, CreatedAt: time.Now()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_bases (id, name, slug, created_at) VALUES (?, ?, ?, ?)`,
		kb.ID, kb.Name, kb.Slug, kb.CreatedAt.Format(timeLayout),
	)
	if err != nil {
		return nil, fmt.Errorf("creating knowledge base %s: %w", slug, err)
	}
	return &kb, nil
}

func (s *KnowledgeStore) getKnowledgeBaseBySlug(ctx context.Context, slug string) (*knowledge.KnowledgeBase, error) {
	var kb knowledge.KnowledgeBase
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, created_at FROM knowledge_bases WHERE slug = ?`, slug,
	).Scan(&kb.ID, &kb.Name, &kb.Slug, &createdAt)
	if err != nil {
		return nil, err
	}
	kb.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &kb, nil
}

func (s *KnowledgeStore) GetKnowledgeBase(ctx context.Context, id string) (*knowledge.KnowledgeBase, error) {
	var kb knowledge.KnowledgeBase
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, created_at FROM knowledge_bases WHERE id = ?`, id,
	).Scan(&kb.ID, &kb.Name, &kb.Slug, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("knowledge base %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("loading knowledge base %s: %w", id, err)
	}
	kb.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &kb, nil
}

func (s *KnowledgeStore) ListKnowledgeBases(ctx context.Context) ([]knowledge.KnowledgeBase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, slug, created_at FROM knowledge_bases ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing knowledge bases: %w", err)
	}
	defer rows.Close()

	var kbs []knowledge.KnowledgeBase
	for rows.Next() {
		var kb knowledge.KnowledgeBase
		var createdAt string
		if err := rows.Scan(&kb.ID, &kb.Name, &kb.Slug, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning knowledge base: %w", err)
		}
		kb.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		kbs = append(kbs, kb)
	}
	return kbs, rows.Err()
}

func (s *KnowledgeStore) CreateDocument(ctx context.Context, d knowledge.Document) error {
	createdAt := d.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (id, knowledge_base_id, filename, mime_type, size_bytes, status, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.KnowledgeBaseID, d.Filename, d.MimeType, d.SizeBytes, string(d.Status), d.Error, createdAt.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating document %s: %w", d.ID, err)
	}
	return nil
}

func (s *KnowledgeStore) UpdateDocumentStatus(ctx context.Context, id string, status knowledge.DocumentStatus, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE documents SET status = ?, error = ? WHERE id = ?`, string(status), errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("updating document %s status: %w", id, err)
	}
	return nil
}

func (s *KnowledgeStore) GetDocument(ctx context.Context, id string) (*knowledge.Document, error) {
	docs, err := s.queryDocuments(ctx, `WHERE d.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("document %s: %w", id, sql.ErrNoRows)
	}
	return &docs[0], nil
}

func (s *KnowledgeStore) ListDocuments(ctx context.Context, knowledgeBaseID string) ([]knowledge.Document, error) {
	return s.queryDocuments(ctx, `WHERE d.knowledge_base_id = ? ORDER BY d.created_at DESC`, knowledgeBaseID)
}

func (s *KnowledgeStore) queryDocuments(ctx context.Context, where string, args ...any) ([]knowledge.Document, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.knowledge_base_id, d.filename, d.mime_type, d.size_bytes, d.status, d.error, d.created_at,
		       (SELECT COUNT(1) FROM chunks c WHERE c.document_id = d.id) AS chunk_count
		FROM documents d
	`+where, args...)
	if err != nil {
		return nil, fmt.Errorf("querying documents: %w", err)
	}
	defer rows.Close()

	var docs []knowledge.Document
	for rows.Next() {
		var d knowledge.Document
		var status, createdAt string
		if err := rows.Scan(&d.ID, &d.KnowledgeBaseID, &d.Filename, &d.MimeType, &d.SizeBytes, &status, &d.Error, &createdAt, &d.ChunkCount); err != nil {
			return nil, fmt.Errorf("scanning document: %w", err)
		}
		d.Status = knowledge.DocumentStatus(status)
		d.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// ReplaceChunks deletes any existing chunks for the document and inserts
// the new set in one transaction, so a re-ingest never leaves a mix of
// old and new chunks visible.
func (s *KnowledgeStore) ReplaceChunks(ctx context.Context, documentID string, chunks []knowledge.Chunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning ReplaceChunks transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE document_id = ?`, documentID); err != nil {
		return fmt.Errorf("clearing old chunks for document %s: %w", documentID, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (id, document_id, idx, text, token_count, page, heading, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("preparing chunk insert: %w", err)
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.ExecContext(ctx, c.ID, documentID, c.Index, c.Text, c.TokenCount, c.Page, c.Heading, c.Hash); err != nil {
			return fmt.Errorf("inserting chunk %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

func (s *KnowledgeStore) ListChunks(ctx context.Context, documentID string) ([]knowledge.Chunk, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, document_id, idx, text, token_count, page, heading, hash
		FROM chunks WHERE document_id = ? ORDER BY idx ASC
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("listing chunks for document %s: %w", documentID, err)
	}
	defer rows.Close()

	var chunks []knowledge.Chunk
	for rows.Next() {
		var c knowledge.Chunk
		var page sql.NullInt64
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.Index, &c.Text, &c.TokenCount, &page, &c.Heading, &c.Hash); err != nil {
			return nil, fmt.Errorf("scanning chunk: %w", err)
		}
		if page.Valid {
			n := int(page.Int64)
			c.Page = &n
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func (s *KnowledgeStore) FindEmbeddingByHash(ctx context.Context, hash, model string) ([]float32, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT vector FROM embeddings WHERE hash = ? AND model = ? LIMIT 1`, hash, model,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("looking up embedding for hash %s: %w", hash, err)
	}
	vector, err := vecenc.Decode(data)
	if err != nil {
		return nil, false, err
	}
	return vector, true, nil
}

func (s *KnowledgeStore) SaveEmbedding(ctx context.Context, chunkID, hash, model string, vector []float32) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO embeddings (chunk_id, hash, model, dims, vector)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
			hash = excluded.hash, model = excluded.model, dims = excluded.dims, vector = excluded.vector
	`, chunkID, hash, model, len(vector), vecenc.Encode(vector))
	if err != nil {
		return fmt.Errorf("saving embedding for chunk %s: %w", chunkID, err)
	}
	return nil
}
