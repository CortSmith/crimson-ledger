package apiserver

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/store"
)

var testPepper = []byte(strings.Repeat("p", 32))

func TestKey_RoundTrip(t *testing.T) {
	now := time.Unix(1731800000, 0)
	for _, scope := range []Scope{ScopeIngest, ScopeView} {
		token, rec, err := GenerateKey(scope, testPepper, rand.Reader, now, 0)
		if err != nil {
			t.Fatalf("GenerateKey(%s): %v", scope, err)
		}
		gotScope, id, secret, err := ParseKey(token)
		if err != nil || gotScope != scope || id != rec.ID {
			t.Fatalf("ParseKey = %q %q %v, want %q %q", gotScope, id, err, scope, rec.ID)
		}
		if err := VerifyKey(rec, scope, testPepper, secret, now); err != nil {
			t.Errorf("VerifyKey on a fresh key: %v", err)
		}
		if rec.ExpiresAt != nil {
			t.Errorf("ttl 0 must mean no expiry, got %v", rec.ExpiresAt)
		}
	}
}

func TestKey_GeneratedKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 20000 {
		token, _, err := GenerateKey(ScopeView, testPepper, rand.Reader, time.Now(), time.Hour)
		if err != nil || seen[token] {
			t.Fatalf("duplicate or failed key: %v", err)
		}
		seen[token] = true
	}
}

func TestKey_GenerateRejectsShortPepper(t *testing.T) {
	if _, _, err := GenerateKey(ScopeView, []byte("short"), rand.Reader, time.Now(), 0); err == nil {
		t.Error("a pepper under 32 bytes must be refused")
	}
}

func TestKey_ParseRejectsMalformed(t *testing.T) {
	good, _, _ := GenerateKey(ScopeIngest, testPepper, rand.Reader, time.Now(), 0)
	rows := []struct{ name, token string }{
		{"empty", ""},
		{"too short", good[:len(good)-1]},
		{"too long", good + "A"},
		{"wrong prefix", "xlk_" + good[4:]},
		{"bad scope letter", good[:4] + "x" + good[5:]},
		{"uppercase id", good[:6] + strings.ToUpper(good[6:14]) + good[14:]},
		{"bad separator", good[:5] + "-" + good[6:]},
		{"secret charset", good[:len(good)-1] + "+"},
		{"space in secret", good[:len(good)-1] + " "},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if _, _, _, err := ParseKey(row.token); !errors.Is(err, ErrMalformedKey) {
				t.Errorf("ParseKey(%q) error = %v, want ErrMalformedKey", row.token, err)
			}
		})
	}
}

func FuzzParseKey(f *testing.F) {
	good, _, _ := GenerateKey(ScopeView, testPepper, rand.Reader, time.Now(), 0)
	f.Add(good)
	f.Add("")
	f.Add("clk_i_abcdefgh_" + strings.Repeat("A", 43))
	f.Fuzz(func(t *testing.T, token string) {
		scope, id, secret, err := ParseKey(token)
		if err != nil {
			return
		}
		letter := map[Scope]string{ScopeIngest: "i", ScopeView: "v"}[scope]
		if rebuilt := "clk_" + letter + "_" + id + "_" + secret; rebuilt != token {
			t.Fatalf("accepted %q but it rebuilds as %q", token, rebuilt)
		}
	})
}

func TestVerifyKey(t *testing.T) {
	now := time.Unix(1731800000, 0)
	token, rec, _ := GenerateKey(ScopeView, testPepper, rand.Reader, now, time.Hour)
	_, _, secret, _ := ParseKey(token)
	past := now.Add(-time.Minute)
	disabled := rec
	disabled.Disabled = true
	expired := rec
	expired.ExpiresAt = &past
	rows := []struct {
		name   string
		rec    store.KeyRecord
		scope  Scope
		secret string
		want   error
	}{
		{"valid", rec, ScopeView, secret, nil},
		{"wrong secret", rec, ScopeView, strings.Repeat("A", 43), ErrBadSecret},
		{"wrong scope", rec, ScopeIngest, secret, ErrWrongScope},
		{"disabled", disabled, ScopeView, secret, ErrKeyDisabled},
		{"expired", expired, ScopeView, secret, ErrKeyExpired},
		{"wrong secret beats disabled", disabled, ScopeView, strings.Repeat("A", 43), ErrBadSecret},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := VerifyKey(row.rec, row.scope, testPepper, row.secret, now); !errors.Is(err, row.want) {
				t.Errorf("VerifyKey = %v, want %v", err, row.want)
			}
		})
	}
}
