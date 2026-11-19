package main

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createKey(t *testing.T, scope, label string, extra ...string) string {
	t.Helper()
	args := append([]string{"keys", "create", "--scope", scope, "--label", label}, extra...)
	code, out, stderr := runCmd(t, args...)
	if code != exitOK {
		t.Fatalf("keys create exit %d: %s", code, stderr)
	}
	return strings.TrimSpace(out)
}

func keyID(token string) string { return strings.Split(token, "_")[2] }

func TestKeys_CreatePrintsOnce(t *testing.T) {
	newHome(t, nil)
	code, out, stderr := runCmd(t, "keys", "create", "--scope", "ingest", "--label", "my-pc")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	token := strings.TrimSpace(out)
	if !strings.HasPrefix(token, "clk_i_") || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout must hold only the token, got %q", out)
	}
	if strings.Contains(stderr, token) {
		t.Fatal("token leaked to stderr")
	}
	_, listOut, _ := runCmd(t, "keys", "list")
	secret := token[strings.LastIndex(token, "_")+1:]
	if strings.Contains(listOut, secret) || !strings.Contains(listOut, keyID(token)) {
		t.Fatalf("list must show the id but never the secret:\n%s", listOut)
	}
}

func TestKeys_DatabaseHoldsNoSecret(t *testing.T) {
	home := newHome(t, nil)
	token := createKey(t, "view", "viewer")
	secret := token[strings.LastIndex(token, "_")+1:]
	db, err := sql.Open("sqlite", filepath.Join(home, "data", "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM api_keys WHERE hash = ? OR label LIKE ? OR id LIKE ?`,
		secret, "%"+secret+"%", "%"+secret+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("secret found in database")
	}
}

func TestKeys_CreateRejectsBadInput(t *testing.T) {
	newHome(t, nil)
	tests := []struct {
		name string
		args []string
	}{
		{"no scope", []string{"--label", "x"}},
		{"bad scope", []string{"--scope", "admin", "--label", "x"}},
		{"no label", []string{"--scope", "view"}},
		{"label too long", []string{"--scope", "view", "--label", strings.Repeat("a", 65)}},
		{"label control char", []string{"--scope", "view", "--label", "a\x00b"}},
		{"bad expiry", []string{"--scope", "view", "--label", "x", "--expires", "soon"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, _ := runCmd(t, append([]string{"keys", "create"}, tt.args...)...)
			if code != exitUsage || out != "" {
				t.Fatalf("code %d stdout %q", code, out)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"90d", 90 * 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"0d", 0, true},
		{"-5d", 0, true},
		{"-1h", 0, true},
		{"abc", 0, true},
		{"d", 0, true},
		{"99999d", 0, true},
		{"100000h", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseExpiry(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestKeys_ExpiresShownInList(t *testing.T) {
	newHome(t, nil)
	createKey(t, "view", "short", "--expires", "12h")
	_, out, _ := runCmd(t, "keys", "list")
	line := lineWith(out, "short")
	if strings.Contains(line, "  -  ") && strings.Count(line, " - ") >= 2 {
		t.Fatalf("expires column empty: %q", line)
	}
}

func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func TestKeys_RevokeBlocksAuth(t *testing.T) {
	newHome(t, nil)
	view := createKey(t, "view", "viewer")
	base, _, _ := startServer(t)
	get := func() int {
		status, _, err := get(t, http.DefaultClient, base+"/v1/charts", view)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	if got := get(); got != http.StatusOK {
		t.Fatalf("before revoke: %d", got)
	}
	if code, _, stderr := runCmd(t, "keys", "revoke", keyID(view)); code != exitOK {
		t.Fatalf("revoke: %s", stderr)
	}
	if got := get(); got != http.StatusForbidden {
		t.Fatalf("after revoke: %d, want 403", got)
	}
}

func TestKeys_RevokeUnknownIDFails(t *testing.T) {
	newHome(t, nil)
	if code, _, stderr := runCmd(t, "keys", "revoke", "nosuchid"); code != exitRuntime || !strings.Contains(stderr, "no key with id") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if code, _, _ := runCmd(t, "keys", "revoke"); code != exitUsage {
		t.Fatalf("missing id gave %d", code)
	}
}

func TestKeys_RotateKeepsScopeAndLabel(t *testing.T) {
	newHome(t, nil)
	old := createKey(t, "ingest", "laptop", "--expires", "30d")
	code, out, stderr := runCmd(t, "keys", "rotate", keyID(old))
	if code != exitOK {
		t.Fatalf("rotate: %s", stderr)
	}
	fresh := strings.TrimSpace(out)
	if !strings.HasPrefix(fresh, "clk_i_") || fresh == old {
		t.Fatalf("replacement %q", fresh)
	}
	_, list, _ := runCmd(t, "keys", "list")
	oldLine, newLine := lineWith(list, keyID(old)), lineWith(list, keyID(fresh))
	if !strings.Contains(oldLine, "true") || !strings.Contains(newLine, "false") || !strings.Contains(newLine, "laptop") {
		t.Fatalf("old must be disabled, new active with the same label:\n%s", list)
	}
	if strings.Contains(newLine, "  -  ") && strings.Count(newLine, " - ") > 1 {
		t.Fatalf("replacement lost its expiry: %q", newLine)
	}
}
