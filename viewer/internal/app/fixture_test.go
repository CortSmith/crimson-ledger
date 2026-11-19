package app

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"crimsonledger/internal/config"
)

// The page tests (viewer/web/ui.test.mjs) boot the page with this exact answer of getAppearance, so a change to the
// Go side that the page does not know about fails here. Regenerate with UPDATE_FIXTURES=1 go test ./viewer/internal/app.
const appearanceFixture = "../../web/testdata/appearance.json"

func TestAppearanceFixtureMatchesGo(t *testing.T) {
	got, err := json.MarshalIndent(appearanceView(config.ViewerDefaults()), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("UPDATE_FIXTURES") != "" {
		if err := os.WriteFile(appearanceFixture, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(appearanceFixture)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("viewer/web/testdata/appearance.json is stale; run UPDATE_FIXTURES=1 go test ./viewer/internal/app")
	}
}
