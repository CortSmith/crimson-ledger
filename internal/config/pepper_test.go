package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreatePepper_CreatesThenReusesSameBytes(t *testing.T) {
	t.Setenv(HomeEnv, t.TempDir())
	first, err := LoadOrCreatePepper()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != PepperBytes {
		t.Fatalf("pepper is %d bytes, want %d", len(first), PepperBytes)
	}
	second, err := LoadOrCreatePepper()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second call returned different bytes")
	}
}

func TestLoadOrCreatePepper_FileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	if _, err := LoadOrCreatePepper(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(home, PepperFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o, want 600", st.Mode().Perm())
	}
}

func TestLoadOrCreatePepper_WhenWrongSize_Refuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	if err := os.WriteFile(filepath.Join(home, PepperFile), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreatePepper(); err == nil {
		t.Fatal("want an error for a 5-byte pepper")
	}
}

func TestLoadOrCreatePepper_WhenGroupReadable_Refuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	if err := os.WriteFile(filepath.Join(home, PepperFile), make([]byte, PepperBytes), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreatePepper(); err == nil {
		t.Fatal("want an error for a group-readable pepper")
	}
}
