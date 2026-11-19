package testkit_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/config"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/secret"
	"crimsonledger/internal/testkit"
)

func TestFakeClock_AdvanceMovesNowAndFiresAfter(t *testing.T) {
	c := testkit.NewFakeClock()
	start := c.Now()
	ch := c.After(time.Minute)
	select {
	case <-ch:
		t.Fatal("After fired before the clock advanced")
	default:
	}
	c.Advance(30 * time.Second)
	select {
	case <-ch:
		t.Fatal("After fired early")
	default:
	}
	c.Advance(31 * time.Second)
	select {
	case got := <-ch:
		if !got.After(start) {
			t.Fatalf("After delivered %v, want a later time than %v", got, start)
		}
	default:
		t.Fatal("After did not fire")
	}
	if c.Now().Sub(start) != 61*time.Second {
		t.Fatalf("Now moved %v, want 61s", c.Now().Sub(start))
	}
}

func TestTempHome_SetsEnvToPrivateDir(t *testing.T) {
	dir := testkit.TempHome(t)
	if got := os.Getenv(config.HomeEnv); got != dir {
		t.Fatalf("%s = %q, want %q", config.HomeEnv, got, dir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v, want 0700", fi.Mode().Perm())
	}
}

func TestAssertNoCanary_FailsWhenPresent(t *testing.T) {
	var rec fakeT
	testkit.AssertNoCanary(&rec, "clean", "has "+testkit.Canary+" inside")
	if !rec.failed {
		t.Fatal("AssertNoCanary passed although the canary was present")
	}
	rec = fakeT{}
	testkit.AssertNoCanary(&rec, "clean", "also clean")
	if rec.failed {
		t.Fatal("AssertNoCanary failed on clean output")
	}
}

func TestCanary_IsRecognisedAsAKeyByRedact(t *testing.T) {
	if secret.Redact(testkit.Canary) == testkit.Canary {
		t.Fatal("Redact does not recognise the canary as a key; leak tests would be misleading")
	}
}

func TestGolden_UpdateFlagWritesAndCompares(t *testing.T) {
	testkit.GoldenWithUpdate(t, "roundtrip", []byte("v1\n"), true)
	testkit.GoldenWithUpdate(t, "roundtrip", []byte("v1\n"), false) // matches
	var rec fakeT
	testkit.GoldenWithUpdate(&rec, "roundtrip", []byte("v2\n"), false)
	if !rec.failed {
		t.Fatal("Golden accepted different content")
	}
	if !strings.Contains(rec.msg, "-update") {
		t.Fatalf("failure message %q does not say how to update", rec.msg)
	}
}

func TestAPing_IsValidByDefault(t *testing.T) {
	p := testkit.APing(testkit.WithContacts(testkit.AContact(testkit.WithFixes(testkit.AFix()))))
	if err := domain.ValidatePing(p, testkit.Epoch); err != nil {
		t.Fatalf("default ping invalid: %v", err)
	}
}

func TestAPing_EachOptionBreaksExactlyItsField(t *testing.T) {
	tests := []struct {
		name string
		opt  testkit.PingOption
		path string
	}{
		{"batch id", testkit.WithBatchID("nope"), "batchId"},
		{"source", testkit.WithSource("bad source!"), "source"},
		{"ruleset", testkit.WithRuleset("hardcore"), "ruleset"},
		{"sentAt", testkit.WithSentAt(testkit.Epoch.Add(-time.Hour).UnixMilli()), "sentAt"},
		{"contact guid", testkit.WithContacts(testkit.AContact(testkit.WithGUID("x"))), "contacts[0].guid"},
		{"contact reaction", testkit.WithContacts(testkit.AContact(testkit.WithReaction("evil"))), "contacts[0].reaction"},
		{"fix x", testkit.WithContacts(testkit.AContact(testkit.WithFixes(testkit.AFix(testkit.WithX(101))))), "contacts[0].fixes[0].x"},
		{"fix map", testkit.WithContacts(testkit.AContact(testkit.WithFixes(testkit.AFix(testkit.WithMapID(0))))), "contacts[0].fixes[0].mapId"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.ValidatePing(testkit.APing(tc.opt), testkit.Epoch)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("got %v, want a ValidationError", err)
			}
			if ve.Path != tc.path {
				t.Fatalf("path %q, want %q", ve.Path, tc.path)
			}
		})
	}
}

func TestAPing_BatchIDsAreUniqueUUIDv4(t *testing.T) {
	a, b := testkit.APing(), testkit.APing()
	if a.BatchID == b.BatchID || !domain.IsUUIDv4(a.BatchID) {
		t.Fatalf("batch ids %q %q", a.BatchID, b.BatchID)
	}
}

func TestGenerateSession_Deterministic(t *testing.T) {
	a := testkit.GenerateSession(42, testkit.SessionOptions{Players: 5, Minutes: 3})
	b := testkit.GenerateSession(42, testkit.SessionOptions{Players: 5, Minutes: 3})
	if strings.Join(a.Lines, "\n") != strings.Join(b.Lines, "\n") {
		t.Fatal("same seed produced different lines")
	}
	c := testkit.GenerateSession(43, testkit.SessionOptions{Players: 5, Minutes: 3})
	if strings.Join(a.Lines, "\n") == strings.Join(c.Lines, "\n") {
		t.Fatal("different seeds produced identical lines")
	}
	if len(a.Pings) == 0 || len(a.Lines) == 0 {
		t.Fatal("empty session")
	}
	for i, p := range a.Pings {
		if err := domain.ValidatePing(p, testkit.Epoch.Add(time.Duration(3)*time.Minute)); err != nil {
			t.Fatalf("ping %d invalid: %v", i, err)
		}
	}
	if len(a.Players) != 5 {
		t.Fatalf("%d players, want 5", len(a.Players))
	}
}

type fakeT struct {
	failed bool
	msg    string
}

func (f *fakeT) Helper()                        {}
func (f *fakeT) Errorf(format string, a ...any) { f.failed = true; f.msg += sprintf(format, a...) }
func (f *fakeT) Fatalf(format string, a ...any) { f.failed = true; f.msg += sprintf(format, a...) }
func (f *fakeT) Name() string                   { return "fake" }

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
