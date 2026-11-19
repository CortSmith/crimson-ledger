package apiserver

import (
	"context"
	"net"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxClientIP
	ctxPrincipal
)

func requestIDOf(r *http.Request) string {
	id, _ := r.Context().Value(ctxRequestID).(string)
	return id
}

func clientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIP).(string)
	return ip
}

// handler assembles the chain, outermost first:
// requestID, accessLog, recoverPanics, securityHeaders, clientIP, preAuthLimit, dispatch (route match),
// then per route: authenticate, keyLimit, bodyLimit, handler.
func (s *server) handler(routes []route) http.Handler {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.pattern, s.routeChain(rt))
	}
	h := s.dispatch(mux)
	for _, mw := range []func(http.Handler) http.Handler{
		s.preAuthLimit, s.clientIP, s.securityHeaders, s.recoverPanics, s.accessLog, s.requestID,
	} {
		h = mw(h)
	}
	return h
}

func (s *server) routeChain(rt route) http.Handler {
	h := s.bodyLimit(rt.handler)
	if rt.scope == "" {
		return h
	}
	return s.authenticate(rt.scope, s.keyLimit(h))
}

func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.log.Info("request", "requestId", requestIDOf(r), "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "ms", s.now().Sub(start).Milliseconds(), "ip", clientIPFrom(r.Context()))
	})
}

func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "requestId", requestIDOf(r), "value", v, "stack", string(debug.Stack()))
				writeError(w, r, http.StatusInternalServerError, codeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) clientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPOf(r, s.opts.TrustProxyHeaders)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClientIP, ip)))
	})
}

// clientIPOf is the connection's address; with trust it is the last X-Forwarded-For entry (the one our
// own proxy appended), never an earlier entry a client could have forged.
func clientIPOf(r *http.Request, trustProxy bool) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !trustProxy {
		return remote
	}
	lines := r.Header.Values("X-Forwarded-For")
	if len(lines) == 0 {
		return remote
	}
	parts := strings.Split(lines[len(lines)-1], ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(last) == nil {
		return remote
	}
	return last
}

func (s *server) preAuthLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPFrom(r.Context())
		if locked, wait := s.locks.locked(ip); locked {
			writeRateLimited(w, r, wait)
			return
		}
		ok, wait, err := s.limiter.Allow(r.Context(), "ip:"+ip, s.opts.RateLimitPerMin, time.Minute)
		switch {
		case err != nil:
			s.limiterFailed(w, r, err)
		case !ok:
			writeRateLimited(w, r, wait)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func (s *server) keyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		ok, wait, err := s.limiter.Allow(r.Context(), "key:"+p.KeyID, s.opts.RateLimitPerMin, time.Minute)
		switch {
		case err != nil:
			s.limiterFailed(w, r, err)
		case !ok:
			writeRateLimited(w, r, wait)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func (s *server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > int64(s.opts.MaxBodyBytes) {
			writeError(w, r, http.StatusRequestEntityTooLarge, codeTooLarge, "request body too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(s.opts.MaxBodyBytes))
		next.ServeHTTP(w, r)
	})
}

// dispatch matches the route itself so 404 and 405 use the standard JSON error shape.
// Only GET and POST are routable; HEAD and OPTIONS on a known path are 405 and no CORS headers are ever sent.
func (s *server) dispatch(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path.Clean(r.URL.Path) {
			writeError(w, r, http.StatusNotFound, codeNotFound, "not found")
			return
		}
		var allowed []string
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			probe := &http.Request{Method: m, URL: r.URL, Host: r.Host}
			if _, pattern := mux.Handler(probe); pattern != "" {
				allowed = append(allowed, m)
			}
		}
		switch {
		case len(allowed) == 0:
			writeError(w, r, http.StatusNotFound, codeNotFound, "not found")
		case contains(allowed, r.Method):
			mux.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
		}
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// limiterFailed fails closed: when the shared counter is down every request gets 503 and Retry-After,
// never a free pass.
func (s *server) limiterFailed(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("rate limiter unavailable", "requestId", requestIDOf(r), "err", err.Error())
	w.Header().Set("Retry-After", unavailableRetryAfter)
	writeError(w, r, http.StatusServiceUnavailable, codeUnavailable, "temporarily unavailable")
}
