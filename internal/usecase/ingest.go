package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

// LoaderRegistry is the subset of extractor.Registry that IngestUseCase
// needs, kept as an interface so tests can supply a fake without pulling
// in the real loaders.
type LoaderRegistry interface {
	Find(filename, mimeType string) (knowledge.Loader, bool)
}

type IngestUseCase struct {
	Store         knowledge.Store
	Loaders       LoaderRegistry
	Tokenizer     *tokenizer.Tokenizer
	ChunkerConfig tokenizer.ChunkerConfig
	Embedder      llm.Embedder
	Prices        llm.PriceTable
	Traces        trace.Store
}

func NewIngestUseCase(store knowledge.Store, loaders LoaderRegistry, tok *tokenizer.Tokenizer, embedder llm.Embedder, prices llm.PriceTable, traces trace.Store) *IngestUseCase {
	return &IngestUseCase{
		Store:         store,
		Loaders:       loaders,
		Tokenizer:     tok,
		ChunkerConfig: tokenizer.DefaultChunkerConfig(),
		Embedder:      embedder,
		Prices:        prices,
		Traces:        traces,
	}
}

// IngestFile runs one file through Load -> normalize -> chunk -> hash ->
// embed (skipping any chunk whose hash+model was already embedded) and
// persists everything. It always returns the created Document, even on
// failure, with its Status/Error reflecting what happened.
func (u *IngestUseCase) IngestFile(ctx context.Context, knowledgeBaseID, filename, mimeType string, data []byte) (*knowledge.Document, error) {
	doc := knowledge.Document{
		ID:              uuid.NewString(),
		KnowledgeBaseID: knowledgeBaseID,
		Filename:        filename,
		MimeType:        mimeType,
		SizeBytes:       int64(len(data)),
		Status:          knowledge.DocumentStatusPending,
	}
	if err := u.Store.CreateDocument(ctx, doc); err != nil {
		return nil, fmt.Errorf("creating document: %w", err)
	}

	fail := func(err error) (*knowledge.Document, error) {
		_ = u.Store.UpdateDocumentStatus(ctx, doc.ID, knowledge.DocumentStatusFailed, err.Error())
		doc.Status = knowledge.DocumentStatusFailed
		doc.Error = err.Error()
		return &doc, err
	}

	loader, ok := u.Loaders.Find(filename, mimeType)
	if !ok {
		return fail(fmt.Errorf("unsupported file type for %q", filename))
	}

	pages, err := loadWithGuard(ctx, loader, data, knowledge.FileMeta{Filename: filename, MimeType: mimeType})
	if err != nil {
		return fail(fmt.Errorf("extracting text: %w", err))
	}
	for i := range pages {
		// NFKC normalization keeps chunk hashes stable across equivalent
		// Unicode forms (e.g. full/half-width kana) and improves later
		// FTS matching (docs/DESIGN_REVIEW.md F-4).
		pages[i].Text = norm.NFKC.String(pages[i].Text)
	}

	chunkResults := u.Tokenizer.ChunkPages(pages, u.ChunkerConfig)
	if len(chunkResults) == 0 {
		return fail(fmt.Errorf("no extractable text found in %q", filename))
	}

	model := u.Embedder.Model()
	chunks := make([]knowledge.Chunk, len(chunkResults))
	for i, cr := range chunkResults {
		chunks[i] = knowledge.Chunk{
			ID:         uuid.NewString(),
			DocumentID: doc.ID,
			Index:      i,
			Text:       cr.Text,
			TokenCount: cr.TokenCount,
			Page:       cr.Page,
			Heading:    cr.Heading,
			Hash:       contentHash(cr.Text, u.ChunkerConfig, model),
		}
	}

	if err := u.Store.ReplaceChunks(ctx, doc.ID, chunks); err != nil {
		return fail(fmt.Errorf("storing chunks: %w", err))
	}

	if err := u.embedChunks(ctx, chunks, model); err != nil {
		return fail(fmt.Errorf("embedding chunks: %w", err))
	}

	if err := u.Store.UpdateDocumentStatus(ctx, doc.ID, knowledge.DocumentStatusReady, ""); err != nil {
		return nil, fmt.Errorf("marking document ready: %w", err)
	}
	doc.Status = knowledge.DocumentStatusReady
	doc.ChunkCount = len(chunks)
	return &doc, nil
}

