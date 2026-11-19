package settings

import (
	"strconv"

	"crimsonledger/internal/config"
)

const (
	viewerFile = "viewer.json"
	loggerFile = "logger.json"
)

// ViewerBackend stores settings in viewer.json.
type ViewerBackend struct{}

// Load reads viewer.json.
func (ViewerBackend) Load() (Form, string, error) {
	c, err := config.Load(viewerFile, config.ViewerDefaults)
	if err != nil {
		return Form{}, "", err
	}
	return Form{ServerURL: c.ServerURL, CAFile: c.CAFile, Ruleset: c.Ruleset,
		Extra: map[string]string{"poll_interval_s": strconv.Itoa(c.PollIntervalS)}}, c.APIKey, nil
}

// Save validates and writes viewer.json, keeping fields the form does not edit.
func (ViewerBackend) Save(f Form, key string) error {
	c, err := config.Load(viewerFile, config.ViewerDefaults)
	if err != nil {
		return err
	}
	c.ServerURL, c.CAFile, c.Ruleset, c.APIKey = f.ServerURL, f.CAFile, f.Ruleset, key
	c.PollIntervalS = atoi(f.Extra, "poll_interval_s", c.PollIntervalS)
	return config.Save(viewerFile, c)
}

// File is the display path.
func (ViewerBackend) File() string { return "~/.crimson-ledger/" + viewerFile }

// UserAgent names the viewer.
func (ViewerBackend) UserAgent() string { return "crimson-ledger/1.0" }

// LoggerBackend stores settings in logger.json.
type LoggerBackend struct{}

// Load reads logger.json.
func (LoggerBackend) Load() (Form, string, error) {
	c, err := config.Load(loggerFile, config.LoggerDefaults)
	if err != nil {
		return Form{}, "", err
	}
	return Form{ServerURL: c.ServerURL, CAFile: c.CAFile, Ruleset: c.Ruleset, Extra: map[string]string{
		"source_name":        c.SourceName,
		"logs_dir":           c.LogsDir,
		"upload_interval_ms": strconv.Itoa(c.UploadIntervalMS),
	}}, c.APIKey, nil
}

// Save validates and writes logger.json, keeping fields the form does not edit.
func (LoggerBackend) Save(f Form, key string) error {
	c, err := config.Load(loggerFile, config.LoggerDefaults)
	if err != nil {
		return err
	}
	c.ServerURL, c.CAFile, c.Ruleset, c.APIKey = f.ServerURL, f.CAFile, f.Ruleset, key
	c.SourceName, c.LogsDir = f.Extra["source_name"], f.Extra["logs_dir"]
	c.UploadIntervalMS = atoi(f.Extra, "upload_interval_ms", c.UploadIntervalMS)
	return config.Save(loggerFile, c)
}

// File is the display path.
func (LoggerBackend) File() string { return "~/.crimson-ledger/" + loggerFile }

// UserAgent names the scout.
func (LoggerBackend) UserAgent() string { return "crimson-scout/1.0" }
