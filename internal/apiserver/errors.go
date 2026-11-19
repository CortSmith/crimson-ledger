package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Error codes of the API (PLAN.md section 5.4). Each appears once, here.
const (
	codeInvalidRequest   = "invalid_request"
	codeInvalidPing      = "invalid_ping"
	codeUnauthorized     = "unauthorized"
	codeForbidden        = "forbidden"
	codeKeyDisabled      = "key_disabled"
	codeNotFound         = "not_found"
	codeMethodNotAllowed = "method_not_allowed"
	codeTooLarge         = "too_large"
	codeUnsupportedMedia = "unsupported_media_type"
	codeRateLimited      = "rate_limited"
	codeInternal         = "internal"
)

const jsonContentType = "application/json; charset=utf-8"

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type errorBody struct {
	Error apiError `json:"error"`
}

// writeError sends the one error shape of the API. msg must be written by us: never pass err.Error().
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	h := w.Header()
	h.Set("Content-Type", jsonContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{apiError{Code: code, Message: msg, RequestID: requestIDOf(r)}})
}

// writeRateLimited sends 429 with Retry-After rounded up to whole seconds (at least 1).
func writeRateLimited(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, r, http.StatusTooManyRequests, codeRateLimited, "too many requests")
}

// writeBodyError maps a body read error: 413 when the limit was hit, else 400.
func writeBodyError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, r, http.StatusRequestEntityTooLarge, codeTooLarge, "request body too large")
		return
	}
	writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "could not read request body")
}
