package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validKey = "clk_i_k3f9a2x1_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde"

// Verbatim from docs/PLAN.md section 4 (api_key placeholder replaced by empty: first run).
const planAPIJSON = `{ "version": 1, "store": "sqlite", "listen": "127.0.0.1:8443",
  "db_path": "~/.crimson-ledger/data/ledger.db", "retention_days": 30,
  "max_body_bytes": 262144, "tls_cert": "", "tls_key": "",
  "rate_limit_per_min": 120, "trust_proxy_headers": false }`
const planLoggerJSON = `{ "version": 1, "server_url": "https://example.lambda-url.us-east-1.on.aws",
  "api_key": "", "source_name": "my-pc", "logs_dir": "",
  "ca_file": "", "ruleset": "auto", "upload_interval_ms": 2000, "batch_max_contacts": 200,
  "tail_from_start": false, "auto_start_upload": true }`
const planViewerJSON = `{ "version": 1, "server_url": "https://example.lambda-url.us-east-1.on.aws",
  "api_key": "", "ca_file": "", "ruleset": "auto", "poll_interval_s": 15,
  "orientation": "std", "selected_map": 0, "show_history": false }`

func TestDefaults_MatchPlan(t *testing.T) {
	check := func(name, plan string, defaults any, strict any) {
		t.Helper()
		dec := json.NewDecoder(strings.NewReader(plan))
		dec.DisallowUnknownFields()
		if err := dec.Decode(strict); err != nil {
			t.Fatalf("%s: plan JSON does not fit struct: %v", name, err)
		}
		a, _ := json.Marshal(defaults)
		b, _ := json.Marshal(strict)
		if string(a) != string(b) {
			t.Errorf("%s defaults differ from PLAN.md section 4:\n got %s\nwant %s", name, a, b)
		}
	}
	check("api", planAPIJSON, APIDefaults(), new(APIConfig))
	check("logger", planLoggerJSON, LoggerDefaults(), new(LoggerConfig))
	check("viewer", planViewerJSON, ViewerDefaults(), new(ViewerConfig))
}

func TestDefaults_AreValid(t *testing.T) {
	for name, v := range map[string]Validator{"api": APIDefaults(), "logger": LoggerDefaults(), "viewer": ViewerDefaults()} {
		if err := v.Validate(); err != nil {
			t.Errorf("%s defaults invalid: %v", name, err)
		}
	}
}

func TestValidate_ServerURL(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://x.y", true},
		{"https://x.y/prefix", true},
		{"http://127.0.0.1:8443", true},
		{"http://localhost", true},
		{"http://[::1]:8443", true},
		{"http://192.168.1.5", false},
		{"http://example.com", false},
		{"ftp://x", false},
		{"https://", false},
		{"", false},
		{"https://user:pw@host", false},
		{"https://user@host", false},
		{"https://host?x=1", false},
		{"https://host#frag", false},
		{"://bad", false},
		{"https://ho st", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			l := LoggerDefaults()
			l.ServerURL = tc.url
			err := l.Validate()
			if (err == nil) != tc.ok {
				t.Errorf("ok=%v, err=%v", tc.ok, err)
			}
			if err != nil && !strings.Contains(err.Error(), "logger.json: server_url") {
				t.Errorf("message not for humans: %v", err)
			}
			v := ViewerDefaults()
			v.ServerURL = tc.url
			if (v.Validate() == nil) != tc.ok {
				t.Errorf("viewer disagrees for %q", tc.url)
			}
		})
	}
}

func TestValidate_ServerURL_NeverEchoesCredentials(t *testing.T) {
	l := LoggerDefaults()
	l.ServerURL = "https://user:hunter2@host"
	if err := l.Validate(); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidate_APIKey(t *testing.T) {
	tests := []struct {
		name, key string
		ok        bool
	}{
		{"empty first run", "", true},
		{"ingest", validKey, true},
		{"viewer", "clk_v_k3f9a2x1_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde", true},
		{"placeholder", "clk_i_...", false},
		{"bad scope", "clk_x_k3f9a2x1_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde", false},
		{"uppercase id", "clk_i_K3F9A2X1_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde", false},
		{"short secret", "clk_i_k3f9a2x1_abc", false},
		{"long secret", validKey + "x", false},
		{"trailing newline", validKey + "\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := LoggerDefaults()
			l.APIKey = tc.key
			err := l.Validate()
			if (err == nil) != tc.ok {
				t.Errorf("logger ok=%v err=%v", tc.ok, err)
			}
			if err != nil && strings.Contains(err.Error(), tc.key) && tc.key != "" {
				t.Errorf("error echoes the key: %v", err)
			}
			v := ViewerDefaults()
			v.APIKey = tc.key
			if (v.Validate() == nil) != tc.ok {
				t.Errorf("viewer disagrees")
			}
		})
	}
}

