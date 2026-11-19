package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// PepperFile is the name of the HMAC pepper file under the config home (PLAN.md section 4).
	PepperFile = "api.pepper"
	// PepperBytes is the pepper size: 32 random bytes (PLAN.md section 2, D6).
	PepperBytes = 32
	// pepperFileMode is owner read/write only; Windows ACLs are applied by makePrivate.
	pepperFileMode = 0o600
)

// LoadOrCreatePepper returns the HMAC pepper used to hash API keys, creating <home>/api.pepper
// (mode 0600, 32 bytes from crypto/rand) on first use. A file of the wrong size, a symlink or a
// file other users can read is an error: replacing it would silently invalidate every key.
func LoadOrCreatePepper() ([]byte, error) {
	dir, err := EnsureHome()
	if err != nil {
		return nil, fmt.Errorf("config home: %w", err)
	}
	path := filepath.Join(dir, PepperFile)
	if err := checkPrivate(path); errors.Is(err, fs.ErrNotExist) {
		return createPepper(path)
	} else if err != nil {
		return nil, err
	}
	raw, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	if len(raw) != PepperBytes {
		return nil, fmt.Errorf("%s holds %d bytes, want %d; restore it from a backup (a new pepper invalidates every API key)", path, len(raw), PepperBytes)
	}
	return raw, nil
}

func createPepper(path string) ([]byte, error) {
	pepper := make([]byte, PepperBytes)
	if _, err := rand.Read(pepper); err != nil {
		return nil, fmt.Errorf("generate pepper: %w", err)
	}
	// O_EXCL: if another process created the file first, fail instead of overwriting its pepper.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, pepperFileMode) // #nosec G304 -- <home>/api.pepper
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(pepper); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close %s: %w", path, err)
	}
	if err := makePrivate(path); err != nil {
		return nil, fmt.Errorf("restrict %s to its owner: %w", path, err)
	}
	return pepper, nil
}
