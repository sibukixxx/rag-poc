-- W4: keyword search side of Hybrid Search. Uses the FTS5 'trigram'
-- tokenizer so Japanese (and other non-whitespace-segmented) text is
-- searchable without a separate segmenter (docs/DESIGN_REVIEW.md F-4).
--
-- This is a standalone (non "content=") FTS5 table: chunks.id is a TEXT
-- primary key, and mapping a TEXT key to FTS5's required INTEGER
-- content_rowid adds complexity for no benefit at v0.1 scale. chunk_id/
-- document_id are stored UNINDEXED and kept in sync manually by
-- KnowledgeStore.ReplaceChunks (delete-by-document then re-insert),
-- mirroring how the `chunks` table itself is replaced on re-ingest.
--
-- Trigram tokenization needs at least 3 characters to produce a trigram,
-- so a 1-2 character query (common in Japanese, e.g. "返品", "配送")
-- matches nothing via MATCH; the keyword searcher falls back to a LIKE
-- scan for such queries (see internal/adapter/sqlite/fts_store.go).
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    chunk_id UNINDEXED,
    document_id UNINDEXED,
    text,
    tokenize = 'trigram'
);
