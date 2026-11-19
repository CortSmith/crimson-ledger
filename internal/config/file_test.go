package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func newHome(t *testing.T) string {
	t.Helper()
	t.Setenv(HomeEnv, t.TempDir())
	dir, err := EnsureHome()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics")
	}
}

func TestLoad_MissingFileWritesDefaults(t *testing.T) {
	dir := newHome(t)
	got, err := Load("viewer.json", ViewerDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ViewerDefaults()) {
		t.Errorf("got %+v, want defaults", got)
	}
	again, err := Load("viewer.json", ViewerDefaults)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("second load: %+v, %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "viewer.json")); err != nil {
		t.Errorf("defaults were not written: %v", err)
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	newHome(t)
	want := LoggerDefaults()
	want.SourceName = "other-pc"
	want.APIKey = validKey
	if err := Save("logger.json", want); err != nil {
		t.Fatal(err)
	}
	got, err := Load("logger.json", LoggerDefaults)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestLoad_RejectsUnknownField(t *testing.T) {
	dir := newHome(t)
	writeFile(t, filepath.Join(dir, "viewer.json"), `{"version":1,"typo":2}`, 0o600)
	_, err := Load("viewer.json", ViewerDefaults)
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("err = %v, want mention of typo", err)
	}
}

func TestLoad_RejectsTrailingData(t *testing.T) {
	dir := newHome(t)
	writeFile(t, filepath.Join(dir, "viewer.json"), `{"version":1} {"version":1}`, 0o600)
	if _, err := Load("viewer.json", ViewerDefaults); err == nil {
		t.Fatal("want trailing data error")
	}
}

func TestLoad_RejectsGroupReadable(t *testing.T) {
	skipOnWindows(t)
	dir := newHome(t)
	writeFile(t, filepath.Join(dir, "viewer.json"), `{"version":1}`, 0o644)
	_, err := Load("viewer.json", ViewerDefaults)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want chmod 600 hint", err)
	}
}

func TestLoad_RejectsBadVersion(t *testing.T) {
	dir := newHome(t)
	for _, v := range []string{`{"version":2}`, `{}`, `{"version":0}`} {
		writeFile(t, filepath.Join(dir, "viewer.json"), v, 0o600)
		if _, err := Load("viewer.json", ViewerDefaults); err == nil {
			t.Errorf("%s: want version error", v)
		}
	}
}

func TestLoad_RejectsSymlink(t *testing.T) {
	skipOnWindows(t)
	dir := newHome(t)
	target := filepath.Join(t.TempDir(), "real.json")
	writeFile(t, target, `{"version":1}`, 0o600)
	if err := os.Symlink(target, filepath.Join(dir, "viewer.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("viewer.json", ViewerDefaults); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want symlink refusal", err)
	}
}

func TestLoad_RejectsDirectoryAndOversize(t *testing.T) {
	dir := newHome(t)
	if err := os.Mkdir(filepath.Join(dir, "viewer.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("viewer.json", ViewerDefaults); err == nil {
		t.Error("want error for a directory")
	}
	writeFile(t, filepath.Join(dir, "api.json"), `{"version":1,"db_path":"`+strings.Repeat("a", maxConfigBytes)+`"}`, 0o600)
	if _, err := Load("api.json", APIDefaults); err == nil {
		t.Error("want error for oversize file")
	}
}

func TestLoad_RejectsPathInName(t *testing.T) {
	newHome(t)
	for _, n := range []string{"../x.json", "a/b.json", "", "."} {
		if _, err := Load(n, ViewerDefaults); err == nil {
			t.Errorf("name %q accepted", n)
		}
		if err := Save(n, ViewerDefaults()); err == nil {
			t.Errorf("Save name %q accepted", n)
		}
	}
}

func TestLoad_InvalidFileNamesTheFile(t *testing.T) {
	dir := newHome(t)
	writeFile(t, filepath.Join(dir, "viewer.json"), `{"version":1,"server_url":"http://example.com","poll_interval_s":15}`, 0o600)
	_, err := Load("viewer.json", ViewerDefaults)
	if err == nil || !strings.Contains(err.Error(), "viewer.json") {
		t.Fatalf("err = %v", err)
	}
}

func TestSave_RejectsInvalid(t *testing.T) {
	dir := newHome(t)
	v := ViewerDefaults()
	v.PollIntervalS = 1
	if err := Save("viewer.json", v); err == nil {
		t.Fatal("want validation error")
	}
	if _, err := os.Stat(filepath.Join(dir, "viewer.json")); err == nil {
		t.Error("invalid config was written")
	}
}

func TestSave_FileModeIs0600(t *testing.T) {
	skipOnWindows(t)
	dir := newHome(t)
	if err := Save("viewer.json", ViewerDefaults()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "viewer.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", st.Mode(), err)
	}
}

func TestSave_Atomic_NoPartialFileOnCrash(t *testing.T) {
	dir := newHome(t)
	path := filepath.Join(dir, "viewer.json")
	if err := Save("viewer.json", ViewerDefaults()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	boom := errors.New("disk full")
	err := writeAtomicWith(path, func(f *os.File) error {
		_, _ = f.WriteString(`{"version":1,"serv`)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("original changed: %q", after)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestWriteAtomic_TempFileIsPrivateBeforeAnyByteIsWritten(t *testing.T) {
	skipOnWindows(t)
	dir := newHome(t)
	path := filepath.Join(dir, "viewer.json")
	var mode os.FileMode
	err := writeAtomicWith(path, func(f *os.File) error {
		st, err := os.Stat(f.Name())
		if err != nil {
			return err
		}
		mode = st.Mode().Perm()
		_, err = f.WriteString("{}")
		return err
	})
	if err != nil || mode&0o077 != 0 {
		t.Fatalf("temp mode %v, err %v; want no group/other bits", mode, err)
	}
}

func FuzzLoadBytes(f *testing.F) {
	seeds := []string{
		`{"version":1}`, `{"version":1,"typo":2}`, `{`, ``, `null`, `[]`, `{"version":"1"}`,
		`{"version":1,"server_url":"https://x.y","poll_interval_s":15,"ruleset":"auto"}`,
		`{"version":1,"listen":"127.0.0.1:8443","store":"sqlite","retention_days":30}`,
		`{"version":1,"server_url":"http://localhost","source_name":"a","ruleset":"pvp","upload_interval_ms":2000,"batch_max_contacts":200}`,
		`{"version":1} trailing`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, raw []byte) {
		_, _ = decode[APIConfig](raw, "api.json")
		_, _ = decode[LoggerConfig](raw, "logger.json")
		_, _ = decode[ViewerConfig](raw, "viewer.json")
	})
}
