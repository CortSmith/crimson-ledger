package apiserver

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"
)

// etagBucket is how long a 304 stays valid for time-bound reads without a write (T07 Rules).
const etagBucket = 5 * time.Second

// Cache-Control values of the read endpoints.
const (
	cacheRevalidate = "private, max-age=0, must-revalidate"
	cacheCharts     = "private, max-age=3600"
)

func hash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// etagFor builds W/"<ruleset>.<version>.<hash of query>". A time-bound query also hashes the clock bucket.
func etagFor(q readQuery, version int64, now time.Time) string {
	text := q.canonical()
	if q.timeBound() {
		text += fmt.Sprintf("|%d", now.UnixNano()/int64(etagBucket))
	}
	return fmt.Sprintf(`W/"%s.%d.%016x"`, q.scope(), version, hash64(text))
}

// etagOfBody is the ETag of a response whose only input is its own bytes (charts).
func etagOfBody(body []byte) string { return fmt.Sprintf(`W/"%016x"`, hash64(string(body))) }

// matches reports whether an If-None-Match header selects tag (weak comparison, RFC 9110 section 13.1.2).
func matches(header, tag string) bool {
	want := strings.TrimPrefix(tag, "W/")
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == want {
			return true
		}
	}
	return false
}

// notModified answers 304 with the validators when the client already has tag.
func notModified(w http.ResponseWriter, r *http.Request, tag, cache string) bool {
	if !matches(r.Header.Get("If-None-Match"), tag) {
		return false
	}
	h := w.Header()
	h.Set("ETag", tag)
	h.Set("Cache-Control", cache)
	w.WriteHeader(http.StatusNotModified)
	return true
}
