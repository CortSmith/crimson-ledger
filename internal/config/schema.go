package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Limits from docs/tasks/T01-config-home.md. Each is enforced by exactly one validate helper below.
const (
	configVersion = 1

	minUploadIntervalMS = 500
	maxUploadIntervalMS = 60000
	minBatchContacts    = 1
	maxBatchContacts    = 200 // matches the ingest endpoint's per-request cap (PLAN.md section 5)
	minPollIntervalS    = 5
	maxPollIntervalS    = 300
	minRetentionDays    = 1
	maxRetentionDays    = 365
)

var (
	apiKeyPattern     = regexp.MustCompile(`^clk_[iv]_[a-z0-9]{8}_[A-Za-z0-9_-]{43}$`)
	sourceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	rulesets          = []string{"auto", "normal", "pvp", "rp", "rppvp"}
	stores            = []string{"sqlite", "dynamodb"}
	loopbackHosts     = []string{"127.0.0.1", "::1", "localhost"}
)

// APIConfig is api.json (the API server in local mode).
type APIConfig struct {
	Version           int    `json:"version"`
	Store             string `json:"store"`
	Listen            string `json:"listen"`
	DBPath            string `json:"db_path"`
	RetentionDays     int    `json:"retention_days"`
	MaxBodyBytes      int    `json:"max_body_bytes"`
	TLSCert           string `json:"tls_cert"`
	TLSKey            string `json:"tls_key"`
	RateLimitPerMin   int    `json:"rate_limit_per_min"`
	TrustProxyHeaders bool   `json:"trust_proxy_headers"`
}

// LoggerConfig is logger.json (the scout).
type LoggerConfig struct {
	Version          int    `json:"version"`
	ServerURL        string `json:"server_url"`
	APIKey           string `json:"api_key"`
	SourceName       string `json:"source_name"`
	LogsDir          string `json:"logs_dir"`
	CAFile           string `json:"ca_file"`
	Ruleset          string `json:"ruleset"`
	UploadIntervalMS int    `json:"upload_interval_ms"`
	BatchMaxContacts int    `json:"batch_max_contacts"`
	TailFromStart    bool   `json:"tail_from_start"`
	AutoStartUpload  bool   `json:"auto_start_upload"`
}

// ViewerConfig is viewer.json (the crimson-ledger viewer).
type ViewerConfig struct {
	Version       int    `json:"version"`
	ServerURL     string `json:"server_url"`
	APIKey        string `json:"api_key"`
	CAFile        string `json:"ca_file"`
	Ruleset       string `json:"ruleset"`
	PollIntervalS int    `json:"poll_interval_s"`
	Orientation   string `json:"orientation"`
	SelectedMap   int    `json:"selected_map"`
	ShowHistory   bool   `json:"show_history"`
	// TimeZone is "local" (or empty), "UTC" or an IANA name; it only changes how times are shown.
	TimeZone   string     `json:"time_zone,omitempty"`
	Appearance Appearance `json:"appearance"`
}

// exampleServerURL is the placeholder written on first run; the user replaces it.
const exampleServerURL = "https://example.lambda-url.us-east-1.on.aws"

// APIDefaults returns the api.json written on first run (PLAN.md section 4).
func APIDefaults() APIConfig {
	return APIConfig{
		Version: configVersion, Store: "sqlite", Listen: "127.0.0.1:8443",
		DBPath: "~/.crimson-ledger/data/ledger.db", RetentionDays: 30,
		MaxBodyBytes: 262144, RateLimitPerMin: 120,
	}
}

// LoggerDefaults returns the logger.json written on first run. The api_key is empty until the user pastes one.
func LoggerDefaults() LoggerConfig {
	return LoggerConfig{
		Version: configVersion, ServerURL: exampleServerURL, SourceName: "my-pc", Ruleset: "auto",
		UploadIntervalMS: 2000, BatchMaxContacts: 200, AutoStartUpload: true,
	}
}

// ViewerDefaults returns the viewer.json written on first run. The api_key is empty until the user pastes one.
func ViewerDefaults() ViewerConfig {
	return ViewerConfig{
		Version: configVersion, ServerURL: exampleServerURL, Ruleset: "auto",
		PollIntervalS: 15, Orientation: "std",
	}
}

// Validate checks every api.json rule; the error names the file and the field.
func (c APIConfig) Validate() error {
	const file = "api.json"
	if err := firstError(
		validateVersion(file, c.Version),
		validateOneOf(file, "store", c.Store, stores),
		validateListen(file, c.Listen),
		validateRange(file, "retention_days", c.RetentionDays, minRetentionDays, maxRetentionDays),
		validateRange(file, "max_body_bytes", c.MaxBodyBytes, 1, 1<<30),
		validateRange(file, "rate_limit_per_min", c.RateLimitPerMin, 1, 1<<20),
		validateTLSPair(file, c.TLSCert, c.TLSKey),
	); err != nil {
		return err
	}
	if c.Store == "sqlite" && c.DBPath == "" {
		return fmt.Errorf("%s: db_path is required when store is sqlite", file)
	}
	return nil
}

