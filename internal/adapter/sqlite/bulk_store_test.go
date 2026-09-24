package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

func newBulkFixture(t *testing.T) (*sql.DB, *sqlite.BulkStore, string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(ctx, "Share", "share")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conn := source.Connection{ID: "conn-fs", KnowledgeBaseID: kb.ID, Provider: "filesystem", Name: "share",
		Config: json.RawMessage(`{"root":"/srv/share"}`), Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := sqlite.NewSourceStore(db).CreateConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewBulkStore(db)
	if err := store.CreateJob(ctx, bulk.Job{ID: "job-1", ConnectionID: conn.ID, Status: bulk.JobQueued, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return db, store, conn.ID
}

func TestBulkStoreAddItemsIsIdempotentAndKeepsProgress(t *testing.T) {
	_, store, _ := newBulkFixture(t)
	ctx := context.Background()
	items := []bulk.Item{{Path: "a.md", Size: 1, ModTime: 10}, {Path: "b.md", Size: 2, ModTime: 20}}
	if err := store.AddItems(ctx, "job-1", items); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishItem(ctx, "job-1", "a.md", bulk.ItemCompleted, 1, ""); err != nil {
		t.Fatal(err)
	}

	// Re-running discovery after a restart must not reset or duplicate files.
	if err := store.AddItems(ctx, "job-1", append(items, bulk.Item{Path: "c.md", Size: 3, ModTime: 30})); err != nil {
		t.Fatal(err)
	}

	got, err := store.Counts(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (bulk.Counts{Discovered: 3, Pending: 2, Completed: 1}); got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

func TestBulkStoreClaimPendingHandsOutEachFileOnceAndResetRecoversInterruptedFiles(t *testing.T) {
	_, store, _ := newBulkFixture(t)
	ctx := context.Background()
	if err := store.AddItems(ctx, "job-1", []bulk.Item{{Path: "a.md"}, {Path: "b.md"}, {Path: "c.md"}}); err != nil {
		t.Fatal(err)
	}

	first, err := store.ClaimPending(ctx, "job-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimPending(ctx, "job-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 1 || second[0].Path != "c.md" {
		t.Fatalf("claims = %+v then %+v, want 2 files then c.md", first, second)
	}
	if more, _ := store.ClaimPending(ctx, "job-1", 2); len(more) != 0 {
		t.Fatalf("claimed %+v twice", more)
	}

	// Simulate a crash: files left processing become pending again.
	if err := store.ResetProcessing(ctx, "job-1"); err != nil {
		t.Fatal(err)
	}
	counts, _ := store.Counts(ctx, "job-1")
	if counts.Pending != 3 || counts.Processing != 0 {
		t.Fatalf("counts after reset = %+v, want 3 pending", counts)
	}
}

func TestBulkStoreRequestJobStatusDoesNotOverwriteFinishedJobs(t *testing.T) {
	_, store, _ := newBulkFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.SetJobStatus(ctx, "job-1", bulk.JobCompleted, "", now); err != nil {
		t.Fatal(err)
	}

	ok, err := store.RequestJobStatus(ctx, "job-1", []bulk.JobStatus{bulk.JobQueued, bulk.JobRunning}, bulk.JobCancelRequested, now)

	if err != nil || ok {
		t.Fatalf("cancel of a completed job = %v, %v; want refused", ok, err)
	}
	job, _ := store.GetJob(ctx, "job-1")
	if job.Status != bulk.JobCompleted || job.FinishedAt == nil {
		t.Fatalf("job = %+v, want completed with finished_at", job)
	}
}

func TestBulkStoreSaveScanAndListUnfinishedJobs(t *testing.T) {
	_, store, connID := newBulkFixture(t)
	ctx := context.Background()
	summary := bulk.ScanSummary{Files: 2, ExcludedSensitive: 1, Unsupported: 3}
	if err := store.SaveScan(ctx, "job-1", summary, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDeleted(ctx, "job-1", 4); err != nil {
		t.Fatal(err)
	}

	job, err := store.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !job.DiscoveryDone || job.Scan != summary || job.Deleted != 4 {
		t.Fatalf("job = %+v", job)
	}
	unfinished, err := store.ListUnfinishedJobs(ctx)
	if err != nil || len(unfinished) != 1 || unfinished[0].ID != "job-1" {
		t.Fatalf("unfinished = %+v, %v", unfinished, err)
	}
	jobs, err := store.ListJobs(ctx, connID, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
}

func TestBulkStoreUnseenSourceItemsListsRemovedFilesOnly(t *testing.T) {
	db, store, connID := newBulkFixture(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO source_items (id, connection_id, external_id, content_hash, synced_at) VALUES ('i1', 'conn-fs', 'kept.md', 'h', '2026-01-01T00:00:00Z')`,
		`INSERT INTO source_items (id, connection_id, external_id, content_hash, synced_at) VALUES ('i2', 'conn-fs', 'removed.md', 'h', '2026-01-01T00:00:00Z')`,
		`INSERT INTO source_items (id, connection_id, external_id, content_hash, synced_at, deleted_at) VALUES ('i3', 'conn-fs', 'already-gone.md', 'h', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AddItems(ctx, "job-1", []bulk.Item{{Path: "kept.md"}}); err != nil {
		t.Fatal(err)
	}

	got, err := store.UnseenSourceItems(ctx, "job-1", connID)

	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"removed.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unseen = %v, want %v", got, want)
	}
}
