package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// maxConfigBytes caps how much of a config file is read (PLAN.md section 7: every read is size-limited).
	maxConfigBytes = 1 << 20
	// tempPrefix names in-flight temp files so leftovers are recognisable.
	tempPrefix = ".tmp-"
)

// Validator is implemented by every config struct (on the value receiver).
type Validator interface{ Validate() error }

// Load reads <home>/<name>, applying def() when the file is missing (and writing it).
// It rejects unknown fields, symlinks and, on Unix, files readable by group or others.
func Load[T Validator](name string, def func() T) (T, error) {
	var zero T
	path, err := configPath(name)
	if err != nil {
		return zero, err
	}
	if err := checkPrivate(path); errors.Is(err, fs.ErrNotExist) {
		v := def()
		return v, Save(name, v)
	} else if err != nil {
		return zero, err
	}
	raw, err := readLimited(path)
	if err != nil {
		return zero, err
	}
	return decode[T](raw, path)
}

// Save validates v, then writes <home>/<name> atomically with mode 0600.
func Save[T Validator](name string, v T) error {
	if err := v.Validate(); err != nil {
		return err
	}
	path, err := configPath(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	data = append(data, '\n')
	return writeAtomicWith(path, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

// configPath returns the absolute path of a config file name, creating the home directory.
func configPath(name string) (string, error) {
	if name == "" || name == "." || filepath.Base(name) != name {
		return "", fmt.Errorf("config file name %q must be a plain file name", name)
	}
	dir, err := EnsureHome()
	if err != nil {
		return "", fmt.Errorf("config home: %w", err)
	}
	return filepath.Join(dir, name), nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- path is <home>/<plain name>, checked by checkPrivate
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // read-only handle: nothing to lose on close
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxConfigBytes)
	}
	return raw, nil
}

// decode parses raw strictly (no unknown fields, no trailing data) and validates the result.
func decode[T Validator](raw []byte, path string) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("parse %s: %w", path, err)
	}
	if dec.More() {
		return v, fmt.Errorf("parse %s: trailing data after the JSON object", path)
	}
	if err := v.Validate(); err != nil {
		return v, err
	}
	return v, nil
}

// writeAtomicWith lets write fill a private temp file in path's directory, then fsyncs and renames it
// over path. On any failure the temp file is removed and path is untouched.
func writeAtomicWith(path string, write func(*os.File) error) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*") // same dir => same filesystem => atomic rename
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmp.Name()) // no-op after a successful rename
	}()
	if err := makePrivate(tmp.Name()); err != nil {
		return fmt.Errorf("restrict temp file to its owner: %w", err)
	}
	if err := write(tmp); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	closed = true
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
