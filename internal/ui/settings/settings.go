// Package settings holds the Go side of the settings form both apps show: load, save, reveal the key, test the connection.
// The API key leaves Go only through RevealKey; LoadSettings returns a flag and the fixed mask.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/secret"
	"crimsonledger/internal/ui/window"
)

// Form is the editable settings. Extra holds app-specific fields as text (the logger's source name, interval, logs dir).
type Form struct {
	ServerURL string            `json:"serverUrl"`
	CAFile    string            `json:"caFile"`
	Ruleset   string            `json:"ruleset"`
	Extra     map[string]string `json:"extra"`
}

// Backend reads and writes one app's config file. The key is handled separately so it never travels inside Form.
type Backend interface {
	Load() (f Form, apiKey string, err error)
	// Save validates and writes; an error names the file and field and never contains the key.
	Save(f Form, apiKey string) error
	// File is the display path shown under the form.
	File() string
	// UserAgent identifies the app to the server.
	UserAgent() string
}

// View is what the page receives. It never contains the key.
type View struct {
	Form
	HasKey  bool   `json:"hasKey"`
	KeyMask string `json:"keyMask"`
	File    string `json:"file"`
}

// Input is what the page sends on Save. APIKey is the placeholder (or empty) when the user did not touch the key field.
type Input struct {
	Form
	APIKey *string `json:"apiKey"`
}

// Result reports validation or connection outcomes. Field is the offending setting when known.
type Result struct {
	OK      bool   `json:"ok"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message,omitempty"`
}

// Pinger is the part of apiclient.Client used by TestConnection.
type Pinger interface {
	Check(ctx context.Context) error
}

// Bindings serves the four page-callable functions.
type Bindings struct {
	B Backend
	// NewPinger builds the connection tester; nil uses apiclient.New. Tests inject a fake.
	NewPinger func(apiclient.Options) (Pinger, error)
	Log       *slog.Logger
}

var knownFields = []string{"server_url", "api_key", "ca_file", "ruleset", "source_name", "upload_interval_ms", "batch_max_contacts",
	"poll_interval_s", "logs_dir"}

// LoadSettings returns the current settings without the key.
func (s *Bindings) LoadSettings() (View, error) {
	f, key, err := s.B.Load()
	if err != nil {
		return View{}, err
	}
	return View{Form: f, HasKey: key != "", KeyMask: secret.Mask(), File: s.B.File()}, nil
}

// SaveSettings stores the form. An absent key or the placeholder keeps the stored key; anything else replaces it.
func (s *Bindings) SaveSettings(in Input) (Result, error) {
	_, key, err := s.B.Load()
	if err != nil {
		return Result{}, err
	}
	if in.APIKey != nil && *in.APIKey != secret.Mask() {
		key = strings.TrimSpace(*in.APIKey)
	}
	if err := s.B.Save(in.Form, key); err != nil {
		return Result{Field: fieldOf(err.Error()), Message: secret.Redact(err.Error())}, nil
	}
	return Result{OK: true}, nil
}

// RevealKey is the only function that returns the key. It logs the event, never the value.
func (s *Bindings) RevealKey() (string, error) {
	_, key, err := s.B.Load()
	if err != nil {
		return "", err
	}
	if s.Log != nil {
		s.Log.Info("api key revealed")
	}
	return key, nil
}

// TestConnection checks the saved server address (GET /v1/health).
func (s *Bindings) TestConnection() (Result, error) {
	f, key, err := s.B.Load()
	if err != nil {
		return Result{}, err
	}
	opts := apiclient.Options{BaseURL: f.ServerURL, APIKey: key, CAFile: f.CAFile, UserAgent: s.B.UserAgent()}
	mk := s.NewPinger
	if mk == nil {
		mk = func(o apiclient.Options) (Pinger, error) { return apiclient.New(o) }
	}
	p, err := mk(opts)
	if err != nil {
		return Result{Field: fieldOf(err.Error()), Message: secret.Redact(err.Error())}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Check(ctx); err != nil {
		return Result{Message: secret.Redact(fmt.Sprintf("could not reach the server: %v", err))}, nil
	}
	return Result{OK: true, Message: "connected"}, nil
}

// Register binds the four functions on w under their page names.
func (s *Bindings) Register(w window.Window) error {
	for name, fn := range map[string]any{
		"loadSettings": s.LoadSettings,
		"saveSettings": s.SaveSettings,
		"revealKey":    s.RevealKey,
	} {
		if err := w.Bind(name, fn); err != nil {
			return err
		}
	}
	// The connection test waits on the network, so it must not run on the UI thread.
	return w.BindAsync("testConnection", s.TestConnection)
}

func fieldOf(msg string) string {
	for _, f := range knownFields {
		if strings.Contains(msg, f) {
			return f
		}
	}
	return ""
}

func atoi(extra map[string]string, k string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(extra[k])); err == nil {
		return v
	}
	return def
}
