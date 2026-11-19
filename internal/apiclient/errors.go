package apiclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"crimsonledger/internal/secret"
)

// Errors the client returns on its own.
var (
	// ErrCrossHostRedirect means the server tried to send the client to another scheme, host or port.
	ErrCrossHostRedirect = errors.New("server redirected to a different address; refusing to follow")
	// ErrTooManyRedirects means more than maxRedirects hops.
	ErrTooManyRedirects = errors.New("server redirected too many times")
	// ErrResponseTooLarge means the body exceeded maxResponseBytes.
	ErrResponseTooLarge = errors.New("server response is too large")
	// ErrNotJSON means the response was not application/json.
	ErrNotJSON = errors.New("server did not answer with JSON; is this the Crimson Ledger address?")
)

// maxMessageRunes bounds a server-supplied message before it can reach a screen.
const maxMessageRunes = 200

// APIError is an error answer from the server (PLAN.md section 5.4).
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server said %s: %s (request %s)", e.Code, e.Message, e.RequestID)
}

// newAPIError builds an APIError whose text is safe to show: control characters stripped, length bounded, keys redacted.
func newAPIError(status int, code, msg, reqID string) *APIError {
	if code == "" {
		code = http.StatusText(status)
		if code == "" {
			code = "error"
		}
	}
	return &APIError{Status: status, Code: clean(code, 40), Message: clean(msg, maxMessageRunes), RequestID: clean(reqID, 64)}
}

func clean(s string, maxRunes int) string {
	s = secret.Redact(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxRunes {
		s = string(r[:maxRunes])
	}
	return s
}

// IsAuth reports whether err means the key is missing, wrong, revoked or lacks the scope (401 or 403).
func IsAuth(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// IsRetryable reports whether trying the same call again later can succeed.
func IsRetryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return retryableStatus(ae.Status)
	}
	return err != nil && !permanentNetwork(err)
}

// permanentNetwork is true for failures a retry cannot fix: certificate problems and refused redirects.
func permanentNetwork(err error) bool {
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	var tlsVerify *tls.CertificateVerificationError
	return errors.Is(err, ErrCrossHostRedirect) || errors.Is(err, ErrTooManyRedirects) ||
		errors.Is(err, ErrNotJSON) || errors.Is(err, ErrResponseTooLarge) ||
		errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &certInvalid) || errors.As(err, &tlsVerify)
}
