package testkit_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"crimsonledger/internal/testkit"
)

func TestNewTestAPI_IngestThenSweepRoundTrips(t *testing.T) {
	clock := testkit.NewFakeClock()
	srv, keys := testkit.NewTestAPI(t, nil, clock)
	ping := testkit.APing(testkit.WithContacts(testkit.AContact(testkit.WithName("Roundtrip"), testkit.WithFixes(testkit.AFix()))))
	body, _ := json.Marshal(ping)

	do := func(method, path, token string, b []byte) (int, []byte) {
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, out
	}
	if code, _ := do("POST", "/v1/pings", keys.Ingest, body); code/100 != 2 {
		t.Fatalf("ingest status %d", code)
	}
	if code, _ := do("POST", "/v1/pings", keys.View, body); code != 403 && code != 401 {
		t.Fatalf("view key may not ingest, got %d", code)
	}
	code, raw := do("GET", "/v1/sweep?ruleset=pvp", keys.View, nil)
	var out struct {
		Contacts []struct{ Name string } `json:"contacts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Contacts) != 1 || out.Contacts[0].Name != "Roundtrip" {
		t.Fatalf("sweep: status %d, %+v, %v", code, out, err)
	}
}
