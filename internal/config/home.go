// Package config finds, loads, validates and atomically saves the JSON config files under
// ~/.crimson-ledger (override with CRIMSON_LEDGER_HOME). Every app uses it the same way:
//
//	cfg, err := config.Load("logger.json", config.LoggerDefaults)
//	if err != nil { return err }          // unknown fields, bad values and loose permissions are errors
//	cfg.SourceName = "my-pc"
//	err = config.Save("logger.json", cfg) // validated, written atomically with mode 0600
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// HomeEnv overrides the config directory (tests and Docker use it).
	HomeEnv = "CRIMSON_LEDGER_HOME"
	// homeDirName is the config directory under the user's home (PLAN.md section 4).
	homeDirName = ".crimson-ledger"
	// dataDirName holds the SQLite file in local mode.
	dataDirName = "data"
	// privateDirMode is the mode of the home and data directories.
	privateDirMode = 0o700
)

// Home returns the config directory: $CRIMSON_LEDGER_HOME, else <user home>/.crimson-ledger.
// It reads the environment on every call.
func Home() (string, error) {
	if dir := os.Getenv(HomeEnv); dir != "" {
		return dir, nil
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home directory: %w", err)
	}
	return filepath.Join(user, homeDirName), nil
}

// EnsureHome creates Home() and its data/ directory with mode 0700 and returns Home().
func EnsureHome() (string, error) {
	dir, err := Home()
	if err != nil {
		return "", err
	}
	for _, d := range []string{dir, filepath.Join(dir, dataDirName)} {
		if err := os.MkdirAll(d, privateDirMode); err != nil {
			return "", fmt.Errorf("create %s: %w", d, err)
		}
		if err := makePrivate(d); err != nil {
			return "", fmt.Errorf("restrict %s to its owner: %w", d, err)
		}
	}
	return dir, nil
}
