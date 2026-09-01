// Command forgeai is the ForgeAI single-binary CLI: `serve` runs the
// server, `doctor` checks the environment, `init` bootstraps local config
// and secrets. See docs/V0.1_SPEC.md for the full command surface as it
// grows (ingest, eval run land in later weeks).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/app"
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
