package app

import (
	"context"
	"log"
	"path"
	"sync"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/adapter/fsscan"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// BulkIngest wires filesystem bulk ingestion (#30) with the same ingest
// pipeline (extraction, outbound guard, embeddings, FTS) as uploads.
func (a *App) BulkIngest() (*usecase.BulkIngestUseCase, error) {
	ingest, err := a.Ingest()
	if err != nil {
		return nil, err
	}
	registry := extractor.NewDefaultRegistry()
	corpus := fsscan.LocalCorpus{Supported: func(p string) bool {
		_, ok := registry.Find(path.Base(p), "")
		return ok
	}}
	uc := usecase.NewBulkIngestUseCase(sqlite.NewBulkStore(a.DB), sqlite.NewSourceStore(a.DB), ingest, corpus)
	cfg := a.Config.Sources.Filesystem
	uc.AllowedRoots = cfg.AllowedRoots
	if cfg.Workers > 0 {
		uc.Workers = cfg.Workers
	}
	if cfg.MaxFileBytes > 0 {
		uc.MaxFileBytes = cfg.MaxFileBytes
	}
	uc.Audit = a.Audit()
	return uc, nil
}

// jobRunner runs ingestion jobs one at a time in the background so no
// browser or HTTP request has to stay open. On start it first resumes
// every job left queued or interrupted by a previous process.
type jobRunner struct {
	uc     *usecase.BulkIngestUseCase
	queue  chan string
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func newJobRunner(uc *usecase.BulkIngestUseCase) *jobRunner {
	ctx, cancel := context.WithCancel(audit.WithActor(context.Background(), "system"))
	return &jobRunner{uc: uc, queue: make(chan string, 256), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (r *jobRunner) start() {
	r.once.Do(func() {
		go func() {
			defer close(r.done)
			if _, err := r.uc.ResumeUnfinished(r.ctx); err != nil && r.ctx.Err() == nil {
				log.Printf("ingestion: resuming unfinished jobs: %v", err)
			}
			for {
				select {
				case <-r.ctx.Done():
					return
				case id := <-r.queue:
					if _, err := r.uc.Run(r.ctx, id); err != nil && r.ctx.Err() == nil {
						log.Printf("ingestion: job %s: %v", id, err)
					}
				}
			}
		}()
	})
}

// Enqueue schedules a job. If the queue is full the job stays queued in the
// database and is picked up by the next resume.
func (r *jobRunner) Enqueue(jobID string) {
	r.start()
	select {
	case r.queue <- jobID:
	default:
		log.Printf("ingestion: queue full; job %s will run on the next resume", jobID)
	}
}

func (r *jobRunner) stop() {
	r.cancel()
	r.once.Do(func() { close(r.done) }) // never started
	<-r.done
}

func (a *App) jobs() (*jobRunner, error) {
	a.runnerMu.Lock()
	defer a.runnerMu.Unlock()
	if a.runner != nil {
		return a.runner, nil
	}
	uc, err := a.BulkIngest()
	if err != nil {
		return nil, err
	}
	a.runner = newJobRunner(uc)
	return a.runner, nil
}

// StartBackground resumes unfinished ingestion jobs and begins consuming
// newly queued ones. Serve calls it; the CLI does not.
func (a *App) StartBackground() {
	r, err := a.jobs()
	if err != nil {
		log.Printf("ingestion: background runner unavailable: %v", err)
		return
	}
	r.start()
}
