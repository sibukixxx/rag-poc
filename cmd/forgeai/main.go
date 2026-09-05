// Command forgeai is the ForgeAI single-binary CLI: `serve` runs the
// server, `doctor` checks the environment, `init` bootstraps local config
// and secrets. See docs/V0.1_SPEC.md for the full command surface as it
// grows (ingest, eval run land in later weeks).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/app"
	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "doctor":
		cmdDoctor(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "secret":
		cmdSecret(os.Args[2:])
	case "ingest":
		cmdIngest(os.Args[2:])
	case "eval":
		cmdEval(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "forgeai: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `forgeai - Self-hosted AI Application / RAG Platform

Usage:
  forgeai serve  [-config path]   Start the server (default port 8080)
  forgeai doctor [-config path]   Check environment and configuration
  forgeai init   [-config path]   Generate a master key and starter config
  forgeai secret [-config path] set <name>
                                   Store an encrypted secret (e.g. an API key).
                                   The value is read from stdin (hidden on a TTY),
                                   never from the command line, so it does not
                                   land in shell history or ps output.
  forgeai secret [-config path] delete <name>
                                   Remove a stored secret.
  forgeai ingest [-config path] -kb <slug> <dir>
                                   Ingest every supported file directly under <dir>
                                   into knowledge base <slug> (created if missing).
  forgeai eval import [-config path] -kb <slug> <dataset-name> <file.json|file.csv>
                                   Create (or reuse) a Golden Dataset scoped to
                                   knowledge base <slug> and import its cases.
  forgeai eval run [-config path] [-top-k N] [-rerank] [-judge [-alias name]] <dataset-name>
                                   Run a dataset's cases through Hybrid Search and
                                   print Recall@K / Precision@K / MRR / Hit Rate.
                                   With -judge, also answer each case via RAG and have
                                   the LLM Judge score Correctness / Groundedness /
                                   Relevance, listing low-scoring cases with reasons.

Flags:
  -config string   Path to a YAML config file (optional; sane defaults apply)`)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	fs.Parse(args)

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	if err := a.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
}

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	fs.Parse(args)

	checks := app.Doctor(*configPath)

	allOK := true
	for _, c := range checks {
		mark := "OK"
		if !c.OK {
			mark = "FAIL"
			allOK = false
		}
		fmt.Printf("%-14s %-6s %s\n", c.Name, mark, c.Info)
	}

	if !allOK {
		os.Exit(1)
	}
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "./forgeai.yaml", "path to write config YAML")
	fs.Parse(args)

	key, err := crypto.GenerateMasterKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(*configPath); err == nil {
		fmt.Printf("forgeai: %s already exists, leaving it untouched\n", *configPath)
	} else {
		const template = `server:
  port: 8080
database:
  type: sqlite
  path: ./data/forgeai.db
storage:
  type: filesystem
  path: ./data/files
security:
  encryption_key_env: FORGEAI_MASTER_KEY
`
		if err := os.WriteFile(*configPath, []byte(template), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "forgeai: writing %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		fmt.Printf("forgeai: wrote %s\n", *configPath)
	}

	fmt.Println()
	fmt.Println("Generated a master key for encrypting secrets at rest.")
	fmt.Println("Export it before running `forgeai serve`:")
	fmt.Println()
	fmt.Printf("  export FORGEAI_MASTER_KEY=%s\n", key)
	fmt.Println()
	fmt.Println("Then start the server:")
	fmt.Println()
	fmt.Printf("  forgeai serve -config %s\n", *configPath)
}

func cmdSecret(args []string) {
	// flag.Parse stops at the first non-flag argument, so -config must
	// come before the subcommand: `forgeai secret -config path set name`.
	fs := flag.NewFlagSet("secret", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	fs.Parse(args)
	rest := fs.Args()

	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai secret: expected a subcommand (set, delete)")
		os.Exit(1)
	}
	sub := rest[0]
	rest = rest[1:]

	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai secret: expected a secret name")
		os.Exit(1)
	}
	name := rest[0]

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	secrets, err := a.Secrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	switch sub {
	case "set":
		if len(rest) >= 2 {
			fmt.Fprintln(os.Stderr, "forgeai secret set: pass the value on stdin, not as an argument (it would be visible in shell history and `ps`)")
			os.Exit(1)
		}
		value, err := readSecretValue()
		if err != nil {
			fmt.Fprintf(os.Stderr, "forgeai: reading value: %v\n", err)
			os.Exit(1)
		}
		if value == "" {
			fmt.Fprintln(os.Stderr, "forgeai: secret value must not be empty")
			os.Exit(1)
		}
		if err := secrets.Set(context.Background(), name, []byte(value)); err != nil {
			fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("forgeai: stored secret %q\n", name)

	case "delete":
		if err := secrets.Delete(context.Background(), name); err != nil {
			fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("forgeai: deleted secret %q\n", name)

	default:
		fmt.Fprintf(os.Stderr, "forgeai secret: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
}

// cmdIngest ingests every supported file directly under a directory into a
// knowledge base, using the same IngestUseCase the HTTP upload endpoint
// uses. It exists so the acceptance flow in docs/V0.1_SPEC.md §9
// (`forgeai ingest ./examples/docs --kb demo`) doesn't require a running
// server plus manual curl uploads.
func cmdIngest(args []string) {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	kbSlug := fs.String("kb", "", "knowledge base slug (created if it doesn't exist)")
	fs.Parse(args)
	rest := fs.Args()

	if *kbSlug == "" {
		fmt.Fprintln(os.Stderr, "forgeai ingest: -kb is required")
		os.Exit(1)
	}
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai ingest: expected a directory argument")
		os.Exit(1)
	}
	dir := rest[0]

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: reading %s: %v\n", dir, err)
		os.Exit(1)
	}

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	kb, err := a.Knowledge().EnsureKnowledgeBase(context.Background(), *kbSlug, *kbSlug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	ingest, err := a.Ingest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	failures := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forgeai: reading %s: %v\n", path, err)
			failures++
			continue
		}
		doc, err := ingest.IngestFile(context.Background(), kb.ID, entry.Name(), "", data)
		if err != nil && doc == nil {
			fmt.Printf("%-30s FAILED   %v\n", entry.Name(), err)
			failures++
			continue
		}
		if doc.Status == knowledge.DocumentStatusFailed {
			fmt.Printf("%-30s FAILED   %s\n", entry.Name(), doc.Error)
			failures++
			continue
		}
		fmt.Printf("%-30s ready    %d chunks\n", entry.Name(), doc.ChunkCount)
	}

	if failures > 0 {
		os.Exit(1)
	}
}

func cmdEval(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai eval: expected a subcommand (import, run)")
		os.Exit(1)
	}
	switch args[0] {
	case "import":
		cmdEvalImport(args[1:])
	case "run":
		cmdEvalRun(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "forgeai eval: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// cmdEvalImport creates (or reuses) a Golden Dataset scoped to a knowledge
// base and imports its cases from a JSON or CSV file
// (docs/ROADMAP.md W7: "JSON / CSV インポート（UI + CLI）").
func cmdEvalImport(args []string) {
	fs := flag.NewFlagSet("eval import", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	kbSlug := fs.String("kb", "", "knowledge base slug the dataset's cases are scoped to")
	fs.Parse(args)
	rest := fs.Args()

	if *kbSlug == "" {
		fmt.Fprintln(os.Stderr, "forgeai eval import: -kb is required")
		os.Exit(1)
	}
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "forgeai eval import: expected <dataset-name> <file.json|file.csv>")
		os.Exit(1)
	}
	datasetName, path := rest[0], rest[1]

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: reading %s: %v\n", path, err)
		os.Exit(1)
	}

	var cases []eval.Case
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		cases, err = usecase.ParseDatasetCasesJSON(data)
	case ".csv":
		cases, err = usecase.ParseDatasetCasesCSV(data)
	default:
		fmt.Fprintf(os.Stderr, "forgeai eval import: unsupported file extension %q (expected .json or .csv)\n", filepath.Ext(path))
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	kb, err := a.Knowledge().EnsureKnowledgeBase(context.Background(), *kbSlug, *kbSlug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	datasets, _, err := a.Evaluation()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	ds, err := datasets.EnsureDataset(context.Background(), datasetName, kb.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	created, err := datasets.AddCases(context.Background(), ds.ID, cases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("forgeai: imported %d case(s) into dataset %q (kb: %s)\n", len(created), datasetName, *kbSlug)
}

// cmdEvalRun runs a dataset's cases through Hybrid Search synchronously
// and prints the aggregate Retrieval metrics
// (docs/ROADMAP.md W7 completion: "forgeai eval run demo-golden で
// Retrieval Hit Rate が出る").
func cmdEvalRun(args []string) {
	fs := flag.NewFlagSet("eval run", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	topK := fs.Int("top-k", 0, "top_k passed to Search (defaults to SearchUseCase's default)")
	rerank := fs.Bool("rerank", false, "enable LLM listwise rerank")
	judge := fs.Bool("judge", false, "also generate a RAG answer per case and score it with the LLM Judge")
	alias := fs.String("alias", "", "LLM alias used to generate answers for -judge (default: normal)")
	fs.Parse(args)
	rest := fs.Args()

	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "forgeai eval run: expected <dataset-name>")
		os.Exit(1)
	}
	datasetName := rest[0]

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	datasets, evalUC, err := a.Evaluation()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	ds, err := datasets.GetDatasetByName(context.Background(), datasetName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: dataset %q not found (import it first with `forgeai eval import`)\n", datasetName)
		os.Exit(1)
	}

	run, err := evalUC.CreateRun(context.Background(), ds.ID, usecase.RunOptions{
		TopK: *topK, Rerank: *rerank, Judge: *judge, Alias: *alias,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	if err := evalUC.Execute(context.Background(), run.ID); err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: evaluation run failed: %v\n", err)
		os.Exit(1)
	}

	final, err := datasets.GetRun(context.Background(), run.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("forgeai: eval run %s (dataset: %s, top_k: %d, rerank: %v, judge: %v)\n", final.ID, datasetName, final.TopK, final.Rerank, final.Judge)
	fmt.Printf("  Recall@K:     %.3f\n", final.RecallAtK)
	fmt.Printf("  Precision@K:  %.3f\n", final.PrecisionAtK)
	fmt.Printf("  MRR:          %.3f\n", final.MRR)
	fmt.Printf("  Hit Rate:     %.3f\n", final.HitRate)
	if !final.Judge {
		return
	}
	fmt.Printf("  Correctness:  %.3f\n", final.Correctness)
	fmt.Printf("  Groundedness: %.3f\n", final.Groundedness)
	fmt.Printf("  Relevance:    %.3f\n", final.Relevance)
	fmt.Printf("  Cost:         $%.6f (alias: %s)\n", final.CostUSD, final.Alias)

	// Low-scoring cases with the judge's reason: the W8 completion
	// criterion is that these are readable right here, without the UI.
	printLowScoringCases(datasets, ds.ID, final.ID)
}

// lowScoreThreshold is the score at or below which a judged case is
// listed as needing attention.
const lowScoreThreshold = 0.5

func printLowScoringCases(datasets eval.Store, datasetID, runID string) {
	cases, err := datasets.ListCases(context.Background(), datasetID)
	if err != nil {
		return
	}
	queries := make(map[string]string, len(cases))
	for _, c := range cases {
		queries[c.ID] = c.Query
	}
	results, err := datasets.ListCaseResults(context.Background(), runID)
	if err != nil {
		return
	}

	var low []eval.CaseResult
	for _, r := range results {
		if r.Error != "" || min(r.Correctness, r.Groundedness, r.Relevance) <= lowScoreThreshold {
			low = append(low, r)
		}
	}
	if len(low) == 0 {
		fmt.Println("\n  All cases scored above", lowScoreThreshold, "on every dimension.")
		return
	}
	fmt.Printf("\n  %d case(s) at or below %.1f (or errored):\n", len(low), lowScoreThreshold)
	for _, r := range low {
		fmt.Printf("\n  - %s\n", queries[r.CaseID])
		if r.Error != "" {
			fmt.Printf("    error: %s\n", r.Error)
			continue
		}
		fmt.Printf("    correctness %.2f / groundedness %.2f / relevance %.2f\n", r.Correctness, r.Groundedness, r.Relevance)
		fmt.Printf("    answer: %s\n", truncateRunes(r.Answer, 160))
		fmt.Printf("    reason: %s\n", r.JudgeReason)
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// readSecretValue reads one line from stdin. On an interactive terminal the
// input is not echoed; when piped (e.g. `echo "$KEY" | forgeai secret set
// openai`) it is read as a plain line.
func readSecretValue() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Enter secret value (hidden): ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
