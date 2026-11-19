//go:build !windows

package config

import (
	"fmt"
	"os"
)

const (
	privateFileMode = 0o600
	groupOtherBits  = 0o077
)

// checkPrivate returns an error wrapping fs.ErrNotExist when path is missing, and refuses
// symlinks, non-regular files and files that group or others can access.
func checkPrivate(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to follow it", path)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if st.Mode().Perm()&groupOtherBits != 0 {
		return fmt.Errorf("%s has permissions %04o, other users can read it; run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	return nil
}

// makePrivate restricts path (a file or a directory) to its owner.
func makePrivate(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return os.Chmod(path, privateDirMode)
	}
	return os.Chmod(path, privateFileMode)
}
