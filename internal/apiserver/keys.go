package apiserver

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"crimsonledger/internal/store"
)

// Scope is what an API key may do.
type Scope = store.Scope

// Key scopes (PLAN.md section 2, D6).
const (
	ScopeIngest = store.ScopeIngest
	ScopeView   = store.ScopeView
)

// Key format: clk_<i|v>_<id 8 chars [a-z0-9]>_<secret 43 chars base64url (32 random bytes)>.
const (
	keyPrefix      = "clk_"
	keyIDLen       = 8
	keySecretBytes = 32
	keySecretLen   = 43 // base64.RawURLEncoding of keySecretBytes
	keyIDAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	// idByteLimit is the largest multiple of len(keyIDAlphabet) below 256; bytes at or above it are
	// skipped so every alphabet character is equally likely.
	idByteLimit = 252
	// minPepperBytes is the shortest HMAC pepper accepted (PLAN.md section 2, D6).
	minPepperBytes = 32

	tokenLen = len(keyPrefix) + 2 + keyIDLen + 1 + keySecretLen // "clk_" + "i_" + id + "_" + secret
)

// Errors returned by ParseKey and VerifyKey.
var (
	ErrMalformedKey = errors.New("malformed API key")
	ErrBadSecret    = errors.New("wrong key secret")
	ErrWrongScope   = errors.New("key scope does not allow this endpoint")
	ErrKeyDisabled  = errors.New("key disabled")
	ErrKeyExpired   = errors.New("key expired")
)

// GenerateKey makes a new key. token is shown to the user once; rec is what gets stored.
// A ttl of zero means the key never expires.
func GenerateKey(scope Scope, pepper []byte, rnd io.Reader, now time.Time, ttl time.Duration) (token string, rec store.KeyRecord, err error) {
	letter, ok := scopeLetter(scope)
	if !ok {
		return "", rec, fmt.Errorf("unknown scope %q", scope)
	}
	if len(pepper) < minPepperBytes {
		return "", rec, fmt.Errorf("pepper must be at least %d bytes", minPepperBytes)
	}
	id, err := randomID(rnd)
	if err != nil {
		return "", rec, fmt.Errorf("generate key id: %w", err)
	}
	raw := make([]byte, keySecretBytes)
	if _, err := io.ReadFull(rnd, raw); err != nil {
		return "", rec, fmt.Errorf("generate key secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	rec = store.KeyRecord{ID: id, Scope: scope, Hash: HashSecret(pepper, secret), CreatedAt: now}
	if ttl > 0 {
		exp := now.Add(ttl)
		rec.ExpiresAt = &exp
	}
	return keyPrefix + letter + "_" + id + "_" + secret, rec, nil
}

func randomID(rnd io.Reader) (string, error) {
	id := make([]byte, 0, keyIDLen)
	buf := make([]byte, keyIDLen*2)
	for len(id) < keyIDLen {
		if _, err := io.ReadFull(rnd, buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b < idByteLimit && len(id) < keyIDLen {
				id = append(id, keyIDAlphabet[int(b)%len(keyIDAlphabet)])
			}
		}
	}
	return string(id), nil
}

// ParseKey splits a token into scope, id and secret without touching any store.
func ParseKey(token string) (scope Scope, id, secret string, err error) {
	if len(token) != tokenLen || token[:len(keyPrefix)] != keyPrefix {
		return "", "", "", ErrMalformedKey
	}
	idStart := len(keyPrefix) + 2
	secretStart := idStart + keyIDLen + 1
	scope, ok := letterScope(token[len(keyPrefix)])
	if !ok || token[len(keyPrefix)+1] != '_' || token[secretStart-1] != '_' {
		return "", "", "", ErrMalformedKey
	}
	id, secret = token[idStart:idStart+keyIDLen], token[secretStart:]
	if !allIn(id, isIDChar) || !allIn(secret, isSecretChar) {
		return "", "", "", ErrMalformedKey
	}
	return scope, id, secret, nil
}

func scopeLetter(s Scope) (string, bool) {
	switch s {
	case ScopeIngest:
		return "i", true
	case ScopeView:
		return "v", true
	}
	return "", false
}

func letterScope(b byte) (Scope, bool) {
	switch b {
	case 'i':
		return ScopeIngest, true
	case 'v':
		return ScopeView, true
	}
	return "", false
}

func allIn(s string, ok func(byte) bool) bool {
	for i := range len(s) {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

func isIDChar(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

func isSecretChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// HashSecret is the stored verifier: HMAC-SHA256(pepper, secret).
func HashSecret(pepper []byte, secret string) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(secret))
	return mac.Sum(nil)
}

// VerifyKey checks a presented secret against a stored record. The secret is checked first so that
// the state of a key (disabled, expired, scope) is only revealed to someone who holds it.
func VerifyKey(rec store.KeyRecord, scope Scope, pepper []byte, secret string, now time.Time) error {
	if subtle.ConstantTimeCompare(HashSecret(pepper, secret), rec.Hash) != 1 {
		return ErrBadSecret
	}
	switch {
	case rec.Disabled:
		return ErrKeyDisabled
	case rec.ExpiresAt != nil && !now.Before(*rec.ExpiresAt):
		return ErrKeyExpired
	case rec.Scope != scope:
		return ErrWrongScope
	}
	return nil
}

// newRequestID returns 8 hex characters, enough to find one request in a log.
func newRequestID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b) // never fails since Go 1.24
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 8)
	for i, v := range b {
		out[i*2], out[i*2+1] = hexDigits[v>>4], hexDigits[v&15]
	}
	return string(out)
}
