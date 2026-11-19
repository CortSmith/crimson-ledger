//go:build integration

package abuse

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// X802: after a full run every file under the data home is owner-only.
func TestX802_FilePermissions(t *testing.T) {
	if tgt.Kind != "local" || tgt.Home == "" {
		t.Skip("local target with CL_HOME only")
	}
	err := filepath.WalkDir(tgt.Home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return err
		}
		if m := info.Mode().Perm(); m&0o077 != 0 {
			t.Errorf("X802 %s: mode %o is readable/writable by group or others", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
