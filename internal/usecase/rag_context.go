package usecase

import (
	"fmt"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

// ContextChunk is one source passed to the LLM, numbered so the model can
// cite it inline (e.g. "[1]") and the UI can map that number back to a
// filename/page for display (docs/ROADMAP.md W5: "引用リンク").
type ContextChunk struct {
	Index      int
	ChunkID    string
	DocumentID string
	Filename   string
	Page       *int
	Heading    string
	Text       string
}

// buildContext packs search results into numbered context blocks within a
// token budget. Results are assumed to already be ranked best-first (as
// SearchUseCase returns them), so truncation simply stops taking further
// chunks once the budget would be exceeded. At least one chunk is always
// included, even if it alone exceeds the budget — answering from a
// truncated single source beats answering from none.
func buildContext(results []retrieval.Result, tok *tokenizer.Tokenizer, budgetTokens int) ([]ContextChunk, string) {
	var chunks []ContextChunk
	var sb strings.Builder
	used := 0

	for _, r := range results {
		tokens := tok.Count(r.Text)
		if len(chunks) > 0 && used+tokens > budgetTokens {
			break
		}

		idx := len(chunks) + 1
		chunks = append(chunks, ContextChunk{
			Index: idx, ChunkID: r.ChunkID, DocumentID: r.DocumentID,
			Filename: r.Filename, Page: r.Page, Heading: r.Heading, Text: r.Text,
		})

		fmt.Fprintf(&sb, "[%d] source: %s", idx, r.Filename)
		if r.Page != nil {
			fmt.Fprintf(&sb, ", page %d", *r.Page)
		}
		sb.WriteString("\n")
		sb.WriteString(r.Text)
		sb.WriteString("\n\n")

		used += tokens
	}

	return chunks, sb.String()
}

// RAGPromptName is the Prompt Registry entry the RAG pipeline reads its
// system prompt from (docs/ROADMAP.md W6). App bootstrap seeds its v1
// content to DefaultRAGSystemPrompt so a fresh install behaves exactly
// as it did before the registry existed.
const RAGPromptName = "rag_system"

// DefaultRAGSystemPrompt is the seed content for RAGPromptName's v1, and
// the fallback RAGChatUseCase uses if the registry has no active version
// yet (e.g. a database that predates bootstrap seeding it).
const DefaultRAGSystemPrompt = `You are a helpful assistant answering questions using ONLY the numbered
context blocks provided by the user. Cite every factual claim with the
matching bracketed number (e.g. [1], [2]) right after the claim. If the
context doesn't contain the answer, say you don't know rather than
guessing or using outside knowledge. Answer in the same language as the
question.`

func buildRAGUserMessage(contextText, question string) string {
	return fmt.Sprintf("Context:\n\n%s\nQuestion: %s", contextText, question)
}
