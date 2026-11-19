package domain

import "testing"

func TestNormalizeName(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"accents", "Élan Vital", "elan vital"},
		{"double spaces", "Mc   Cheeki ", "mc cheeki"},
		{"mixed case", "MiXeD", "mixed"},
		{"extended-A", "Łódź Śnięty", "lodz sniety"},
		{"combining mark", "Café", "cafe"},
		{"tabs and newlines", "a\t\nb", "a b"},
		{"unmapped kept", "日本", "日本"},
		{"dotted capital I", "İ", "i"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeName(tt.in); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestEnums_Valid(t *testing.T) {
	if !ReactionFriendly.Valid() || Reaction("").Valid() || !FixApprox.Valid() || FixKind("x").Valid() {
		t.Fatal("enum Valid wrong")
	}
	for _, r := range []Ruleset{RulesetNormal, RulesetPvP, RulesetRP, RulesetRPPvP} {
		if !r.Valid() {
			t.Fatal(r)
		}
	}
	if Ruleset("PVP").Valid() {
		t.Fatal("ruleset is case sensitive")
	}
}
