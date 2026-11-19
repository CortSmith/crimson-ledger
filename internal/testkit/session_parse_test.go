package testkit_test

import (
	"testing"
	"time"

	"crimsonledger/internal/logparse/parser"
	"crimsonledger/internal/testkit"
)

func TestGenerateSession_LinesParseToTheExpectedPlayers(t *testing.T) {
	s := testkit.GenerateSession(7, testkit.SessionOptions{Players: 4, Minutes: 2})
	seen := map[string]int{}
	for i, line := range s.Lines {
		ev, err := parser.ParseLine(line, time.UTC)
		if err != nil {
			t.Fatalf("line %d: %v\n%s", i, err, line)
		}
		if parser.ReactionOf(ev.Src.Flags) != parser.ReactionHostile {
			t.Fatalf("line %d: source not hostile", i)
		}
		seen[ev.Src.GUID]++
	}
	if len(seen) != 4 {
		t.Fatalf("parsed %d distinct players, want 4", len(seen))
	}
	for _, p := range s.Players {
		if seen[p.GUID] != 8 { // 4 casts per minute x 2 minutes
			t.Errorf("%s parsed %d lines, want 8", p.Name, seen[p.GUID])
		}
	}
}
