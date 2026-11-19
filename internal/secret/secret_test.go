package secret

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const body43 = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde" // 43 chars

func TestMask_FixedLength(t *testing.T) {
	if got := Mask(); utf8.RuneCountInString(got) != 12 || strings.Trim(got, "•") != "" {
		t.Fatalf("Mask() = %q, want 12 bullet dots", got)
	}
}

func TestRedact(t *testing.T) {
	if len(body43) != 43 {
		t.Fatalf("fixture is %d chars", len(body43))
	}
	tests := []struct {
		name, in, want string
	}{
		{"ingest key keeps id", "auth failed for clk_i_k3f9a2x1_" + body43 + " today", "auth failed for clk_x_k3f9a2x1_[redacted] today"},
		{"viewer key", "clk_v_abcd1234_" + body43, "clk_x_abcd1234_[redacted]"},
		{"two keys", "clk_i_aaaaaaaa_" + body43 + " clk_v_bbbbbbbb_" + body43, "clk_x_aaaaaaaa_[redacted] clk_x_bbbbbbbb_[redacted]"},
		{"no key untouched", "nothing to hide", "nothing to hide"},
		{"empty", "", ""},
		{"short body untouched", "clk_i_k3f9a2x1_short", "clk_i_k3f9a2x1_short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedact_IsIdempotent(t *testing.T) {
	once := Redact("x clk_i_k3f9a2x1_" + body43)
	if twice := Redact(once); twice != once {
		t.Errorf("second pass changed %q to %q", once, twice)
	}
}

func TestEqual_ConstantTime(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "abcd", false},
		{"", "", true},
		{"", "a", false},
	}
	for _, tc := range tests {
		if got := Equal(tc.a, tc.b); got != tc.want {
			t.Errorf("Equal(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func FuzzRedact(f *testing.F) {
	f.Add("", "")
	f.Add("auth failed: ", " end")
	f.Add("clk_i_", "_")
	f.Add("clk_v_abcd1234_", "\x00")
	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		key := "clk_i_k3f9a2x1_" + body43
		out := Redact(prefix + key + suffix)
		if strings.Contains(out, body43) {
			t.Fatalf("secret survived: %q", out)
		}
	})
}
