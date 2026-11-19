package dynamostore

import (
	"sort"
	"strings"
	"testing"

	"crimsonledger/internal/domain"
)

func TestFixSK_SortsNumerically(t *testing.T) {
	keys := []string{fixSK(1763380000000, 1411), fixSK(999999999999, 1411), fixSK(1763380000000, 12), fixSK(5, 2000000)}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	want := []string{fixSK(5, 2000000), fixSK(999999999999, 1411), fixSK(1763380000000, 12), fixSK(1763380000000, 1411)}
	for i := range want {
		if sorted[i] != want[i] {
			t.Fatalf("string order %v; want numeric order %v", sorted, want)
		}
	}
}

func TestFixSK_ParsesMapBack(t *testing.T) {
	if id, ok := parseFixSK(fixSK(1763380000000, 1411)); !ok || id != 1411 {
		t.Fatalf("parseFixSK = %d, %v; want 1411", id, ok)
	}
}

func TestWordKeys_HostileInputsCannotForgeOtherKeys(t *testing.T) {
	hostile := []string{"a#b", "##", "w#pvp#mc", "x#Player-1-ABCD"}
	for _, w := range hostile {
		pk, sk := wordPK(domain.RulesetPvP, w), wordSK(w, "Player-1329-0A1B")
		if strings.Count(pk, sep) != 2 {
			t.Errorf("wordPK(%q) = %q; want exactly the two separators of W#<ruleset>#", w, pk)
		}
		if strings.Count(sk, sep) != 1 || guidOfWordSK(sk) != "Player-1329-0A1B" {
			t.Errorf("wordSK(%q) = %q; want one separator and the guid recoverable", w, sk)
		}
	}
}

func TestWordPK_UsesFirstTwoRunes(t *testing.T) {
	tests := map[string]string{"mcgee": "W#pvp#mc", "a": "W#pvp#a", "ñandú": "W#pvp#ña"}
	for w, want := range tests {
		if got := wordPK(domain.RulesetPvP, w); got != want {
			t.Errorf("wordPK(%q) = %q; want %q", w, got, want)
		}
	}
}

func TestKeyFormats_AreDocumentedInReadme(t *testing.T) {
	readme := readFile(t, "../../../database/dynamodb/README.md")
	for _, prefix := range []string{prefixContact, prefixFix, prefixWord, prefixKill, prefixBounty, prefixBatch, prefixVersion, prefixKey, prefixRuleset, keyChart} {
		if !strings.Contains(readme, prefix) {
			t.Errorf("README.md does not document the key prefix %q", prefix)
		}
	}
}
