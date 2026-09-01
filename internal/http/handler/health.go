package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type HealthHandler struct {
	db      *sql.DB
	version string
}

func NewHealthHandler(db *sql.DB, version string) *HealthHandler {
	return &HealthHandler{db: db, version: version}
}

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Database string `json:"database"`
}

func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", Version: h.version, Database: "ok"}
	status := http.StatusOK

	if err := h.db.PingContext(r.Context()); err != nil {
		resp.Status = "degraded"
		resp.Database = "unreachable"
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
