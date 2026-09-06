// Command forgeai is the ForgeAI single-binary CLI: `serve` runs the
// server, `doctor` checks the environment, `init` bootstraps local config
// and secrets. See docs/V0.1_SPEC.md for the full command surface as it
// grows (ingest, eval run land in later weeks).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/app"
	"github.com/sibukixxx/rag-poc/internal/domain/evaluation"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
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
  forgeai eval run --dataset golden.json [--output run.json]
                   [-config path] [--top-k 1,3,5,10] [--rerank]
                   [--results fixed-results.json]
                                   Evaluate retrieval and write schema v1 evidence.
  forgeai eval compare --baseline run-a.json --candidate run-b.json
                                   Compare metrics and per-query regressions.
  forgeai eval check --baseline run-a.json --candidate run-b.json
                   [--min-mrr-delta -0.05] [--max-regressed-ratio 0.1]
                                   Exit non-zero when user-supplied gates fail.

Flags:
  -config string   Path to a YAML config file (optional; sane defaults apply)`)
}

type fixedResults map[string][]retrieval.Result
type fixedEvaluationSearcher struct{ results fixedResults }
func (f fixedEvaluationSearcher) Search(_ context.Context, _ string, query string, _ retrieval.Options) ([]retrieval.Result,error){return f.results[query],nil}

func cmdEval(args []string) {
	if len(args)==0 { fmt.Fprintln(os.Stderr,"forgeai eval: expected run, compare, or check"); os.Exit(1) }
	switch args[0] { case "run": evalRun(args[1:]); case "compare": evalCompare(args[1:]); case "check": evalCheck(args[1:]); default: fmt.Fprintf(os.Stderr,"forgeai eval: unknown subcommand %q\n",args[0]); os.Exit(1) }
}

func evalRun(args []string) {
	fs:=flag.NewFlagSet("eval run",flag.ExitOnError); datasetPath:=fs.String("dataset","","golden dataset JSON"); output:=fs.String("output","","artifact file or directory (stdout when empty)"); configPath:=fs.String("config","","config YAML"); topK:=fs.String("top-k","1,3,5,10","comma-separated K values"); rerank:=fs.Bool("rerank",false,"enable configured reranker"); resultsPath:=fs.String("results","","deterministic query-keyed retrieval results JSON fixture"); fs.Parse(args)
	if *datasetPath=="" { fmt.Fprintln(os.Stderr,"forgeai eval run: --dataset is required"); os.Exit(1) }
	var dataset evaluation.Dataset; if err:=readJSON(*datasetPath,&dataset);err!=nil{fatalf("reading dataset: %v",err)}
	ks,err:=parseTopK(*topK);if err!=nil{fatalf("top-k: %v",err)}
	var runner *usecase.EvaluateUseCase; var closeApp func()
	if *resultsPath!="" { var fixed fixedResults;if err:=readJSON(*resultsPath,&fixed);err!=nil{fatalf("reading fixed results: %v",err)};runner=&usecase.EvaluateUseCase{Searcher:fixedEvaluationSearcher{fixed},Version:app.Version,EmbeddingModel:"fixture"}
	} else { a,err:=app.Bootstrap(*configPath);if err!=nil{fatalf("%v",err)};closeApp=func(){_ = a.Close()};defer closeApp();runner=a.Evaluation() }
	run,err:=runner.Run(context.Background(),dataset,evaluation.Config{TopK:ks,Rerank:*rerank,Retrieval:"hybrid_rrf"});if err!=nil{fatalf("evaluation: %v",err)}
	if err:=writeJSON(*output,run.RunID,run);err!=nil{fatalf("writing artifact: %v",err)}
}

func evalCompare(args []string){fs:=flag.NewFlagSet("eval compare",flag.ExitOnError);b:=fs.String("baseline","","baseline artifact");c:=fs.String("candidate","","candidate artifact");out:=fs.String("output","","comparison file (stdout when empty)");fs.Parse(args);if *b==""||*c==""{fatalf("eval compare: --baseline and --candidate are required")};var br,cr evaluation.Run;if err:=readJSON(*b,&br);err!=nil{fatalf("baseline: %v",err)};if err:=readJSON(*c,&cr);err!=nil{fatalf("candidate: %v",err)};cmp:=evaluation.Compare(br,cr);if err:=writeJSON(*out,"comparison",cmp);err!=nil{fatalf("writing comparison: %v",err)}}

func evalCheck(args []string){fs:=flag.NewFlagSet("eval check",flag.ExitOnError);b:=fs.String("baseline","","baseline artifact");c:=fs.String("candidate","","candidate artifact");minMRR:=fs.Float64("min-mrr-delta",-1,"minimum aggregate MRR delta; -1 disables");maxRatio:=fs.Float64("max-regressed-ratio",1,"maximum fraction of comparable queries that regress");fs.Parse(args);if *b==""||*c==""{fatalf("eval check: --baseline and --candidate are required")};var br,cr evaluation.Run;if err:=readJSON(*b,&br);err!=nil{fatalf("baseline: %v",err)};if err:=readJSON(*c,&cr);err!=nil{fatalf("candidate: %v",err)};cmp:=evaluation.Compare(br,cr);failed:=false;for _,m:=range cmp.Metrics{if m.Metric=="reciprocal_rank"&&m.Delta!=nil&&*minMRR>-1&&*m.Delta<*minMRR{fmt.Fprintf(os.Stderr,"FAIL reciprocal_rank delta %.6f < %.6f\n",*m.Delta,*minMRR);failed=true}};total:=cmp.Improved+cmp.Regressed+cmp.Unchanged;if total>0&&float64(cmp.Regressed)/float64(total)>*maxRatio{fmt.Fprintf(os.Stderr,"FAIL regressed query ratio %.6f > %.6f\n",float64(cmp.Regressed)/float64(total),*maxRatio);failed=true};if failed{os.Exit(2)};fmt.Println("PASS evaluation regression gates")}

func parseTopK(s string)([]int,error){var out []int;for _,part:=range strings.Split(s,","){k,err:=strconv.Atoi(strings.TrimSpace(part));if err!=nil||k<=0{return nil,fmt.Errorf("invalid K %q",part)};out=append(out,k)};return out,nil}
func readJSON(path string,v any)error{b,err:=os.ReadFile(path);if err!=nil{return err};return json.Unmarshal(b,v)}
func writeJSON(path,id string,v any)error{b,err:=json.MarshalIndent(v,"","  ");if err!=nil{return err};b=append(b,'\n');if path==""{_,err=os.Stdout.Write(b);return err};if info,statErr:=os.Stat(path);(statErr==nil&&info.IsDir())||strings.HasSuffix(path,string(os.PathSeparator)){if err:=os.MkdirAll(path,0o755);err!=nil{return err};path=filepath.Join(path,id+".json")};if err:=os.WriteFile(path,b,0o644);err!=nil{return err};fmt.Fprintln(os.Stderr,path);return nil}
func fatalf(format string,args ...any){fmt.Fprintf(os.Stderr,"forgeai: "+format+"\n",args...);os.Exit(1)}

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
