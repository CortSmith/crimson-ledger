package testkit

import "strings"

// Canary is a well-formed ingest key (it matches secret.Redact's pattern) that must never appear in
// logs, outboxes, activity feeds, error text or any rendered output. Tests plant it as the user's
// key, run a scenario, then call AssertNoCanary on everything the scenario produced.
const Canary = "clk_i_c4n4ry00_CANARYCANARYCANARYCANARY0"

// AssertNoCanary fails the test for each output that contains the canary key or its secret part.
func AssertNoCanary(t T, outputs ...string) {
	t.Helper()
	const secretPart = "CANARYCANARYCANARYCANARY0"
	for i, out := range outputs {
		if strings.Contains(out, Canary) || strings.Contains(out, secretPart) {
			t.Errorf("canary key leaked in output #%d (%d bytes)", i, len(out))
		}
	}
}