func TestValidate_LoggerFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LoggerConfig)
		ok     bool
	}{
		{"source ok", func(c *LoggerConfig) { c.SourceName = "a.B_c-1" }, true},
		{"source 32", func(c *LoggerConfig) { c.SourceName = strings.Repeat("a", 32) }, true},
		{"source 33", func(c *LoggerConfig) { c.SourceName = strings.Repeat("a", 33) }, false},
		{"source empty", func(c *LoggerConfig) { c.SourceName = "" }, false},
		{"source space", func(c *LoggerConfig) { c.SourceName = "my pc" }, false},
		{"interval min", func(c *LoggerConfig) { c.UploadIntervalMS = 500 }, true},
		{"interval max", func(c *LoggerConfig) { c.UploadIntervalMS = 60000 }, true},
		{"interval low", func(c *LoggerConfig) { c.UploadIntervalMS = 499 }, false},
		{"interval high", func(c *LoggerConfig) { c.UploadIntervalMS = 60001 }, false},
		{"batch min", func(c *LoggerConfig) { c.BatchMaxContacts = 1 }, true},
		{"batch max", func(c *LoggerConfig) { c.BatchMaxContacts = 200 }, true},
		{"batch zero", func(c *LoggerConfig) { c.BatchMaxContacts = 0 }, false},
		{"batch high", func(c *LoggerConfig) { c.BatchMaxContacts = 201 }, false},
		{"ruleset pvp", func(c *LoggerConfig) { c.Ruleset = "pvp" }, true},
		{"ruleset rppvp", func(c *LoggerConfig) { c.Ruleset = "rppvp" }, true},
		{"ruleset bad", func(c *LoggerConfig) { c.Ruleset = "hardcore" }, false},
		{"version 2", func(c *LoggerConfig) { c.Version = 2 }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := LoggerDefaults()
			tc.mutate(&c)
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Errorf("ok=%v err=%v", tc.ok, err)
			}
		})
	}
}

func TestValidate_ViewerFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ViewerConfig)
		ok     bool
	}{
		{"poll min", func(c *ViewerConfig) { c.PollIntervalS = 5 }, true},
		{"poll max", func(c *ViewerConfig) { c.PollIntervalS = 300 }, true},
		{"poll low", func(c *ViewerConfig) { c.PollIntervalS = 4 }, false},
		{"poll high", func(c *ViewerConfig) { c.PollIntervalS = 301 }, false},
		{"ruleset bad", func(c *ViewerConfig) { c.Ruleset = "" }, false},
		{"version 0", func(c *ViewerConfig) { c.Version = 0 }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := ViewerDefaults()
			tc.mutate(&c)
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Errorf("ok=%v err=%v", tc.ok, err)
			}
		})
	}
}

func TestValidate_APIFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIConfig)
		ok     bool
	}{
		{"listen ipv6", func(c *APIConfig) { c.Listen = "[::1]:8443" }, true},
		{"listen all", func(c *APIConfig) { c.Listen = ":8443" }, true},
		{"listen no port", func(c *APIConfig) { c.Listen = "127.0.0.1" }, false},
		{"listen empty", func(c *APIConfig) { c.Listen = "" }, false},
		{"retention min", func(c *APIConfig) { c.RetentionDays = 1 }, true},
		{"retention max", func(c *APIConfig) { c.RetentionDays = 365 }, true},
		{"retention zero", func(c *APIConfig) { c.RetentionDays = 0 }, false},
		{"retention high", func(c *APIConfig) { c.RetentionDays = 366 }, false},
		{"store dynamodb", func(c *APIConfig) { c.Store = "dynamodb" }, true},
		{"store bad", func(c *APIConfig) { c.Store = "mysql" }, false},
		{"body zero", func(c *APIConfig) { c.MaxBodyBytes = 0 }, false},
		{"rate zero", func(c *APIConfig) { c.RateLimitPerMin = 0 }, false},
		{"tls cert only", func(c *APIConfig) { c.TLSCert = "c.pem" }, false},
		{"tls both", func(c *APIConfig) { c.TLSCert, c.TLSKey = "c.pem", "k.pem" }, true},
		{"db_path empty with sqlite", func(c *APIConfig) { c.DBPath = "" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := APIDefaults()
			tc.mutate(&c)
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Errorf("ok=%v err=%v", tc.ok, err)
			}
		})
	}
}

func TestExpandedDBPath(t *testing.T) {
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)
	c := APIDefaults()
	got, err := c.ExpandedDBPath()
	if want := filepath.Join(fake, ".crimson-ledger", "data", "ledger.db"); err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	c.DBPath = "/abs/ledger.db"
	if got, _ := c.ExpandedDBPath(); got != "/abs/ledger.db" {
		t.Errorf("absolute path changed: %q", got)
	}
	c.DBPath = "~other/x"
	if got, _ := c.ExpandedDBPath(); got != "~other/x" {
		t.Errorf("~user must not expand: %q", got)
	}
}

func TestValidate_CAFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path string
		ok         bool
	}{
		{"unset", "", true},
		{"regular file", good, true},
		{"missing", filepath.Join(dir, "nope.pem"), false},
		{"directory", dir, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := LoggerDefaults()
			l.CAFile = tc.path
			if (l.Validate() == nil) != tc.ok {
				t.Errorf("logger ok=%v", tc.ok)
			}
			v := ViewerDefaults()
			v.CAFile = tc.path
			if (v.Validate() == nil) != tc.ok {
				t.Errorf("viewer ok=%v", tc.ok)
			}
		})
	}
}