// extractTimeout bounds how long a single loader may run. Parsers of
// untrusted input (PDF in particular) can be made to loop; the request
// must still return, even if the goroutine keeps spinning until it next
// checks ctx.
const extractTimeout = 30 * time.Second

// embedBatchSize caps how many chunks go to the embeddings API in one
// request, so a large document neither exceeds provider request limits
// nor holds the whole document's vectors in one response.
const embedBatchSize = 128

// loadWithGuard runs loader.Load with a timeout and converts a panic in the
// loader into an error so a malformed upload marks the document failed
// instead of leaking a pending row (or crashing the request).
func loadWithGuard(ctx context.Context, loader knowledge.Loader, data []byte, meta knowledge.FileMeta) (pages []knowledge.Page, err error) {
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	type result struct {
		pages []knowledge.Page
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{nil, fmt.Errorf("loader panicked: %v", r)}
			}
		}()
		p, e := loader.Load(ctx, data, meta)
		done <- result{p, e}
	}()

	select {
	case r := <-done:
		return r.pages, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("text extraction timed out after %s", extractTimeout)
	}
}

// embedChunks looks up each chunk's hash first and only calls the
// embedder for chunks that were never embedded under this model — the
// core of the "re-ingest doesn't re-embed" guarantee.
func (u *IngestUseCase) embedChunks(ctx context.Context, chunks []knowledge.Chunk, model string) error {
	type pending struct {
		index int
		chunk knowledge.Chunk
	}
	var toEmbed []pending

	for i, c := range chunks {
		if vector, found, err := u.Store.FindEmbeddingByHash(ctx, c.Hash, model); err != nil {
			return err
		} else if found {
			if err := u.Store.SaveEmbedding(ctx, c.ID, c.Hash, model, vector); err != nil {
				return err
			}
		} else {
			toEmbed = append(toEmbed, pending{index: i, chunk: c})
		}
	}

	if len(toEmbed) == 0 {
		return nil
	}

	traceID := uuid.NewString()
	start := time.Now()
	var vectors [][]float32
	var err error
	for i := 0; i < len(toEmbed) && err == nil; i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(toEmbed) {
			end = len(toEmbed)
		}
		texts := make([]string, 0, end-i)
		for _, p := range toEmbed[i:end] {
			texts = append(texts, p.chunk.Text)
		}
		var batch [][]float32
		batch, err = u.Embedder.Embed(ctx, texts)
		vectors = append(vectors, batch...)
	}
	duration := time.Since(start)

	status := trace.StatusOK
	errMsg := ""
	if err != nil {
		status = trace.StatusError
		errMsg = err.Error()
	}
	// Embedding usage isn't returned per-call the way chat usage is; cost
	// is approximated from the tokenizer's count of what was actually sent.
	inputTokens := 0
	for _, p := range toEmbed {
		inputTokens += p.chunk.TokenCount
	}
	costUSD, _, _ := u.Prices.Cost(model, llm.Usage{InputTokens: inputTokens})

	if u.Traces != nil {
		bg := context.Background()
		_ = u.Traces.CreateTrace(bg, trace.Trace{
			ID: traceID, Name: "ingest:embed", StartedAt: start,
			DurationMS: duration.Milliseconds(), Status: status, CostUSD: costUSD,
		})
		_ = u.Traces.CreateSpan(bg, trace.Span{
			ID: uuid.NewString(), TraceID: traceID, Kind: trace.SpanKindLLM, Name: "embedder.embed",
			StartedAt: start, DurationMS: duration.Milliseconds(), Model: model,
			InputTokens: inputTokens, CostUSD: costUSD, Status: status, Error: errMsg,
		})
	}

	if err != nil {
		return err
	}
	if len(vectors) != len(toEmbed) {
		return fmt.Errorf("embedder returned %d vectors for %d inputs", len(vectors), len(toEmbed))
	}

	for i, p := range toEmbed {
		if err := u.Store.SaveEmbedding(ctx, p.chunk.ID, p.chunk.Hash, model, vectors[i]); err != nil {
			return err
		}
	}
	return nil
}

// contentHash ties a chunk's cache key to its exact text plus the chunker
// config and embedding model that produced it, so changing either one
// naturally invalidates the cache instead of silently reusing a stale
// vector (docs/V0.1_SPEC.md §3).
func contentHash(text string, cfg tokenizer.ChunkerConfig, model string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%d\x00%s", text, cfg.MaxTokens, cfg.Overlap, model)
	return hex.EncodeToString(h.Sum(nil))
}
