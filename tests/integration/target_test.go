package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func base() map[string]string {
	return map[string]string{AckEnv: "yes", "CL_TARGET": "local", "CL_URL": "http://127.0.0.1:9/", "CL_INGEST_KEY": "i", "CL_VIEW_KEY": "v"}
}

func TestFromEnv_Guards(t *testing.T) {
	stack := filepath.Join(t.TempDir(), "stack.json")
	if err := os.WriteFile(stack, []byte(`{"urls":["https://it.example.test"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string // substring of the error, "" = accepted
	}{
		{"ok local", func(map[string]string) {}, ""},
		{"no ack", func(m map[string]string) { delete(m, AckEnv) }, AckEnv},
		{"bad kind", func(m map[string]string) { m["CL_TARGET"] = "prod" }, "CL_TARGET"},
		{"no keys", func(m map[string]string) { m["CL_VIEW_KEY"] = "" }, "required"},
		{"local non-loopback", func(m map[string]string) { m["CL_URL"] = "http://203.0.113.5" }, "loopback"},
		{"relative url", func(m map[string]string) { m["CL_URL"] = "/v1" }, "absolute"},
		{"aws not in stack", func(m map[string]string) {
			m["CL_TARGET"], m["CL_URL"], m["CL_IT_STACK"] = "aws", "https://prod.example.test", stack
		}, "refusing"},
		{"aws http", func(m map[string]string) {
			m["CL_TARGET"], m["CL_URL"], m["CL_IT_STACK"] = "aws", "http://it.example.test", stack
		}, "https"},
		{"aws listed", func(m map[string]string) {
			m["CL_TARGET"], m["CL_URL"], m["CL_IT_STACK"] = "aws", "https://it.example.test/", stack
		}, ""},
		{"aws no stack file", func(m map[string]string) {
			m["CL_TARGET"], m["CL_URL"], m["CL_IT_STACK"] = "aws", "https://it.example.test", filepath.Join(t.TempDir(), "none")
		}, "bootstrap_aws.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mutate(m)
			_, err := FromEnv(env(m))
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
