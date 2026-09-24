package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/sibukixxx/rag-poc/internal/app"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

const auditUsage = `  forgeai audit list [-config path] [-limit N]
                                   Print the security audit trail (newest first) as JSON.`

// cliContext attributes CLI actions in the audit trail to "cli:<OS user>".
// The OS user name is a label, not an authenticated identity: anyone who
// can run the binary against the database can act (docs/security/AUDIT_TRAIL.md).
func cliContext() context.Context {
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	return audit.WithActor(context.Background(), "cli:"+name)
}

func recordCLIAudit(ctx context.Context, a *app.App, action string, outcome audit.Outcome, target string) {
	_ = a.Audit().Record(ctx, audit.Event{
		OccurredAt: time.Now(), Action: action, Outcome: outcome, Actor: audit.ActorFrom(ctx), Target: target,
	})
}

func cmdAudit(args []string) {
	if len(args) < 1 || args[0] != "list" {
		fmt.Fprintln(os.Stderr, "forgeai audit: expected the subcommand list")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("audit list", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config YAML")
	limit := fs.Int("limit", 100, "maximum number of events (1-1000)")
	fs.Parse(args[1:])

	a, err := app.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()
	events, err := a.Audit().ListEvents(context.Background(), *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgeai audit list: %v\n", err)
		os.Exit(1)
	}
	if events == nil {
		events = []audit.Event{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(events)
}
