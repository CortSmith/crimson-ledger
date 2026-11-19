package testkit

import (
	"os"
	"testing"

	"crimsonledger/internal/config"
)

// TempHome points CRIMSON_LEDGER_HOME at a fresh 0700 directory for the test and returns it.
func TempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// #nosec G302 -- a directory needs the execute bit
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod temp home: %v", err)
	}
	t.Setenv(config.HomeEnv, dir)
	return dir
}
