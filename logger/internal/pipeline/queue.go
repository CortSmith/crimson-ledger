package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// MaxQueueBytes bounds the outbox on disk (T13: at most 5 MB).
	MaxQueueBytes = 5 << 20
	queueFileExt  = ".ping"
	dirMode       = 0o700
	fileMode      = 0o600
)

type entry struct {
	name string
	size int64
}

// Queue is a bounded FIFO of encoded pings kept on disk so a restart or an offline evening loses nothing
// until the bound is hit; then the oldest are dropped. Only the file names and sizes live in memory.
// It is safe for concurrent use.
type Queue struct {
	mu      sync.Mutex
	dir     string
	max     int64
	items   []entry
	bytes   int64
	seq     uint64
	dropped int
}

// OpenQueue opens (creating it with mode 0700) the outbox in dir and adopts pings left by a previous run.
// Files that are unreadable or not ours are skipped, never fatal.
func OpenQueue(dir string, maxBytes int64) (*Queue, error) {
	if maxBytes <= 0 {
		maxBytes = MaxQueueBytes
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create outbox: %w", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read outbox: %w", err)
	}
	q := &Queue{dir: dir, max: maxBytes}
	for _, e := range ents {
		q.adopt(e)
	}
	sort.Slice(q.items, func(i, j int) bool { return q.items[i].name < q.items[j].name })
	q.trim()
	return q, nil
}

func (q *Queue) adopt(e os.DirEntry) {
	n, ok := strings.CutSuffix(e.Name(), queueFileExt)
	if !ok || e.IsDir() {
		return
	}
	seq, err := strconv.ParseUint(n, 10, 64)
	info, ierr := e.Info()
	if err != nil || ierr != nil || !info.Mode().IsRegular() {
		return
	}
	q.items = append(q.items, entry{name: e.Name(), size: info.Size()})
	q.bytes += info.Size()
	q.seq = max(q.seq, seq)
}

// Push appends one encoded ping, then drops the oldest entries while over the bound.
func (q *Queue) Push(data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	name := fmt.Sprintf("%020d%s", q.seq, queueFileExt)
	if err := os.WriteFile(filepath.Join(q.dir, name), data, fileMode); err != nil {
		return fmt.Errorf("write outbox: %w", err)
	}
	q.items = append(q.items, entry{name: name, size: int64(len(data))})
	q.bytes += int64(len(data))
	q.trim()
	return nil
}

// trim drops oldest entries until the queue fits. The newest entry always stays.
func (q *Queue) trim() {
	for q.bytes > q.max && len(q.items) > 1 {
		q.removeAt(0)
		q.dropped++
	}
}

func (q *Queue) removeAt(i int) {
	e := q.items[i]
	_ = os.Remove(filepath.Join(q.dir, e.name))
	q.bytes -= e.size
	q.items = append(q.items[:i], q.items[i+1:]...)
}

// ErrEmpty means the queue holds nothing.
var ErrEmpty = errors.New("outbox is empty")

// Peek returns the oldest ping without removing it. A corrupt (unreadable) file is removed and skipped.
func (q *Queue) Peek() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) > 0 {
		b, err := os.ReadFile(filepath.Join(q.dir, q.items[0].name)) // #nosec G304 -- name is generated here
		if err == nil && len(b) > 0 {
			return b, nil
		}
		q.removeAt(0)
	}
	return nil, ErrEmpty
}

// Pop removes the oldest ping (after it was sent or refused for good).
func (q *Queue) Pop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 {
		q.removeAt(0)
	}
}

// Len is how many pings wait.
func (q *Queue) Len() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.items) }

// Bytes is the disk space the pings use.
func (q *Queue) Bytes() int64 { q.mu.Lock(); defer q.mu.Unlock(); return q.bytes }

// Dropped counts pings thrown away because the queue was full.
func (q *Queue) Dropped() int { q.mu.Lock(); defer q.mu.Unlock(); return q.dropped }