// ExpandedDBPath returns DBPath with a leading "~" replaced by the user's home directory.
func (c APIConfig) ExpandedDBPath() (string, error) {
	if c.DBPath != "~" && !strings.HasPrefix(c.DBPath, "~/") {
		return c.DBPath, nil
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand db_path: %w", err)
	}
	return filepath.Join(user, c.DBPath[1:]), nil
}

// Validate checks every logger.json rule; the error names the file and the field.
func (c LoggerConfig) Validate() error {
	const file = "logger.json"
	return firstError(
		validateVersion(file, c.Version),
		validateServerURL(file, c.ServerURL),
		validateAPIKey(file, c.APIKey),
		validateSourceName(file, c.SourceName),
		validateCAFile(file, c.CAFile),
		validateOneOf(file, "ruleset", c.Ruleset, rulesets),
		validateRange(file, "upload_interval_ms", c.UploadIntervalMS, minUploadIntervalMS, maxUploadIntervalMS),
		validateRange(file, "batch_max_contacts", c.BatchMaxContacts, minBatchContacts, maxBatchContacts),
	)
}

// Validate checks every viewer.json rule; the error names the file and the field.
func (c ViewerConfig) Validate() error {
	const file = "viewer.json"
	return firstError(
		validateVersion(file, c.Version),
		validateServerURL(file, c.ServerURL),
		validateAPIKey(file, c.APIKey),
		validateCAFile(file, c.CAFile),
		validateOneOf(file, "ruleset", c.Ruleset, rulesets),
		validateRange(file, "poll_interval_s", c.PollIntervalS, minPollIntervalS, maxPollIntervalS),
		validateTimeZone(file, c.TimeZone),
		c.Appearance.Validate(),
	)
}

func validateTimeZone(file, z string) error {
	if !ValidTimeZone(z) {
		return fmt.Errorf("%s: time_zone must be local, UTC or an IANA zone name", file)
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func validateVersion(file string, v int) error {
	if v != configVersion {
		return fmt.Errorf("%s: version must be %d (got %d)", file, configVersion, v)
	}
	return nil
}

func validateRange(file, field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s: %s must be between %d and %d (got %d)", file, field, lo, hi, v)
	}
	return nil
}

func validateOneOf(file, field, v string, allowed []string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s: %s must be one of %s", file, field, strings.Join(allowed, ", "))
}

// validateServerURL requires https://, or http:// for loopback hosts only, and refuses credentials,
// queries and fragments. The URL itself is never echoed: it could carry a password.
func validateServerURL(file, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || strings.ContainsAny(raw, " \t\r\n?#") {
		return fmt.Errorf("%s: server_url must be a valid URL such as https://host (no query or fragment)", file)
	}
	if u.User != nil {
		return fmt.Errorf("%s: server_url must not contain a user name or password", file)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && slices.Contains(loopbackHosts, u.Hostname())) {
		return nil
	}
	return fmt.Errorf("%s: server_url must start with https:// (http:// is only allowed for localhost)", file)
}

// validateAPIKey accepts an empty key (first run) or a well-formed one. The key is never echoed.
func validateAPIKey(file, key string) error {
	if key != "" && !apiKeyPattern.MatchString(key) {
		return fmt.Errorf("%s: api_key is not a valid key (expected the clk_ key you were given), or leave it empty", file)
	}
	return nil
}

func validateSourceName(file, name string) error {
	if !sourceNamePattern.MatchString(name) {
		return fmt.Errorf("%s: source_name must be 1-32 characters of letters, digits, _ . -", file)
	}
	return nil
}

// validateCAFile accepts an empty path or an existing, readable, regular file.
func validateCAFile(file, path string) error {
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%s: ca_file %q must be an existing regular file", file, path)
	}
	f, err := os.Open(path) // #nosec G304 -- user-configured trust anchor; checked to be a regular file above
	if err != nil {
		return fmt.Errorf("%s: ca_file %q is not readable", file, path)
	}
	return f.Close()
}

func validateListen(file, addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("%s: listen must look like host:port, for example 127.0.0.1:8443", file)
	}
	return nil
}

func validateTLSPair(file, cert, key string) error {
	if (cert == "") != (key == "") {
		return fmt.Errorf("%s: tls_cert and tls_key must be set together", file)
	}
	return nil
}

// ValidateServerURL applies the server_url rules (https only, loopback may use http, no credentials) for
// callers outside this package such as the API client. The URL is never echoed in the error.
func ValidateServerURL(raw string) error { return validateServerURL("server_url", raw) }
