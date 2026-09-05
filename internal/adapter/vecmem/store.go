// Package vecmem implements Embedded-mode vector search: a brute-force
// cosine scan over the embeddings already stored in SQLite by the
// ingestion pipeline (docs/DESIGN_REVIEW.md F-6). It reads directly from
// the database rather than keeping a separate in-memory index, which is
// simple and fine up to roughly the tens-of-thousands-of-chunks range
// documented as Embedded mode's ceiling; a pgvector/Qdrant adapter
// (Production mode, W4.5+) takes over beyond that.
package vecmem

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"

	"github.com/sibukixxx/rag-poc/internal/adapter/vecenc"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

var _ retrieval.VectorSearcher = (*Store)(nil)

func (s *Store) Search(ctx context.Context, knowledgeBaseID string, queryVector []float32, topK int) ([]retrieval.Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.chunk_id, c.document_id, d.filename, c.text, c.page, c.heading, e.vector
		FROM embeddings e
		JOIN chunks c ON c.id = e.chunk_id
		JOIN documents d ON d.id = c.document_id
		WHERE d.knowledge_base_id = ?
	`, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("vecmem: querying embeddings: %w", err)
	}
	defer rows.Close()

	var results []retrieval.Result
	for rows.Next() {
		var r retrieval.Result
		var page sql.NullInt64
		var vecBytes []byte
		if err := rows.Scan(&r.ChunkID, &r.DocumentID, &r.Filename, &r.Text, &page, &r.Heading, &vecBytes); err != nil {
			return nil, fmt.Errorf("vecmem: scanning row: %w", err)
		}
		if page.Valid {
			n := int(page.Int64)
			r.Page = &n
		}

		vector, err := vecenc.Decode(vecBytes)
		if err != nil {
			return nil, fmt.Errorf("vecmem: decoding vector for chunk %s: %w", r.ChunkID, err)
		}
		r.Score = cosineSimilarity(queryVector, vector)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
