package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_StatusPrintsVersion(t *testing.T) {
	t.Setenv("CRIMSON_LEDGER_HOME", t.TempDir())
	for _, args := range [][]string{{"migrate"}, {"status"}} {
		var out bytes.Buffer
		if code := run(args, &out); code != 0 {
			t.Fatalf("%v exit %d: %s", args, code, out.String())
		}
		if args[0] == "status" && !strings.Contains(out.String(), "version 1") {
			t.Errorf("status output %q lacks 'version 1'", out.String())
		}
	}
}

func TestCLI_UsageErrors(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, &out); code != 2 {
		t.Errorf("no args exit %d, want 2", code)
	}
	if code := run([]string{"nonsense"}, &out); code != 2 {
		t.Errorf("unknown command exit %d, want 2", code)
	}
}

func TestCLI_BackupPruneCheckVacuum(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CRIMSON_LEDGER_HOME", home)
	for _, args := range [][]string{{"migrate"}, {"backup", "--keep", "3"}, {"prune", "--days", "7"}, {"check"}, {"vacuum"}} {
		var out bytes.Buffer
		if code := run(args, &out); code != 0 {
			t.Fatalf("%v exit %d: %s", args, code, out.String())
		}
	}
	if m, _ := filepath.Glob(filepath.Join(home, "data", "backups", "ledger-*.db")); len(m) != 1 {
		t.Errorf("backups = %v, want 1", m)
	}
}

func TestCLI_ErrorPaths(t *testing.T) {
	t.Setenv("CRIMSON_LEDGER_HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"prune zero days", []string{"prune", "--days", "0"}, 2},
		{"prune bad flag", []string{"prune", "--nope"}, 2},
		{"backup bad flag", []string{"backup", "--nope"}, 2},
		{"bad global flag", []string{"--nope", "status"}, 2},
		{"db is a directory", []string{"--db", t.TempDir(), "status"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(tc.args, &out); code != tc.want {
				t.Errorf("exit %d, want %d: %s", code, tc.want, out.String())
			}
		})
	}
}

func TestCLI_DBFlagOverridesConfig(t *testing.T) {
	t.Setenv("CRIMSON_LEDGER_HOME", t.TempDir())
	db := filepath.Join(t.TempDir(), "other.db")
	var out bytes.Buffer
	if code := run([]string{"--db", db, "migrate"}, &out); code != 0 || !strings.Contains(out.String(), db) {
		t.Errorf("exit %d output %q", code, out.String())
	}
}
