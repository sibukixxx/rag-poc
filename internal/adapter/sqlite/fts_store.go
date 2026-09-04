package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

// FTSStore is the keyword side of Hybrid Search: FTS5 with the trigram
// tokenizer (docs/DESIGN_REVIEW.md F-4), so Japanese and other
// non-whitespace-segmented text is searchable without a separate
// segmenter.
type FTSStore struct {
	db *sql.DB
}

func NewFTSStore(db *sql.DB) *FTSStore {
	return &FTSStore{db: db}
}

var _ retrieval.KeywordSearcher = (*FTSStore)(nil)

// minTrigramQueryRunes is the shortest query the trigram tokenizer can
// match anything with: a trigram needs 3 characters, so a 1-2 character
// query (common in Japanese: "返品", "配送") always matches zero rows via
// MATCH regardless of index content. Below this length we fall back to a
// LIKE scan instead (verified empirically — see docs/DESIGN_REVIEW.md F-4).
const minTrigramQueryRunes = 3

func (s *FTSStore) Search(ctx context.Context, knowledgeBaseID, query string, topK int) ([]retrieval.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	if len([]rune(query)) < minTrigramQueryRunes {
		return s.likeSearch(ctx, knowledgeBaseID, query, topK)
	}
	return s.matchSearch(ctx, knowledgeBaseID, query, topK)
}

// matchSearch quotes the whole query as one FTS5 phrase literal, so it's
// matched as a contiguous substring rather than parsed as FTS5 query
// syntax (which would choke on arbitrary user input containing e.g. `-`
// or unbalanced quotes).
func (s *FTSStore) matchSearch(ctx context.Context, knowledgeBaseID, query string, topK int) ([]retrieval.Result, error) {
	phrase := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`

	rows, err := s.db.QueryContext(ctx, `
		SELECT f.chunk_id, c.document_id, d.filename, c.text, c.page, c.heading, bm25(chunks_fts) AS score
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.chunk_id
		JOIN documents d ON d.id = c.document_id
		WHERE chunks_fts MATCH ? AND d.knowledge_base_id = ?
		ORDER BY score ASC
		LIMIT ?
	`, phrase, knowledgeBaseID, topK)
	if err != nil {
		return nil, fmt.Errorf("fts: MATCH query: %w", err)
	}
	defer rows.Close()
	// bm25() is negative and lower-is-better; negate so Result.Score follows
	// the "higher is better" convention used everywhere else (vecmem, RRF).
	return scanResults(rows, func(raw float64) float64 { return -raw })
}

func (s *FTSStore) likeSearch(ctx context.Context, knowledgeBaseID, query string, topK int) ([]retrieval.Result, error) {
	pattern := "%" + strings.ReplaceAll(strings.ReplaceAll(query, "%", "\\%"), "_", "\\_") + "%"

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.document_id, d.filename, c.text, c.page, c.heading, 0.0 AS score
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE c.text LIKE ? ESCAPE '\' AND d.knowledge_base_id = ?
		LIMIT ?
	`, pattern, knowledgeBaseID, topK)
	if err != nil {
		return nil, fmt.Errorf("fts: LIKE fallback query: %w", err)
	}
	defer rows.Close()
	return scanResults(rows, nil)
}

func scanResults(rows *sql.Rows, transformScore func(float64) float64) ([]retrieval.Result, error) {
	var results []retrieval.Result
	for rows.Next() {
		var r retrieval.Result
		var page sql.NullInt64
		var score float64
		if err := rows.Scan(&r.ChunkID, &r.DocumentID, &r.Filename, &r.Text, &page, &r.Heading, &score); err != nil {
			return nil, fmt.Errorf("fts: scanning row: %w", err)
		}
		if page.Valid {
			n := int(page.Int64)
			r.Page = &n
		}
		if transformScore != nil {
			score = transformScore(score)
		}
		r.Score = score
		results = append(results, r)
	}
	return results, rows.Err()
}
