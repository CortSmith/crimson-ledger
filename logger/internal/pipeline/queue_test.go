package pipeline

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestQueue_FIFOAndPersistsAcrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	q, err := OpenQueue(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "b", "c"} {
		if err := q.Push([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	q.Pop()
	q2, err := OpenQueue(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q2.Len() != 2 {
		t.Fatalf("len = %d after restart, want 2", q2.Len())
	}
	for _, want := range []string{"b", "c"} {
		got, err := q2.Peek()
		if err != nil || string(got) != want {
			t.Fatalf("peek = %q, %v; want %q", got, err, want)
		}
		q2.Pop()
	}
	if _, err := q2.Peek(); !errors.Is(err, ErrEmpty) {
		t.Errorf("peek on empty = %v", err)
	}
	if err := q2.Push([]byte("d")); err != nil { // sequence continues, no name clash
		t.Fatal(err)
	}
}

func TestQueue_BoundDropsOldest(t *testing.T) {
	q, _ := OpenQueue(t.TempDir(), 1000)
	for i := 0; i < 30; i++ {
		_ = q.Push(append(bytes.Repeat([]byte{'x'}, 99), byte('A'+i%26)))
	}
	if q.Bytes() > 1000 || q.Len() != 10 || q.Dropped() != 20 {
		t.Errorf("bytes=%d len=%d dropped=%d, want <=1000/10/20", q.Bytes(), q.Len(), q.Dropped())
	}
	first, _ := q.Peek()
	if first[99] != byte('A'+20%26) {
		t.Errorf("oldest kept = %c, want the 21st push", first[99])
	}
}

func TestQueue_OversizedSingleEntryIsKept(t *testing.T) {
	q, _ := OpenQueue(t.TempDir(), 10)
	_ = q.Push(bytes.Repeat([]byte{'x'}, 50))
	if q.Len() != 1 {
		t.Errorf("len = %d; the newest entry must always stay", q.Len())
	}
}

func TestQueue_CorruptAndForeignFilesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "abc.ping"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "00000000000000000001.ping"), nil, 0o600) // empty = corrupt
	_ = os.WriteFile(filepath.Join(dir, "00000000000000000002.ping"), []byte("good"), 0o600)
	q, err := OpenQueue(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.Peek()
	if err != nil || string(got) != "good" {
		t.Fatalf("peek = %q, %v", got, err)
	}
}

func TestQueue_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	dir := filepath.Join(t.TempDir(), "outbox")
	q, _ := OpenQueue(dir, 0)
	_ = q.Push([]byte("x"))
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", di.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	fi, _ := ents[0].Info()
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v", fi.Mode().Perm())
	}
}
