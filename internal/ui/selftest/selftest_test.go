package selftest

import (
	"bytes"
	"strings"
	"testing"

	"crimsonledger/internal/ui/window"
)

func TestWanted(t *testing.T) {
	if !Wanted([]string{"--selftest"}) || Wanted([]string{"--other"}) || Wanted(nil) {
		t.Fatal("Wanted must be true only for --selftest")
	}
}

func TestInject_PutsNoncedScriptBeforeBodyEnd(t *testing.T) {
	got := Inject("<html><body>x</body></html>", "abc")
	if !strings.Contains(got, `<script nonce="abc">`) || !strings.HasSuffix(got, "</script></body></html>") {
		t.Fatalf("bad page: %s", got)
	}
	if !strings.Contains(got, "loadSettings") || !strings.Contains(got, DoneBinding) {
		t.Fatal("script must call loadSettings then report through the done binding")
	}
}

func TestBind_ReportsOKAndTerminates(t *testing.T) {
	w, out := window.NewFake(), &bytes.Buffer{}
	code := new(int)
	if err := Bind(w, out, func(c int) { *code = c }); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Call(DoneBinding, true); err != nil {
		t.Fatal(err)
	}
	if out.String() != "SELFTEST OK\n" || *code != 0 || !w.Terminated {
		t.Fatalf("out=%q code=%d terminated=%v", out.String(), *code, w.Terminated)
	}
}

func TestBind_ReportsFailure(t *testing.T) {
	w, out := window.NewFake(), &bytes.Buffer{}
	code := new(int)
	_ = Bind(w, out, func(c int) { *code = c })
	_, _ = w.Call(DoneBinding, false)
	if !strings.HasPrefix(out.String(), "SELFTEST FAILED") || *code != 1 {
		t.Fatalf("out=%q code=%d", out.String(), *code)
	}
}
