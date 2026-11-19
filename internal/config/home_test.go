package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHome_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(HomeEnv, dir)
	got, err := Home()
	if err != nil || got != dir {
		t.Fatalf("Home() = %q, %v; want %q", got, err, dir)
	}
}

func TestHome_DefaultsToUserHome(t *testing.T) {
	fake := t.TempDir()
	t.Setenv(HomeEnv, "")
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)
	got, err := Home()
	if want := filepath.Join(fake, ".crimson-ledger"); err != nil || got != want {
		t.Fatalf("Home() = %q, %v; want %q", got, err, want)
	}
}

func TestHome_ReadsEnvEveryCall(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv(HomeEnv, a)
	first, _ := Home()
	t.Setenv(HomeEnv, b)
	second, _ := Home()
	if first != a || second != b {
		t.Fatalf("Home() cached: %q then %q", first, second)
	}
}

func TestEnsureHome_Creates0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	t.Setenv(HomeEnv, filepath.Join(t.TempDir(), "home"))
	dir, err := EnsureHome()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "data")} {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want dir with mode 0700", p, st, err)
		}
	}
}

func TestEnsureHome_IsIdempotent(t *testing.T) {
	t.Setenv(HomeEnv, t.TempDir())
	a, err1 := EnsureHome()
	b, err2 := EnsureHome()
	if err1 != nil || err2 != nil || a != b {
		t.Fatalf("EnsureHome twice: %q %v / %q %v", a, err1, b, err2)
	}
}

func TestEnsureHome_FailsWhenHomeIsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(HomeEnv, f)
	if _, err := EnsureHome(); err == nil {
		t.Fatal("want error when home is a regular file")
	}
}
