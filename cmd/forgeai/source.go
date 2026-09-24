package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/app"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

const sourceUsage = `  forgeai source add-fs [-config path] -kb <slug> [-name n] [-include p]... [-exclude p]... <root>
                                   Register a directory (under sources.filesystem.allowed_roots)
                                   as a recursive source of knowledge base <slug>.
  forgeai source list [-config path] -kb <slug>
                                   List source connections and their latest job.
  forgeai source sync [-config path] <connection-id>
                                   Run a bulk ingestion job in the foreground, printing progress.
                                   An interrupted run resumes with: forgeai source resume <job-id>
  forgeai source job [-config path] <job-id>
                                   Show a job's progress and failed files.
  forgeai source pause|cancel|resume [-config path] <job-id>
                                   Control a job. resume continues a paused or interrupted job.`

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func cmdSource(args []string) {
	if len(args) < 1 {
		fail("forgeai source: expected a subcommand (add-fs, list, sync, job, pause, cancel, resume)")
	}
	sub := args[0]
	fs := flag.NewFlagSet("source "+sub, flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	kbSlug := fs.String("kb", "", "knowledge base slug")
	name := fs.String("name", "", "connection name")
	var include, exclude stringList
	fs.Var(&include, "include", "include pattern (repeatable)")
	fs.Var(&exclude, "exclude", "exclude pattern (repeatable)")
	fs.Parse(args[1:])

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fail("forgeai: %v", err)
	}
	defer a.Close()
	uc, err := a.BulkIngest()
	if err != nil {
		fail("forgeai: %v", err)
	}
	ctx := cliContext()
	arg := func(what string) string {
		if fs.NArg() != 1 {
			fail("forgeai source %s: expected exactly one %s", sub, what)
		}
		return fs.Arg(0)
	}

	switch sub {
	case "add-fs":
		if *kbSlug == "" {
			fail("forgeai source add-fs: -kb is required")
		}
		root := arg("root directory")
		kb, err := a.Knowledge().EnsureKnowledgeBase(ctx, *kbSlug, *kbSlug)
		if err != nil {
			fail("forgeai source add-fs: %v", err)
		}
		conn, err := uc.CreateFilesystemConnection(ctx, kb.ID, *name, usecase.FilesystemSourceConfig{Root: root, Include: include, Exclude: exclude})
		if err != nil {
			fail("forgeai source add-fs: %v", err)
		}
		fmt.Printf("forgeai: added filesystem source %s (%s)\n", conn.ID, conn.Name)
		fmt.Printf("run it with: forgeai source sync %s\n", conn.ID)
	case "list":
		if *kbSlug == "" {
			fail("forgeai source list: -kb is required")
		}
		kbID, err := knowledgeBaseIDBySlug(ctx, a, *kbSlug)
		if err != nil {
			fail("forgeai source list: %v", err)
		}
		conns, err := uc.ListConnections(ctx, kbID)
		if err != nil {
			fail("forgeai source list: %v", err)
		}
		printJSON(conns)
	case "sync":
		job, err := uc.StartJob(ctx, arg("connection ID"))
		if err != nil {
			fail("forgeai source sync: %v", err)
		}
		fmt.Fprintf(os.Stderr, "forgeai: job %s started\n", job.ID)
		runWithProgress(ctx, uc, job.ID)
	case "resume":
		jobID := arg("job ID")
		p, err := uc.Progress(ctx, jobID)
		if err != nil {
			fail("forgeai source resume: %v", err)
		}
		if p.Job.Status == "paused" {
			if err := uc.Resume(ctx, jobID); err != nil {
				fail("forgeai source resume: %v", err)
			}
		}
		runWithProgress(ctx, uc, jobID)
	case "job":
		p, err := uc.Progress(ctx, arg("job ID"))
		if err != nil {
			fail("forgeai source job: %v", err)
		}
		printJSON(p)
	case "pause", "cancel":
		jobID := arg("job ID")
		control := uc.Pause
		if sub == "cancel" {
			control = uc.Cancel
		}
		if err := control(ctx, jobID); err != nil {
			fail("forgeai source %s: %v", sub, err)
		}
		fmt.Printf("forgeai: %s requested for job %s (applied after the current batch)\n", sub, jobID)
	default:
		fail("forgeai source: unknown subcommand %q", sub)
	}
}

// runWithProgress runs a job in the foreground and reports progress every
// few seconds. Ctrl-C interrupts it; the job resumes where it stopped.
func runWithProgress(ctx context.Context, uc *usecase.BulkIngestUseCase, jobID string) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if p, err := uc.Progress(context.Background(), jobID); err == nil {
					c := p.Counts
					fmt.Fprintf(os.Stderr, "  discovered=%d completed=%d skipped=%d failed=%d pending=%d\n",
						c.Discovered, c.Completed, c.Skipped, c.Failed, c.Pending+c.Processing)
				}
			}
		}
	}()
	_, err := uc.Run(ctx, jobID)
	close(done)
	if err != nil {
		fail("forgeai: job %s stopped: %v", jobID, err)
	}
	p, err := uc.Progress(context.Background(), jobID)
	if err != nil {
		fail("forgeai: %v", err)
	}
	printJSON(p)
	if p.Counts.Failed > 0 {
		os.Exit(2)
	}
}
