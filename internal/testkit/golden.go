package testkit

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
)

var update = flag.Bool("update", false, "rewrite golden files instead of comparing")

// Golden compares got with testdata/golden/<name>.golden next to the calling test.
// Run `go test -update` to (re)write the file.
func Golden(t T, name string, got []byte) { GoldenWithUpdate(t, name, got, *update) }

// GoldenWithUpdate is Golden with the update decision passed in (used to test Golden itself).
func GoldenWithUpdate(t T, name string, got []byte, doUpdate bool) {
	t.Helper()
	path := filepath.Join("testdata", "golden", strings.ReplaceAll(name, "/", "_")+".golden")
	if doUpdate {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("golden %s: %v", name, err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path) // #nosec G304 -- fixed testdata directory, name chosen by the test author
	if err != nil {
		t.Fatalf("golden %s: %v (run go test -update to create it)", name, err)
		return
	}
	if !bytes.Equal(want, got) {
		t.Errorf("golden %s differs (run go test -update to accept):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}
