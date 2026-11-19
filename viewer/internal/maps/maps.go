// Package maps serves the bundled zone images to the page as data: URIs. Images are static assets, not database
// data, so they live in the binary; the chart rectangles still come from the API.
package maps

import (
	"container/list"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"path"
	"strings"
	"sync"
)

// cacheSize is how many encoded images stay in memory (PLAN T14: LRU of 8).
const cacheSize = 8

// ErrNotFound means no bundled image matches the zone.
var ErrNotFound = errors.New("no bundled map for that zone")

type entry struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// Store reads images from an fs.FS holding index.json.
type Store struct {
	fsys  fs.FS
	zones map[string]entry
	mu    sync.Mutex
	lru   *list.List // front = newest; values are *cached
	byKey map[string]*list.Element
}

type cached struct{ key, uri string }

// Open parses index.json from fsys.
func Open(fsys fs.FS) (*Store, error) {
	raw, err := fs.ReadFile(fsys, "index.json")
	if err != nil {
		return nil, fmt.Errorf("read map index: %w", err)
	}
	var idx struct {
		Zones map[string]entry `json:"zones"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("parse map index: %w", err)
	}
	return &Store{fsys: fsys, zones: idx.Zones, lru: list.New(), byKey: map[string]*list.Element{}}, nil
}

// Normalize lowercases name and keeps only [a-z0-9] (the key form used by index.json).
func Normalize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Has reports whether an image file exists for the zone name.
func (s *Store) Has(zone string) bool {
	_, ok := s.file(zone)
	return ok
}

func (s *Store) file(zone string) (string, bool) {
	e, ok := s.zones[Normalize(zone)]
	if !ok || e.File == "" || e.File != path.Base(e.File) {
		return "", false
	}
	for _, name := range []string{strings.TrimSuffix(e.File, path.Ext(e.File)) + ".webp", e.File} {
		if _, err := fs.Stat(s.fsys, name); err == nil {
			return name, true
		}
	}
	return "", false
}

// DataURI returns the zone image as a data: URI.
func (s *Store) DataURI(zone string) (string, error) {
	name, ok := s.file(zone)
	if !ok {
		return "", ErrNotFound
	}
	if uri, hit := s.get(name); hit {
		return uri, nil
	}
	raw, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return "", fmt.Errorf("read map %s: %w", name, err)
	}
	typ := mime.TypeByExtension(path.Ext(name))
	if typ == "" {
		typ = "application/octet-stream"
	}
	uri := "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(raw)
	s.put(name, uri)
	return uri, nil
}

func (s *Store) get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.byKey[key]
	if !ok {
		return "", false
	}
	s.lru.MoveToFront(el)
	return el.Value.(*cached).uri, true
}

func (s *Store) put(key, uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byKey[key]; ok {
		return
	}
	s.byKey[key] = s.lru.PushFront(&cached{key, uri})
	for s.lru.Len() > cacheSize {
		old := s.lru.Remove(s.lru.Back()).(*cached)
		delete(s.byKey, old.key)
	}
}

// Cached reports how many images are held (for tests).
func (s *Store) Cached() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lru.Len()
}
