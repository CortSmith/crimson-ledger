package preflight

import (
	"bytes"
	"testing"
)

func TestCheck_RealHostIsConsistentWithReport(t *testing.T) {
	m := Check("Crimson Ledger")
	var b bytes.Buffer
	Report(&b, "Crimson Ledger", m, Family())
	if (len(m) == 0) != (b.Len() == 0) {
		t.Fatalf("missing=%v output=%q", m, b.String())
	}
}

func TestDialog_DoesNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("dialogs can pop up on desktop OSes")
	}
	// Linux Dialog is a no-op; other OSes are exercised on their CI runners.
	Dialog("x", nil)
}
