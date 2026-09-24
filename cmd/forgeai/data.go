package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/app"
	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
)

const dataUsage = `  forgeai data delete-document [-config path] [-compact] <document-id>
                                   Delete one document and its chunks, embeddings,
                                   FTS rows, and source-sync records.
  forgeai data delete-kb [-config path] [-include-deployments] [-compact] -kb <slug>
                                   Delete a knowledge base and everything derived from
                                   it (documents, datasets, runs, source connections).
                                   Refuses while Deployments exist unless
                                   -include-deployments is given.
  forgeai data retention [-config path]
                                   Delete traces / evaluation runs older than the
                                   retention.* settings (0 days keeps them).
  forgeai data compact [-config path]
                                   Merge the FTS index, checkpoint the WAL, and VACUUM
                                   so deleted text no longer remains in the database
                                   files. Backups made earlier are NOT affected.`

// cmdData is the operator path for customer-data deletion and retention
// (#23, docs/security/DATA_LIFECYCLE.md).
func cmdData(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai data: expected a subcommand (delete-document, delete-kb, retention, compact)")
		os.Exit(1)
	}
	sub := args[0]
	fs := flag.NewFlagSet("data "+sub, flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	compact := fs.Bool("compact", false, "compact the database after deleting")
	includeDeployments := fs.Bool("include-deployments", false, "also delete Deployments serving the knowledge base")
	kbSlug := fs.String("kb", "", "knowledge base slug")
	fs.Parse(args[1:])

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()
	ctx := context.Background()
	uc := a.DataLifecycle()

	var out any
	switch sub {
	case "delete-document":
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "forgeai data delete-document: expected exactly one document ID")
			os.Exit(1)
		}
		out, err = uc.DeleteDocument(ctx, fs.Arg(0))
	case "delete-kb":
		if *kbSlug == "" {
			fmt.Fprintln(os.Stderr, "forgeai data delete-kb: -kb is required")
			os.Exit(1)
		}
		kbID, lookupErr := knowledgeBaseIDBySlug(ctx, a, *kbSlug)
		if lookupErr != nil {
			fmt.Fprintf(os.Stderr, "forgeai data delete-kb: %v\n", lookupErr)
			os.Exit(1)
		}
		out, err = uc.DeleteKnowledgeBase(ctx, kbID, *includeDeployments)
		if errors.Is(err, lifecycle.ErrKnowledgeBaseHasDeployments) {
			fmt.Fprintln(os.Stderr, "forgeai data delete-kb: knowledge base still serves Deployments; rerun with -include-deployments to delete them too")
			os.Exit(1)
		}
	case "retention":
		out, err = uc.ApplyRetention(ctx)
	case "compact":
		*compact = true
		out = map[string]string{"status": "compacted"}
	default:
		fmt.Fprintf(os.Stderr, "forgeai data: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai data %s: %v\n", sub, err)
		os.Exit(1)
	}
	if *compact {
		if err := uc.Compact(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "forgeai data %s: compacting: %v\n", sub, err)
			os.Exit(1)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func knowledgeBaseIDBySlug(ctx context.Context, a *app.App, slug string) (string, error) {
	kbs, err := sqlite.NewKnowledgeStore(a.DB).ListKnowledgeBases(ctx)
	if err != nil {
		return "", err
	}
	for _, kb := range kbs {
		if kb.Slug == slug {
			return kb.ID, nil
		}
	}
	return "", fmt.Errorf("knowledge base %q not found", slug)
}
