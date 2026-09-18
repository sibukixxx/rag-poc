package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

const runtimeRequestsPerMinute = 60

type DeploymentHandler struct {
	deploy  *usecase.DeploymentUseCase
	runtime *usecase.RuntimeUseCase
	limiter *runtimeTokenLimiter
}

func NewDeploymentHandler(deploy *usecase.DeploymentUseCase, runtime *usecase.RuntimeUseCase) *DeploymentHandler {
	return &DeploymentHandler{deploy: deploy, runtime: runtime, limiter: newRuntimeTokenLimiter(runtimeRequestsPerMinute)}
}

type deploymentDTO struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	PromptName      string    `json:"prompt_name"`
	PromptVersion   int       `json:"prompt_version"`
	Alias           string    `json:"alias"`
	TopK            int       `json:"top_k"`
	Rerank          bool      `json:"rerank"`
	CreatedAt       time.Time `json:"created_at"`
}

func deploymentToDTO(d deployment.Deployment) deploymentDTO {
	return deploymentDTO{
		ID: d.ID, Slug: d.Slug, KnowledgeBaseID: d.KnowledgeBaseID,
		PromptName: d.PromptName, PromptVersion: d.PromptVersion,
		Alias: d.Alias, TopK: d.TopK, Rerank: d.Rerank, CreatedAt: d.CreatedAt,
	}
}

type runtimeTokenDTO struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Token     string     `json:"token,omitempty"`
}

func tokenToDTO(t deployment.Token) runtimeTokenDTO {
	return runtimeTokenDTO{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt, RevokedAt: t.RevokedAt}
}

func (h *DeploymentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug            string `json:"slug"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
		Alias           string `json:"alias"`
		TopK            int    `json:"top_k"`
		Rerank          bool   `json:"rerank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	d, err := h.deploy.Create(r.Context(), usecase.CreateDeploymentInput{
		Slug: req.Slug, KnowledgeBaseID: req.KnowledgeBaseID, Alias: req.Alias,
		TopK: req.TopK, Rerank: req.Rerank,
	})
	if err != nil {
		log.Printf("deployment: create: %v", err)
		if strings.Contains(err.Error(), "invalid deployment slug") ||
			strings.Contains(err.Error(), "knowledge_base_id must not be empty") ||
			strings.Contains(err.Error(), "top_k must be") ||
			strings.Contains(err.Error(), "invalid model alias") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "could not create deployment", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(deploymentToDTO(*d))
}

