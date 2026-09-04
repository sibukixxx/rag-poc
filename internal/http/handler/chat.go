package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Request shape limits. The body itself is capped by MaxBytesReader in the
// router; these bound what is forwarded to the provider per request.
const (
	maxChatMessages     = 64
	maxChatContentChars = 200_000
	sseWriteDeadline    = 10 * time.Minute
)

type ChatHandler struct {
	chat *usecase.ChatUseCase
}

func NewChatHandler(chat *usecase.ChatUseCase) *ChatHandler {
	return &ChatHandler{chat: chat}
}

type chatMessageDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequestDTO struct {
	Alias    string           `json:"alias"`
	Messages []chatMessageDTO `json:"messages"`
}

type usageDTO struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type chatStreamEventDTO struct {
	Delta   string    `json:"delta,omitempty"`
	Done    bool      `json:"done,omitempty"`
	TraceID string    `json:"trace_id,omitempty"`
	Usage   *usageDTO `json:"usage,omitempty"`
	CostUSD float64   `json:"cost_usd,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// Stream handles POST /api/v1/chat, driving the Playground via
// server-sent events. A POST body (rather than GET query params) is
// required to carry a full message list, so this is a manual SSE writer
// rather than the browser EventSource API.
func (h *ChatHandler) Stream(w http.ResponseWriter, r *http.Request) {
	var req chatRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Alias == "" {
		req.Alias = "normal"
	}
	if len(req.Messages) == 0 {
		http.Error(w, "messages must not be empty", http.StatusBadRequest)
		return
	}
	if len(req.Messages) > maxChatMessages {
		http.Error(w, fmt.Sprintf("too many messages (max %d)", maxChatMessages), http.StatusBadRequest)
		return
	}

	total := 0
	messages := make([]llm.Message, len(req.Messages))
	for i, m := range req.Messages {
		switch llm.Role(m.Role) {
		case llm.RoleSystem, llm.RoleUser, llm.RoleAssistant:
		default:
			http.Error(w, "invalid message role", http.StatusBadRequest)
			return
		}
		total += len(m.Content)
		if total > maxChatContentChars {
			http.Error(w, fmt.Sprintf("messages too long (max %d chars)", maxChatContentChars), http.StatusRequestEntityTooLarge)
			return
		}
		messages[i] = llm.Message{Role: llm.Role(m.Role), Content: m.Content}
	}

	result, err := h.chat.ChatStream(r.Context(), req.Alias, messages)
	if err != nil {
		// Full detail (upstream URL, provider body) goes to the log only.
		log.Printf("chat alias=%q: %v", req.Alias, err)
		http.Error(w, "upstream provider error", http.StatusBadGateway)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// SSE responses are exempt from a global WriteTimeout, so bound them
	// here instead of leaving a slow reader attached forever.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sseWriteDeadline))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	write := func(ev chatStreamEventDTO) {
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	for ev := range result.Events {
		switch {
		case ev.Err != nil:
			log.Printf("chat stream trace=%s: %v", result.TraceID, ev.Err)
			write(chatStreamEventDTO{Error: "upstream provider error", TraceID: result.TraceID})
		case ev.Done:
			write(chatStreamEventDTO{
				Done:    true,
				TraceID: result.TraceID,
				Usage:   &usageDTO{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens},
				CostUSD: ev.CostUSD,
			})
		default:
			write(chatStreamEventDTO{Delta: ev.Delta})
		}
	}
}
