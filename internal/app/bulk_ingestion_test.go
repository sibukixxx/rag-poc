package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
)

func writeCorpusFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startBulkServer starts the real wiring with filesystem sources allowed
// under a temporary share directory.
func startBulkServer(t *testing.T) (*e2eServer, string, string) {
	t.Helper()
	provider := newFakeProvider(t, 64)
	share := filepath.Join(t.TempDir(), "share")
	writeCorpusFile(t, share, "handbook/refunds.md", "# Refunds\n\nRefunds are accepted within 30 days.")
	writeCorpusFile(t, share, "handbook/ops/osaka.md", "# Shipping\n\nOrders ship from the Osaka warehouse.")
	writeCorpusFile(t, share, ".env", "API_KEY=never")
	configPath := writeE2EConfig(t, provider.baseURL())
	extra := fmt.Sprintf("sources:\n  filesystem:\n    allowed_roots: [%q]\n    workers: 2\n", filepath.Dir(share))
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Setenv("FORGEAI_E2E_PROVIDER_KEY", "fake-key")
	t.Setenv("FORGEAI_DEMO_AUTH_ENABLED", "")
	return startE2EServer(t, configPath), share, configPath
}

type progressDTO struct {
	Job struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Deleted int    `json:"deleted"`
		Scan    struct {
			Files             int `json:"files"`
			ExcludedSensitive int `json:"excluded_sensitive"`
		} `json:"scan"`
	} `json:"job"`
	Counts bulk.Counts `json:"counts"`
}

func waitForJob(t *testing.T, srv *e2eServer, jobID string) progressDTO {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var p progressDTO
	for time.Now().Before(deadline) {
		srv.getJSON(t, "/api/v1/ingestion-jobs/"+jobID, http.StatusOK, &p)
		if p.Job.Status == "completed" || p.Job.Status == "failed" || p.Job.Status == "cancelled" {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish: %+v", jobID, p)
	return p
}

func TestFilesystemSourceAPIRunsBulkIngestionInTheBackground(t *testing.T) {
	srv, share, _ := startBulkServer(t)
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Share","slug":"share"}`, http.StatusOK, &kb)

	var conn struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Root     string `json:"root"`
	}
	srv.postJSON(t, "/api/v1/source-connections",
		fmt.Sprintf(`{"knowledge_base_id":%q,"provider":"filesystem","name":"share","root":%q,"exclude":["drafts/"]}`, kb.ID, share),
		http.StatusCreated, &conn)
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	srv.postJSON(t, "/api/v1/source-connections/"+conn.ID+"/jobs", "", http.StatusAccepted, &job)
	p := waitForJob(t, srv, job.ID)

	if p.Job.Status != "completed" || p.Counts.Completed != 2 || p.Job.Scan.ExcludedSensitive != 1 {
		t.Fatalf("progress = %+v", p)
	}
	var search struct {
		Results []struct {
			Filename string `json:"filename"`
		} `json:"results"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases/"+kb.ID+"/search", `{"query":"Osaka warehouse","top_k":3}`, http.StatusOK, &search)
	if len(search.Results) == 0 || search.Results[0].Filename != "handbook/ops/osaka.md" {
		t.Fatalf("search = %+v", search.Results)
	}
	var list []struct {
		ID        string       `json:"id"`
		LatestJob *progressDTO `json:"latest_job"`
	}
	srv.getJSON(t, "/api/v1/source-connections?knowledge_base_id="+kb.ID, http.StatusOK, &list)
	if len(list) != 1 || list[0].ID != conn.ID || list[0].LatestJob == nil || list[0].LatestJob.Job.Status != "completed" {
		t.Fatalf("connections = %+v", list)
	}
}

func TestFilesystemSourceAPIRefusesRootsOutsideTheAllowlist(t *testing.T) {
	srv, _, _ := startBulkServer(t)
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Share","slug":"share"}`, http.StatusOK, &kb)

	body := srv.do(t, http.MethodPost, "/api/v1/source-connections",
		fmt.Sprintf(`{"knowledge_base_id":%q,"provider":"filesystem","root":"/etc"}`, kb.ID), "", http.StatusBadRequest)

	if got := strings.TrimSpace(string(body)); got != `root "/etc" is not under an allowed root` {
		t.Fatalf("body = %q", got)
	}
}

func TestServerStartupResumesQueuedIngestionJobs(t *testing.T) {
	srv, share, configPath := startBulkServer(t)
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Share","slug":"share"}`, http.StatusOK, &kb)
	var conn struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/source-connections",
		fmt.Sprintf(`{"knowledge_base_id":%q,"provider":"filesystem","root":%q}`, kb.ID, share), http.StatusCreated, &conn)
	// Queue a job directly (as if the process stopped before running it).
	uc, err := srv.app.BulkIngest()
	if err != nil {
		t.Fatal(err)
	}
	job, err := uc.StartJob(context.Background(), conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv.close()

	restarted := startE2EServer(t, configPath)
	restarted.app.StartBackground()

	p := waitForJob(t, restarted, job.ID)
	if p.Job.Status != "completed" || p.Counts.Completed != 2 {
		t.Fatalf("resumed job = %+v", p)
	}
}
