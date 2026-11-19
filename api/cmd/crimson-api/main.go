// Command crimson-api runs the Crimson Ledger API on your own machine and manages its API keys.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes (docs/tasks/T08).
const (
	exitOK      = 0
	exitRuntime = 1
	exitUsage   = 2
)

const usageText = `usage: crimson-api <command> [flags]

commands:
  serve [--listen ADDR] [--insecure-lan]        run the API (default 127.0.0.1:8443)
  keys create --scope ingest|view --label NAME [--expires 90d]
  keys list
  keys revoke <id>
  keys rotate <id>
  tls-selfsign --host HOST [--host HOST ...]    write a self-signed certificate for a LAN
  healthcheck [--url URL]                       exit 0 when the running API answers /v1/health (container probe)
  version`

// IO is everything a command touches outside its own arguments, injected so tests need no globals.
type IO struct {
	Stdout io.Writer        // machine-readable output only (a freshly created key)
	Stderr io.Writer        // banners, logs, guidance
	Now    func() time.Time // nil means time.Now
	Rand   io.Reader        // nil means crypto/rand

	// Test seams, never set by main.
	Ready       func(addr string) // called once the server is listening
	NoKeyCache  bool              // revocation takes effect immediately
	PruneEvery  time.Duration     // zero means hourly
	DrainBudget time.Duration     // zero means 10 s
}

func (i IO) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func (i IO) random() io.Reader {
	if i.Rand != nil {
		return i.Rand
	}
	return rand.Reader
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], IO{Stdout: os.Stdout, Stderr: os.Stderr})
	stop()
	os.Exit(code)
}

// run dispatches one command and returns the process exit code.
func run(ctx context.Context, args []string, io IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(io.Stderr, usageText)
		return exitUsage
	}
	rest := args[1:]
	switch args[0] {
	case "serve":
		return runServe(ctx, rest, io)
	case "keys":
		return runKeys(ctx, rest, io)
	case "tls-selfsign":
		return runTLSSelfsign(rest, io)
	case "healthcheck":
		return runHealthcheck(ctx, rest, io)
	case "version":
		_, _ = fmt.Fprintln(io.Stdout, "crimson-api", version)
		return exitOK
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(io.Stdout, usageText)
		return exitOK
	}
	_, _ = fmt.Fprintf(io.Stderr, "crimson-api: unknown command %q\n%s\n", args[0], usageText)
	return exitUsage
}