func (h *DeploymentHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.deploy.List(r.Context())
	if err != nil {
		log.Printf("deployment: list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]deploymentDTO, len(items))
	for i, d := range items {
		out[i] = deploymentToDTO(d)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *DeploymentHandler) IssueToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	issued, err := h.deploy.IssueToken(r.Context(), chi.URLParam(r, "id"), req.Name)
	if err != nil {
		if errors.Is(err, deployment.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		log.Printf("deployment: issue token: %v", err)
		http.Error(w, "could not issue token", http.StatusBadRequest)
		return
	}
	dto := tokenToDTO(issued.Token)
	dto.Token = issued.Secret // returned once; never persisted by the store
	// The plaintext runtime token exists only in this response. Prevent
	// browsers/proxies from caching it as ordinary API content.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(dto)
}

func (h *DeploymentHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	items, err := h.deploy.ListTokens(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, deployment.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		log.Printf("deployment: list tokens: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]runtimeTokenDTO, len(items))
	for i, t := range items {
		out[i] = tokenToDTO(t)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *DeploymentHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := h.deploy.RevokeToken(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "tokenID")); err != nil {
		if errors.Is(err, deployment.ErrNotFound) {
			http.Error(w, "token not found", http.StatusNotFound)
			return
		}
		log.Printf("deployment: revoke token: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DeploymentHandler) RuntimeSearch(w http.ResponseWriter, r *http.Request) {
	d, _, ok := h.authorizeRuntime(w, r)
	if !ok {
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query must not be empty", http.StatusBadRequest)
		return
	}
	if len(req.Query) > maxChatContentChars {
		http.Error(w, fmt.Sprintf("query too long (max %d chars)", maxChatContentChars), http.StatusRequestEntityTooLarge)
		return
	}
	results, err := h.runtime.SearchDeployment(r.Context(), d, req.Query)
	if err != nil {
		log.Printf("runtime: search deployment=%s: %v", d.Slug, err)
		http.Error(w, "search failed", http.StatusBadGateway)
		return
	}
	out := make([]searchResultDTO, len(results))
	for i, res := range results {
		out[i] = searchResultDTO{
			ChunkID: res.ChunkID, DocumentID: res.DocumentID, Filename: res.Filename,
			Text: res.Text, Score: res.Score, Page: res.Page, Heading: res.Heading,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"deployment": d.Slug, "results": out})
}

func (h *DeploymentHandler) RuntimeChat(w http.ResponseWriter, r *http.Request) {
	d, _, ok := h.authorizeRuntime(w, r)
	if !ok {
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query must not be empty", http.StatusBadRequest)
		return
	}
	if len(req.Query) > maxChatContentChars {
		http.Error(w, fmt.Sprintf("query too long (max %d chars)", maxChatContentChars), http.StatusRequestEntityTooLarge)
		return
	}

	result, err := h.runtime.ChatDeployment(r.Context(), d, req.Query)
	if err != nil {
		log.Printf("runtime: chat deployment=%s: %v", d.Slug, err)
		http.Error(w, "chat failed", http.StatusBadGateway)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sseWriteDeadline))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	write := func(ev ragChatStreamEventDTO) {
		payload, _ := json.Marshal(ev)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
	for ev := range result.Events {
		switch {
		case ev.Err != nil:
			log.Printf("runtime: chat stream trace=%s: %v", result.TraceID, ev.Err)
			write(ragChatStreamEventDTO{Error: "upstream provider error", TraceID: result.TraceID})
		case ev.Done:
			citations := make([]citationDTO, len(ev.Citations))
			for i, c := range ev.Citations {
				citations[i] = citationDTO{
					Index: c.Index, ChunkID: c.ChunkID, DocumentID: c.DocumentID,
					Filename: c.Filename, Page: c.Page, Heading: c.Heading, Text: c.Text,
				}
			}
			write(ragChatStreamEventDTO{
				Done: true, TraceID: result.TraceID,
				Usage: &usageDTO{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens},
				CostUSD: ev.CostUSD, Citations: citations, NoContext: ev.NoContext,
			})
		default:
			write(ragChatStreamEventDTO{Delta: ev.Delta})
		}
	}
}

func (h *DeploymentHandler) authorizeRuntime(w http.ResponseWriter, r *http.Request) (*deployment.Deployment, *deployment.Token, bool) {
	raw, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, nil, false
	}
	d, t, err := h.runtime.Authenticate(r.Context(), chi.URLParam(r, "slug"), raw)
	if err != nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, nil, false
	}
	if !h.limiter.Allow(t.ID) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return nil, nil, false
	}
	return d, t, true
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return parts[1], true
}

type runtimeTokenWindow struct {
	started time.Time
	count   int
}

type runtimeTokenLimiter struct {
	mu      sync.Mutex
	limit   int
	windows map[string]runtimeTokenWindow
}

func newRuntimeTokenLimiter(limit int) *runtimeTokenLimiter {
	return &runtimeTokenLimiter{limit: limit, windows: make(map[string]runtimeTokenWindow)}
}

func (l *runtimeTokenLimiter) Allow(tokenID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	window := l.windows[tokenID]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = runtimeTokenWindow{started: now}
	}
	if window.count >= l.limit {
		l.windows[tokenID] = window
		return false
	}
	window.count++
	l.windows[tokenID] = window
	return true
}
