package domain

import "testing"

func TestParseGUID(t *testing.T) {
	tests := []struct {
		in   string
		kind string
		ok   bool
	}{
		{"Player-1329-0A1B2C3D", "Player", true},
		{"Creature-0-ABCD", "Creature", true},
		{"Pet-1-0123456789ABCDEF", "Pet", true},
		{"Vehicle-9-ABCD", "Vehicle", true},
		{"Foo-1-2", "", false},
		{"Player-1-ABC", "", false},
		{"Player-1-0123456789ABCDEF0", "", false},
		{"Player--ABCD", "", false},
		{"Player-1-abcd", "", false},
		{" Player-1-ABCD", "", false},
		{"", "", false},
		{"Player-1-ABCD\n", "", false},
	}
	for _, tt := range tests {
		kind, ok := ParseGUID(tt.in)
		if kind != tt.kind || ok != tt.ok {
			t.Errorf("ParseGUID(%q) = %q,%v", tt.in, kind, ok)
		}
	}
}

func TestIsUUIDv4(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44", true},
		{"6F1C2E0E-8B5A-4A9E-9D34-0A7C1D2F3B44", true},
		{"6f1c2e0e-8b5a-1a9e-9d34-0a7c1d2f3b44", false},
		{"6f1c2e0e8b5a4a9e9d340a7c1d2f3b44", false},
		{"6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b4", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsUUIDv4(tt.in); got != tt.ok {
			t.Errorf("IsUUIDv4(%q) = %v", tt.in, got)
		}
	}
}
