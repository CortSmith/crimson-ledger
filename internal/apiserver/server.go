package apiserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"crimsonledger/internal/store"
)

// Deps is everything the API needs from outside. Time and the database are injected so tests are deterministic.
type Deps struct {
	Store   store.Store
	Pepper  []byte           // at least 32 random bytes: api secret file locally, SSM on AWS
	Limiter Limiter          // nil means an in-memory token bucket
	Now     func() time.Time // nil means time.Now
	Log     *slog.Logger     // request id, route, status, duration; never keys or bodies. nil discards
	Opts    Options
}

type server struct {
	store   store.Store
	quota   *quota
	keys    *keyCache
	pepper  []byte
	limiter Limiter
	locks   *failedAuthTracker
	now     func() time.Time
	log     *slog.Logger
	opts    Options
}

// New builds the API handler. It refuses weak configuration instead of starting insecurely.
func New(d Deps) (http.Handler, error) {
	s, err := newServer(d)
	if err != nil {
		return nil, err
	}
	return s.handler(s.routes()), nil
}

func newServer(d Deps) (*server, error) {
	if d.Store == nil {
		return nil, errors.New("apiserver: store is required")
	}
	if len(d.Pepper) < minPepperBytes {
		return nil, errors.New("apiserver: pepper must be at least 32 bytes")
	}
	if err := d.Opts.Validate(); err != nil {
		return nil, err
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	limiter := d.Limiter
	if limiter == nil {
		limiter = NewMemoryLimiter(now)
	}
	log := d.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &server{
		store:   d.Store,
		quota:   newQuota(),
		keys:    newKeyCache(d.Store.Keys(), d.Opts.KeyCacheTTL, now),
		pepper:  d.Pepper,
		limiter: limiter,
		locks:   newFailedAuthTracker(now),
		now:     now,
		log:     slog.New(newRedactingHandler(log.Handler())),
		opts:    d.Opts,
	}, nil
}

// internal logs err with the request id and answers 500 without leaking it.
func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error", "requestId", requestIDOf(r), "err", err.Error())
	writeError(w, r, http.StatusInternalServerError, codeInternal, "internal error")
}
