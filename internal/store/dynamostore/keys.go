package dynamostore

import (
	"fmt"
	"strings"

	"crimsonledger/internal/domain"
)

// Key prefixes. keys.go is the only file that knows these formats.
const (
	prefixContact = "C#"
	prefixFix     = "F#"
	prefixWord    = "W#"
	prefixKill    = "K#"
	prefixBounty  = "B#"
	prefixBatch   = "BATCH#"
	prefixVersion = "V#"
	prefixKey     = "KEY#"
	prefixRuleset = "R#" // GSI1PK of contacts
	keyChart      = "CHART"
	keyAPIKeys    = "KEYS" // GSI1PK of API keys
	skMeta        = "META"
	skNone        = "-"
	sep           = "#"
	// timeDigits pads epoch milliseconds so string order equals numeric order (13 digits last until the year 2286).
	timeDigits = 13
	// mapDigits pads map ids the same way.
	mapDigits = 6
	// wordPrefixRunes is how many leading runes of a word pick its partition.
	wordPrefixRunes = 2
)

func pad(n int64, digits int) string {
	if n < 0 {
		n = 0
	}
	return fmt.Sprintf("%0*d", digits, n)
}

func contactKey(rs domain.Ruleset, guid string) (pk, sk string) {
	return prefixContact + string(rs) + sep + guid, skMeta
}

func contactIndexKey(rs domain.Ruleset, lastSeen int64) (pk, sk string) {
	return prefixRuleset + string(rs), pad(lastSeen, timeDigits)
}

func fixPK(rs domain.Ruleset, guid string) string { return prefixFix + string(rs) + sep + guid }

func fixSK(t int64, mapID int) string { return pad(t, timeDigits) + sep + pad(int64(mapID), mapDigits) }

// parseFixSK reads the map id back out of a fix sort key.
func parseFixSK(sk string) (mapID int, ok bool) {
	_, tail, found := strings.Cut(sk, sep)
	if !found {
		return 0, false
	}
	_, err := fmt.Sscanf(tail, "%d", &mapID)
	return mapID, err == nil
}

// wordKey neutralises "#" so a hostile name cannot forge another key; search text goes through it too.
func wordKey(w string) string { return strings.ReplaceAll(w, sep, "_") }

func wordPK(rs domain.Ruleset, word string) string {
	r := []rune(wordKey(word))
	return prefixWord + string(rs) + sep + string(r[:min(len(r), wordPrefixRunes)])
}

func wordSK(word, guid string) string { return wordKey(word) + sep + guid }

// guidOfWordSK returns the guid at the end of a word sort key (guids contain no "#").
func guidOfWordSK(sk string) string { return sk[strings.LastIndex(sk, sep)+1:] }

func killPK(rs domain.Ruleset, killer string) string { return prefixKill + string(rs) + sep + killer }

func killSK(t int64, victim string) string { return pad(t, timeDigits) + sep + victim }

func bountyPK(rs domain.Ruleset) string { return prefixBounty + string(rs) }

func chartSK(mapID int) string { return pad(int64(mapID), mapDigits) }

func batchPK(id string) string { return prefixBatch + id }

func versionPK(rs domain.Ruleset) string { return prefixVersion + string(rs) }

func apiKeyPK(id string) string { return prefixKey + id }

func apiKeyIndexSK(createdMillis int64, id string) string {
	return pad(createdMillis, timeDigits) + sep + id
}
