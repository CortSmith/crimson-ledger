// Package secret holds the few helpers that keep API keys out of the UI, logs and errors.
package secret

import (
	"crypto/subtle"
	"regexp"
)

// maskText is what the UI shows instead of a key. Its length is fixed so the key length does not leak.
const maskText = "••••••••••••"

// tokenPattern matches an API key anywhere in a string: clk_<i|v>_<8 char id>_<secret>.
// The secret is matched from 20 characters up so truncated or odd keys are still hidden.
var tokenPattern = regexp.MustCompile(`clk_[iv]_([a-z0-9]{8})_[A-Za-z0-9_-]{20,}`)

// Mask returns the fixed placeholder (12 dots) shown in place of an API key.
func Mask() string { return maskText }

// Redact replaces the secret part of every API key in s with "[redacted]", keeping the key id.
// The output uses "clk_x_" so a redacted key never matches the key format again.
func Redact(s string) string { return tokenPattern.ReplaceAllString(s, "clk_x_${1}_[redacted]") }

// Equal compares two secrets in constant time.
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
