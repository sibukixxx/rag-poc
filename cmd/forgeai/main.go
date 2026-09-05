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
	"strings"

	"golang.org/x/term"

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
	case "secret":
		cmdSecret(os.Args[2:])
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
