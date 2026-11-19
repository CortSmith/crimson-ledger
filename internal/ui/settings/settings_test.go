package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/config"
	"crimsonledger/internal/secret"
	"crimsonledger/internal/ui/window"
)

const goodKey = "clk_v_abcd1234_" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde" // 8 + 43 = valid shape
const otherKey = "clk_v_zzzz9999_" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde"

type fakePinger struct{ err error }

func (f fakePinger) Check(context.Context) error { return f.err }

func newBindings(t *testing.T) *Bindings {
	t.Helper()
	t.Setenv(config.HomeEnv, t.TempDir())
	b := &Bindings{B: ViewerBackend{}}
	if _, err := b.SaveSettings(Input{Form: Form{ServerURL: "https://api.example.com", Ruleset: "auto", Extra: map[string]string{"poll_interval_s": "15"}}, APIKey: ptr(goodKey)}); err != nil {
		t.Fatal(err)
	}
	return b
}

func ptr(s string) *string { return &s }

func formOf(t *testing.T, b *Bindings) Form {
	t.Helper()
	v, err := b.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return v.Form
}

func TestLoadSettings_NeverReturnsKey(t *testing.T) {
	b := newBindings(t)
	v, _ := b.LoadSettings()
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), goodKey) || !v.HasKey || v.KeyMask != secret.Mask() {
		t.Fatalf("view leaks or is wrong: %s", raw)
	}
}

func TestSaveSettings_KeepsKeyWhenPlaceholder(t *testing.T) {
	b := newBindings(t)
	for _, in := range []*string{nil, ptr(secret.Mask())} {
		if r, _ := b.SaveSettings(Input{Form: formOf(t, b), APIKey: in}); !r.OK {
			t.Fatalf("%+v", r)
		}
		if k, _ := b.RevealKey(); k != goodKey {
			t.Fatal("key changed")
		}
	}
}

func TestSaveSettings_ReplacesKey(t *testing.T) {
	b := newBindings(t)
	if r, _ := b.SaveSettings(Input{Form: formOf(t, b), APIKey: ptr(otherKey)}); !r.OK {
		t.Fatalf("%+v", r)
	}
	if k, _ := b.RevealKey(); k != otherKey {
		t.Fatal("key not replaced")
	}
}

func TestSaveSettings_RejectsBadKey(t *testing.T) {
	b := newBindings(t)
	r, err := b.SaveSettings(Input{Form: formOf(t, b), APIKey: ptr("clk_v_nope")})
	if err != nil || r.OK || r.Field != "api_key" || strings.Contains(r.Message, "clk_v_nope") {
		t.Fatalf("%+v %v", r, err)
	}
	if k, _ := b.RevealKey(); k != goodKey {
		t.Fatal("bad save changed the stored key")
	}
}

func TestSaveSettings_RejectsHTTPRemote(t *testing.T) {
	b := newBindings(t)
	f := formOf(t, b)
	f.ServerURL = "http://api.example.com"
	if r, _ := b.SaveSettings(Input{Form: f}); r.OK || r.Field != "server_url" {
		t.Fatalf("%+v", r)
	}
}

func TestRevealKey_ReturnsStored(t *testing.T) {
	if k, err := newBindings(t).RevealKey(); err != nil || k != goodKey {
		t.Fatalf("%q %v", k, err)
	}
}

func TestTestConnection_UsesApiclient(t *testing.T) {
	b := newBindings(t)
	var got apiclient.Options
	b.NewPinger = func(o apiclient.Options) (Pinger, error) { got = o; return fakePinger{}, nil }
	if r, _ := b.TestConnection(); !r.OK || got.BaseURL != "https://api.example.com" || got.UserAgent != "crimson-ledger/1.0" {
		t.Fatalf("%+v %+v", r, got)
	}
	b.NewPinger = func(apiclient.Options) (Pinger, error) { return fakePinger{errors.New("down " + goodKey)}, nil }
	r, _ := b.TestConnection()
	if r.OK || strings.Contains(r.Message, "AbCdEfGh") {
		t.Fatalf("failure not reported or key leaked: %+v", r)
	}
}

func TestLoggerBackend_RoundTrip(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	b := &Bindings{B: LoggerBackend{}}
	v, err := b.LoadSettings()
	if err != nil || v.Extra["source_name"] == "" || v.File != "~/.crimson-ledger/logger.json" {
		t.Fatalf("%+v %v", v, err)
	}
	v.Extra["source_name"] = "raid-pc"
	if r, _ := b.SaveSettings(Input{Form: v.Form}); !r.OK {
		t.Fatalf("%+v", r)
	}
	if v2, _ := b.LoadSettings(); v2.Extra["source_name"] != "raid-pc" {
		t.Fatal("not saved")
	}
}

func TestRegister_BindsFour(t *testing.T) {
	w := window.NewFake()
	if err := newBindings(t).Register(w); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"loadSettings", "saveSettings", "revealKey", "testConnection"} {
		if !w.Has(n) {
			t.Errorf("%s not bound", n)
		}
	}
}

func TestTestConnection_BadURLAndBadBind(t *testing.T) {
	b := newBindings(t)
	f := formOf(t, b)
	f.ServerURL = "https://api.example.com"
	b.NewPinger = func(apiclient.Options) (Pinger, error) { return nil, errors.New("server_url is bad") }
	if r, _ := b.TestConnection(); r.OK || r.Field != "server_url" {
		t.Fatalf("%+v", r)
	}
	if err := b.Register(badWindow{}); err == nil {
		t.Fatal("expected bind error")
	}
}

type badWindow struct{ window.Window }

func (badWindow) Bind(string, any) error { return errors.New("no") }

func TestSaveSettings_StoreFailure(t *testing.T) {
	t.Setenv(config.HomeEnv, "/proc/definitely/not/writable")
	if _, err := (&Bindings{B: ViewerBackend{}}).SaveSettings(Input{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := (&Bindings{B: ViewerBackend{}}).LoadSettings(); err == nil {
		t.Fatal("expected error")
	}
	if _, err := (&Bindings{B: ViewerBackend{}}).RevealKey(); err == nil {
		t.Fatal("expected error")
	}
	if _, err := (&Bindings{B: ViewerBackend{}}).TestConnection(); err == nil {
		t.Fatal("expected error")
	}
}

func TestBackends_Names(t *testing.T) {
	v, l := ViewerBackend{}, LoggerBackend{}
	if v.UserAgent() == l.UserAgent() || v.File() == l.File() {
		t.Fatal("backends must be distinguishable")
	}
}
