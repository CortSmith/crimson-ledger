package apiserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"crimsonledger/internal/store"
)

// Principal is the authenticated caller.
type Principal struct {
	KeyID string
	Scope Scope
	Label string
}

var errUnauthorized = errors.New("unauthorized")

// dummyHash is compared against when the key id is unknown, so that path costs the same as a wrong secret.
var dummyHash = sha256.Sum256([]byte("crimson-ledger dummy key hash"))

// authenticate requires a key of the given scope. Every 401 is identical (no key-existence oracle);
// 403 key_disabled is only reached after the secret verified.
func (s *server) authenticate(scope Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, present := bearer(r.Header.Get("Authorization"))
		p, err := s.principal(r.Context(), token, present, scope)
		switch {
		case errors.Is(err, errUnauthorized):
			s.locks.record(clientIPFrom(r.Context()))
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "missing or invalid API key")
		case errors.Is(err, ErrWrongScope):
			writeError(w, r, http.StatusForbidden, codeForbidden, "this key cannot use this endpoint")
		case errors.Is(err, ErrKeyDisabled), errors.Is(err, ErrKeyExpired):
			writeError(w, r, http.StatusForbidden, codeKeyDisabled, "this key is disabled or expired")
		case err != nil:
			s.internal(w, r, err)
		default:
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
		}
	})
}

func (s *server) principal(ctx context.Context, token string, present bool, want Scope) (Principal, error) {
	tokenScope, id, secret, parseErr := ParseKey(token)
	rec, found, err := s.lookupKey(ctx, id, present && parseErr == nil)
	if err != nil {
		return Principal{}, err
	}
	hash := dummyHash[:]
	if found {
		hash = rec.Hash
	}
	// Always computed, so unknown id, malformed token and wrong secret take the same time.
	good := subtle.ConstantTimeCompare(HashSecret(s.pepper, secret), hash) == 1
	if !present || parseErr != nil || !found || !good || rec.Scope != tokenScope {
		return Principal{}, errUnauthorized
	}
	if err := VerifyKey(rec, want, s.pepper, secret, s.now()); err != nil {
		return Principal{}, err
	}
	if err := s.keys.touch(ctx, rec.ID); err != nil {
		s.log.Warn("touch key failed", "keyId", rec.ID, "err", err.Error())
	}
	return Principal{KeyID: rec.ID, Scope: rec.Scope, Label: rec.Label}, nil
}

// lookupKey reads the key record when lookup is true. An unknown id is not an error (found=false); only a
// store failure is.
func (s *server) lookupKey(ctx context.Context, id string, lookup bool) (rec store.KeyRecord, found bool, err error) {
	if !lookup {
		return rec, false, nil
	}
	rec, err = s.keys.get(ctx, id)
	switch {
	case err == nil:
		return rec, true, nil
	case errors.Is(err, store.ErrNotFound):
		return rec, false, nil
	default:
		return rec, false, err
	}
}

// bearer extracts the token from "Authorization: Bearer <token>" (scheme is case-insensitive).
func bearer(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	return header[len(scheme):], true
}

// PrincipalFrom returns the caller authenticated for this request, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok
}
