package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"crimsonledger/database"
	"crimsonledger/internal/config"
)

// Exit codes (PLAN.md T03).
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = "usage: crimson-db [--db PATH] migrate | status | backup [--keep N] | prune [--days N] | check | vacuum"

// run executes one command and returns the process exit code.
func run(args []string, out io.Writer) int {
	global := flag.NewFlagSet("crimson-db", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dbFlag := global.String("db", "", "database path (default: db_path of api.json)")
	if err := global.Parse(args); err != nil || global.NArg() == 0 {
		fmt.Fprintln(out, usage)
		return exitUsage
	}
	cmd, rest := global.Arg(0), global.Args()[1:]
	if !knownCommand(cmd) {
		fmt.Fprintf(out, "unknown command %q\n%s\n", cmd, usage)
		return exitUsage
	}
	if err := execute(cmd, rest, *dbFlag, out); err != nil {
		fmt.Fprintf(out, "crimson-db %s: %v\n", cmd, err)
		if _, isUsage := err.(usageError); isUsage {
			return exitUsage
		}
		return exitError
	}
	return exitOK
}

type usageError struct{ error }

func knownCommand(cmd string) bool {
	switch cmd {
	case "migrate", "status", "backup", "prune", "check", "vacuum":
		return true
	}
	return false
}

func execute(cmd string, rest []string, dbFlag string, out io.Writer) error {
	cfg, err := config.Load("api.json", config.APIDefaults)
	if err != nil {
		return err
	}
	path := dbFlag
	if path == "" {
		if path, err = cfg.ExpandedDBPath(); err != nil {
			return err
		}
	}
	if _, err := config.EnsureHome(); err != nil {
		return err
	}
	db, err := database.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	return dispatch(context.Background(), cmd, rest, command{db: db, path: path, retentionDays: cfg.RetentionDays, out: out})
}

type command struct {
	db            *sql.DB
	path          string
	retentionDays int
	out           io.Writer
}

func dispatch(ctx context.Context, name string, rest []string, c command) error {
	switch name {
	case "migrate":
		if err := database.Migrate(ctx, c.db); err != nil {
			return err
		}
		return c.status(ctx)
	case "status":
		return c.status(ctx)
	case "backup":
		return c.backup(ctx, rest)
	case "prune":
		return c.prune(ctx, rest)
	case "check":
		if err := database.Integrity(ctx, c.db); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "ok")
		return nil
	default: // vacuum
		return database.Vacuum(ctx, c.db)
	}
}

func (c command) status(ctx context.Context) error {
	v, err := database.Version(ctx, c.db)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s: schema version %d\n", c.path, v)
	return nil
}

func (c command) backup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keep := fs.Int("keep", database.DefaultKeepBackups, "backups to keep")
	if err := fs.Parse(args); err != nil {
		return usageError{err}
	}
	home, err := config.Home()
	if err != nil {
		return err
	}
	file, err := database.Backup(ctx, c.db, filepath.Join(home, "data", "backups"), *keep)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "backup written:", file)
	return nil
}

func (c command) prune(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	days := fs.Int("days", c.retentionDays, "retention in days")
	if err := fs.Parse(args); err != nil || *days < 1 {
		return usageError{fmt.Errorf("--days must be a positive number")}
	}
	cutoff := time.Now().Add(-time.Duration(*days) * 24 * time.Hour).UnixMilli()
	res, err := database.Prune(ctx, c.db, cutoff)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "pruned %d fixes, %d batch ids\n", res.Fixes, res.Batches)
	return nil
}
