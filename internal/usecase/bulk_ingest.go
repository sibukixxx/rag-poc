package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

// FilesystemProvider is the source_connections.provider for local/NAS roots.
const FilesystemProvider = "filesystem"

// FileIngester extracts, chunks, and embeds one file (IngestUseCase).
type FileIngester interface {
	IngestFile(ctx context.Context, knowledgeBaseID, filename, mimeType string, data []byte) (*knowledge.Document, error)
}

// Corpus enumerates and reads files under a root without escaping it.
type Corpus interface {
	Scan(root string, rules bulk.Rules, fn func(bulk.Entry) error) (bulk.ScanSummary, error)
	ReadFile(root, rel string, maxBytes int64) ([]byte, error)
}

// FilesystemSourceConfig is stored in source_connections.config_json.
type FilesystemSourceConfig struct {
	Root    string   `json:"root"`
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// ErrInvalidSourceConfig marks operator input the server refuses (a root
// outside the allowlist, a malformed pattern). Its messages are safe to
// return to the operator.
var ErrInvalidSourceConfig = errors.New("invalid source configuration")

type invalidSourceError struct{ err error }

func (e invalidSourceError) Error() string        { return e.err.Error() }
func (e invalidSourceError) Unwrap() error        { return e.err }
func (e invalidSourceError) Is(target error) bool { return target == ErrInvalidSourceConfig }

// ErrJobStateConflict is returned when a pause/cancel/resume request does
// not apply to the job's current state.
var ErrJobStateConflict = errors.New("ingestion job state conflict")

// ConnectionStatus is one source connection with its latest job.
type ConnectionStatus struct {
	ID              string                  `json:"id"`
	KnowledgeBaseID string                  `json:"knowledge_base_id"`
	Provider        string                  `json:"provider"`
	Name            string                  `json:"name"`
	Enabled         bool                    `json:"enabled"`
	Filesystem      *FilesystemSourceConfig `json:"filesystem,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	LatestJob       *JobProgress            `json:"latest_job,omitempty"`
}

// JobProgress is what operators see for one job.
type JobProgress struct {
	Job      bulk.Job    `json:"job"`
	Counts   bulk.Counts `json:"counts"`
	Failures []bulk.Item `json:"failures"`
}

// BulkIngestUseCase runs resumable filesystem ingestion jobs (#30). It
// reuses source_connections/source_items for identity and IngestUseCase
// for extraction, chunking, embedding, and FTS.
type BulkIngestUseCase struct {
	Jobs     bulk.Store
	Sources  source.Store
	Ingester FileIngester
	Corpus   Corpus
	Audit    audit.Recorder

	// AllowedRoots bounds which host directories may become sources, so the
	// management API cannot be used to read arbitrary server files.
	AllowedRoots []string
	Workers      int
	MaxFileBytes int64
	MaxAttempts  int
	RetryDelay   time.Duration
	Now          func() time.Time
	NewID        func() string
}

func NewBulkIngestUseCase(jobs bulk.Store, sources source.Store, ingester FileIngester, corpus Corpus) *BulkIngestUseCase {
	return &BulkIngestUseCase{
		Jobs: jobs, Sources: sources, Ingester: ingester, Corpus: corpus,
		Workers: 4, MaxFileBytes: maxSourceDocumentBytes, MaxAttempts: 3, RetryDelay: time.Second,
		Now: time.Now, NewID: uuid.NewString,
	}
}

func (u *BulkIngestUseCase) resolveRoot(root string) (string, error) {
	if len(u.AllowedRoots) == 0 {
		return "", errors.New("filesystem sources are disabled: set sources.filesystem.allowed_roots")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("root %q must be absolute", root)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("root %q: %w", root, err)
	}
	for _, allowed := range u.AllowedRoots {
		base, err := filepath.EvalSymlinks(filepath.Clean(allowed))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("root %q is not under an allowed root", root)
}

// CreateFilesystemConnection registers a directory as a source of a
// knowledge base after checking it against the allowlist and rules.
func (u *BulkIngestUseCase) CreateFilesystemConnection(ctx context.Context, knowledgeBaseID, name string, cfg FilesystemSourceConfig) (*source.Connection, error) {
	root, err := u.resolveRoot(cfg.Root)
	if err != nil {
		return nil, invalidSourceError{err}
	}
	cfg.Root = root
	if err := (bulk.Rules{Include: cfg.Include, Exclude: cfg.Exclude}).Validate(); err != nil {
		return nil, invalidSourceError{err}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(root)
	}
	now := u.Now()
	conn := source.Connection{
		ID: u.NewID(), KnowledgeBaseID: knowledgeBaseID, Provider: FilesystemProvider, Name: name,
		Config: raw, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := u.Sources.CreateConnection(ctx, conn); err != nil {
		return nil, err
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceConnectionCreate, audit.OutcomeSuccess, "source_connection:"+conn.ID, map[string]string{
		"provider": FilesystemProvider, "knowledge_base_id": knowledgeBaseID, "root": root,
		"include": strings.Join(cfg.Include, ","), "exclude": strings.Join(cfg.Exclude, ","),
	})
	return &conn, nil
}

func (u *BulkIngestUseCase) filesystemConnection(ctx context.Context, connectionID string) (*source.Connection, FilesystemSourceConfig, error) {
	var cfg FilesystemSourceConfig
	conn, err := u.Sources.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, cfg, err
	}
	if conn.Provider != FilesystemProvider {
		return nil, cfg, invalidSourceError{fmt.Errorf("source connection %s is not a filesystem source", connectionID)}
	}
	if err := json.Unmarshal(conn.Config, &cfg); err != nil {
		return nil, cfg, fmt.Errorf("decoding filesystem source config: %w", err)
	}
	// Re-check on every use: the allowlist may have been narrowed since.
	if _, err := u.resolveRoot(cfg.Root); err != nil {
		return nil, cfg, invalidSourceError{err}
	}
	return conn, cfg, nil
}

// StartJob queues a job for a connection. Only one unfinished job may
// exist per connection so two runs never race on the same files.
func (u *BulkIngestUseCase) StartJob(ctx context.Context, connectionID string) (*bulk.Job, error) {
	conn, _, err := u.filesystemConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if !conn.Enabled {
		return nil, invalidSourceError{fmt.Errorf("source connection %s is disabled", connectionID)}
	}
	jobs, err := u.Jobs.ListJobs(ctx, connectionID, 20)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if !j.Status.Finished() {
			return nil, fmt.Errorf("%w: job %s is still %s for this connection", ErrJobStateConflict, j.ID, j.Status)
		}
	}
	now := u.Now()
	job := bulk.Job{ID: u.NewID(), ConnectionID: connectionID, Status: bulk.JobQueued, CreatedAt: now, UpdatedAt: now}
	if err := u.Jobs.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	recordAudit(ctx, u.Audit, audit.ActionIngestionJobControl, audit.OutcomeSuccess, "ingestion_job:"+job.ID, map[string]string{"request": "start", "connection_id": connectionID})
	return &job, nil
}

func (u *BulkIngestUseCase) control(ctx context.Context, jobID, request string, from []bulk.JobStatus, to bulk.JobStatus) error {
	ok, err := u.Jobs.RequestJobStatus(ctx, jobID, from, to, u.Now())
	if err != nil {
		return err
	}
	outcome := audit.OutcomeSuccess
	if !ok {
		outcome = audit.OutcomeDenied
	}
	recordAudit(ctx, u.Audit, audit.ActionIngestionJobControl, outcome, "ingestion_job:"+jobID, map[string]string{"request": request})
	if !ok {
		return fmt.Errorf("%w: job %s cannot be %s in its current state", ErrJobStateConflict, jobID, request+"d")
	}
	return nil
}

// Pause asks a running job to stop after the current batch of files.
func (u *BulkIngestUseCase) Pause(ctx context.Context, jobID string) error {
	return u.control(ctx, jobID, "pause", []bulk.JobStatus{bulk.JobQueued, bulk.JobRunning}, bulk.JobPauseRequested)
}

// Cancel stops a job permanently; files already ingested stay indexed.
func (u *BulkIngestUseCase) Cancel(ctx context.Context, jobID string) error {
	return u.control(ctx, jobID, "cancel",
		[]bulk.JobStatus{bulk.JobQueued, bulk.JobRunning, bulk.JobPauseRequested, bulk.JobPaused}, bulk.JobCancelRequested)
}

// Resume re-queues a paused job; Run (or ResumeUnfinished) continues it.
func (u *BulkIngestUseCase) Resume(ctx context.Context, jobID string) error {
	return u.control(ctx, jobID, "resume", []bulk.JobStatus{bulk.JobPaused}, bulk.JobQueued)
}

func (u *BulkIngestUseCase) Progress(ctx context.Context, jobID string) (JobProgress, error) {
	job, err := u.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return JobProgress{}, err
	}
	counts, err := u.Jobs.Counts(ctx, jobID)
	if err != nil {
		return JobProgress{}, err
	}
	failures, err := u.Jobs.ListItems(ctx, jobID, bulk.ItemFailed, 50)
	if err != nil {
		return JobProgress{}, err
	}
	if failures == nil {
		failures = []bulk.Item{}
	}
	return JobProgress{Job: *job, Counts: counts, Failures: failures}, nil
}

// ListConnections returns a knowledge base's source connections with the
// progress of each one's most recent job.
func (u *BulkIngestUseCase) ListConnections(ctx context.Context, knowledgeBaseID string) ([]ConnectionStatus, error) {
	conns, err := u.Sources.ListConnections(ctx, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionStatus, 0, len(conns))
	for _, c := range conns {
		st := ConnectionStatus{ID: c.ID, KnowledgeBaseID: c.KnowledgeBaseID, Provider: c.Provider, Name: c.Name, Enabled: c.Enabled, CreatedAt: c.CreatedAt}
		if c.Provider == FilesystemProvider {
			var cfg FilesystemSourceConfig
			if json.Unmarshal(c.Config, &cfg) == nil {
				st.Filesystem = &cfg
			}
		}
		jobs, err := u.Jobs.ListJobs(ctx, c.ID, 1)
		if err != nil {
			return nil, err
		}
		if len(jobs) == 1 {
			p, err := u.Progress(ctx, jobs[0].ID)
			if err != nil {
				return nil, err
			}
			st.LatestJob = &p
		}
		out = append(out, st)
	}
	return out, nil
}

// ListJobs returns a connection's recent jobs, newest first.
func (u *BulkIngestUseCase) ListJobs(ctx context.Context, connectionID string) ([]bulk.Job, error) {
	jobs, err := u.Jobs.ListJobs(ctx, connectionID, 50)
	if jobs == nil {
		jobs = []bulk.Job{}
	}
	return jobs, err
}

// ResumeUnfinished runs every queued or interrupted job to completion, one
// at a time. The server calls it at startup so work survives restarts.
func (u *BulkIngestUseCase) ResumeUnfinished(ctx context.Context) (int, error) {
	jobs, err := u.Jobs.ListUnfinishedJobs(ctx)
	if err != nil {
		return 0, err
	}
	for i, j := range jobs {
		if _, err := u.Run(ctx, j.ID); err != nil {
			return i, err
		}
	}
	return len(jobs), nil
}

// Run discovers, processes, and reconciles one job. It returns early (with
// the job paused or cancelled) when an operator asked for that, and with
// ctx.Err() when the context ends; a later Run resumes where it stopped.
func (u *BulkIngestUseCase) Run(ctx context.Context, jobID string) (*bulk.Job, error) {
	job, err := u.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case bulk.JobQueued, bulk.JobRunning:
	case bulk.JobCancelRequested:
		return u.finish(ctx, job.ID, bulk.JobCancelled, "")
	case bulk.JobPauseRequested:
		return u.finish(ctx, job.ID, bulk.JobPaused, "")
	default:
		return job, nil
	}
	conn, cfg, err := u.filesystemConnection(ctx, job.ConnectionID)
	if err != nil {
		return u.finish(ctx, job.ID, bulk.JobFailed, err.Error())
	}
	if err := u.Jobs.SetJobStatus(ctx, job.ID, bulk.JobRunning, "", u.Now()); err != nil {
		return nil, err
	}
	if err := u.Jobs.ResetProcessing(ctx, job.ID); err != nil {
		return nil, err
	}

	if !job.DiscoveryDone {
		if err := u.discover(ctx, job.ID, cfg); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return u.finish(ctx, job.ID, bulk.JobFailed, "scanning corpus: "+err.Error())
		}
	}

	for {
		if stop, st := u.stopRequested(ctx, job.ID); stop {
			return u.finish(ctx, job.ID, st, "")
		}
		items, err := u.Jobs.ClaimPending(ctx, job.ID, u.workers()*2)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		u.processBatch(ctx, *conn, cfg, items)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	if err := u.reconcileRemoved(ctx, job.ID, conn.ID); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return u.finish(ctx, job.ID, bulk.JobFailed, "reconciling removed files: "+err.Error())
	}
	counts, err := u.Jobs.Counts(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	lastErr := ""
	if counts.Failed > 0 {
		lastErr = fmt.Sprintf("%d file(s) failed", counts.Failed)
	}
	return u.finish(ctx, job.ID, bulk.JobCompleted, lastErr)
}

func (u *BulkIngestUseCase) workers() int {
	if u.Workers < 1 {
		return 1
	}
	return u.Workers
}

func (u *BulkIngestUseCase) finish(ctx context.Context, jobID string, status bulk.JobStatus, lastErr string) (*bulk.Job, error) {
	if err := u.Jobs.SetJobStatus(context.WithoutCancel(ctx), jobID, status, lastErr, u.Now()); err != nil {
		return nil, err
	}
	return u.Jobs.GetJob(context.WithoutCancel(ctx), jobID)
}

func (u *BulkIngestUseCase) stopRequested(ctx context.Context, jobID string) (bool, bulk.JobStatus) {
	job, err := u.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return false, ""
	}
	switch job.Status {
	case bulk.JobPauseRequested:
		return true, bulk.JobPaused
	case bulk.JobCancelRequested:
		return true, bulk.JobCancelled
	}
	return false, ""
}

const discoveryBatch = 500

func (u *BulkIngestUseCase) discover(ctx context.Context, jobID string, cfg FilesystemSourceConfig) error {
	batch := make([]bulk.Item, 0, discoveryBatch)
	flush := func() error {
		if err := u.Jobs.AddItems(ctx, jobID, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return ctx.Err()
	}
	summary, err := u.Corpus.Scan(cfg.Root, bulk.Rules{Include: cfg.Include, Exclude: cfg.Exclude}, func(e bulk.Entry) error {
		batch = append(batch, bulk.Item{Path: e.Path, Size: e.Size, ModTime: e.ModTime})
		if len(batch) == discoveryBatch {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return u.Jobs.SaveScan(ctx, jobID, summary, true, u.Now())
}

func (u *BulkIngestUseCase) processBatch(ctx context.Context, conn source.Connection, cfg FilesystemSourceConfig, items []bulk.Item) {
	sem := make(chan struct{}, u.workers())
	var wg sync.WaitGroup
	for _, it := range items {
		if ctx.Err() != nil {
			break // unclaimed-but-marked files are reset to pending on resume
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(it bulk.Item) {
			defer wg.Done()
			defer func() { <-sem }()
			u.processItem(ctx, conn, cfg, it)
		}(it)
	}
	wg.Wait()
}

type fileMeta struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mtime"`
}

func (u *BulkIngestUseCase) processItem(ctx context.Context, conn source.Connection, cfg FilesystemSourceConfig, it bulk.Item) {
	status, attempts, err := u.ingestFile(ctx, conn, cfg, it)
	if err != nil && ctx.Err() != nil {
		return // interrupted: leave it processing; ResetProcessing requeues it
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = u.Jobs.FinishItem(context.WithoutCancel(ctx), it.JobID, it.Path, status, attempts, msg)
}

func (u *BulkIngestUseCase) ingestFile(ctx context.Context, conn source.Connection, cfg FilesystemSourceConfig, it bulk.Item) (bulk.ItemStatus, int, error) {
	existing, err := u.Sources.GetItem(ctx, conn.ID, it.Path)
	if err != nil && !errors.Is(err, source.ErrNotFound) {
		return bulk.ItemFailed, 1, err
	}
	live := err == nil && existing.DeletedAt == nil && existing.DocumentID != ""
	meta, _ := json.Marshal(fileMeta{Size: it.Size, ModTime: it.ModTime})
	if live {
		var prev fileMeta
		if json.Unmarshal(existing.Metadata, &prev) == nil && prev.Size == it.Size && prev.ModTime == it.ModTime {
			return bulk.ItemSkipped, 0, nil // unchanged size and mtime: not even read
		}
	}

	var lastErr error
	attempts := 0
	for attempts < u.maxAttempts() {
		attempts++
		status, err := u.ingestOnce(ctx, conn, cfg, it, existing, live, meta)
		if err == nil {
			return status, attempts, nil
		}
		lastErr = err
		if ctx.Err() != nil || permanentIngestError(err) {
			break
		}
		if attempts < u.maxAttempts() && u.RetryDelay > 0 {
			select {
			case <-time.After(u.RetryDelay * time.Duration(attempts)):
			case <-ctx.Done():
				return bulk.ItemFailed, attempts, ctx.Err()
			}
		}
	}
	return bulk.ItemFailed, attempts, lastErr
}

func (u *BulkIngestUseCase) maxAttempts() int {
	if u.MaxAttempts < 1 {
		return 1
	}
	return u.MaxAttempts
}

func permanentIngestError(err error) bool {
	return errors.Is(err, ErrUnprocessableDocument) || errors.Is(err, outbound.ErrBlocked)
}

func (u *BulkIngestUseCase) ingestOnce(ctx context.Context, conn source.Connection, cfg FilesystemSourceConfig, it bulk.Item, existing *source.Item, live bool, meta []byte) (bulk.ItemStatus, error) {
	data, err := u.Corpus.ReadFile(cfg.Root, it.Path, u.MaxFileBytes)
	if err != nil {
		return bulk.ItemFailed, unprocessable(fmt.Errorf("reading file: %w", err))
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	now := u.Now()
	if live && existing.ContentHash == hash {
		// Touched but identical: refresh size/mtime without re-embedding.
		existing.Metadata = meta
		existing.SyncedAt = now
		if err := u.Sources.ReplaceItemDocument(ctx, *existing); err != nil {
			return bulk.ItemFailed, err
		}
		return bulk.ItemSkipped, nil
	}

	doc, err := u.Ingester.IngestFile(ctx, conn.KnowledgeBaseID, it.Path, "", data)
	if err != nil {
		if doc != nil {
			_ = u.Sources.DeleteDocument(context.WithoutCancel(ctx), doc.ID)
		}
		return bulk.ItemFailed, err
	}
	itemID := u.NewID()
	if existing != nil {
		itemID = existing.ID
	}
	// The file is already embedded; commit its mapping even if the run is
	// being interrupted, so the work is not thrown away and redone.
	if err := u.Sources.ReplaceItemDocument(context.WithoutCancel(ctx), source.Item{
		ID: itemID, ConnectionID: conn.ID, ExternalID: it.Path, DocumentID: doc.ID, Title: it.Path,
		Metadata: meta, ContentHash: hash, SyncedAt: now,
	}); err != nil {
		_ = u.Sources.DeleteDocument(context.WithoutCancel(ctx), doc.ID)
		return bulk.ItemFailed, fmt.Errorf("mapping file to document: %w", err)
	}
	return bulk.ItemCompleted, nil
}

// reconcileRemoved tombstones files that disappeared from the corpus. It
// only runs after a complete scan, so a partial scan never deletes data.
func (u *BulkIngestUseCase) reconcileRemoved(ctx context.Context, jobID, connectionID string) error {
	job, err := u.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if !job.DiscoveryDone {
		return nil
	}
	removed, err := u.Jobs.UnseenSourceItems(ctx, jobID, connectionID)
	if err != nil {
		return err
	}
	deleted := 0
	for _, externalID := range removed {
		ok, err := u.Sources.DeleteItemDocument(ctx, connectionID, externalID, u.Now())
		if err != nil {
			return err
		}
		if ok {
			deleted++
		}
	}
	if deleted > 0 {
		if err := u.Jobs.AddDeleted(ctx, jobID, deleted); err != nil {
			return err
		}
		recordAudit(ctx, u.Audit, audit.ActionIngestionJobControl, audit.OutcomeSuccess, "ingestion_job:"+jobID,
			map[string]string{"request": "reconcile", "deleted": strconv.Itoa(deleted)})
	}
	return nil
}
