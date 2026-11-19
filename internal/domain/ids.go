package domain

import "regexp"

var (
	// guidRE is the GUID format from PLAN section 5.
	guidRE = regexp.MustCompile(`^(Player|Creature|Pet|Vehicle)-\d+-[0-9A-F]{4,16}$`)
	// uuidV4RE matches a UUID whose version nibble is 4 and variant is 8, 9, a or b.
	uuidV4RE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	// sourceRE is the allowed source label shape (length is bounded by MaxSourceLen).
	sourceRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
)

// ParseGUID reports the unit kind ("Player", "Creature", "Pet" or "Vehicle") of a
// well-formed GUID. ok is false for anything else.
func ParseGUID(s string) (kind string, ok bool) {
	if len(s) > 64 || !guidRE.MatchString(s) {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			return s[:i], true
		}
	}
	return "", false
}

// IsUUIDv4 reports whether s is a canonical, hyphenated UUID version 4.
func IsUUIDv4(s string) bool { return len(s) == 36 && uuidV4RE.MatchString(s) }
