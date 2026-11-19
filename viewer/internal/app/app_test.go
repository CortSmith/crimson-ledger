package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/config"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/ui/settings"
	"crimsonledger/internal/ui/window"
)

const canaryKey = "clk_v_abcd1234_CANARYCANARYCANARYCANARY"
const guidA = "Player-1329-0A1B2C3D"

type fakeClient struct {
	mu       sync.Mutex
	calls    []string
	err      error
	checkErr error
}

func (f *fakeClient) record(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakeClient) setErr(e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = e
}

func (f *fakeClient) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeClient) Sweep(context.Context, apiclient.SweepParams) (apiclient.SweepResult, error) {
	err := f.record("sweep")
	return apiclient.SweepResult{ServerTime: 1, Contacts: []domain.SweepContact{{GUID: guidA, Name: "</script><b>"}}}, err
}
func (f *fakeClient) Check(context.Context) error { return f.checkErr }
func (f *fakeClient) Players(_ context.Context, _ domain.Ruleset, q string, _ int) ([]apiclient.PlayerSummary, error) {
	return nil, f.record("players:" + q)
}
func (f *fakeClient) Trail(_ context.Context, _ domain.Ruleset, g string, _ apiclient.TrailParams) (apiclient.Trail, error) {
	return apiclient.Trail{GUID: g}, f.record("trail:" + g)
}
func (f *fakeClient) Bounty(context.Context, domain.Ruleset, int) ([]domain.KillTotal, error) {
	return nil, f.record("bounty")
}
func (f *fakeClient) Charts(context.Context) ([]domain.Chart, error) {
	return nil, f.record("charts")
}

type fakeBackend struct{}

func (fakeBackend) Load() (settings.Form, string, error) {
	return settings.Form{ServerURL: "https://x.example", Extra: map[string]string{}}, canaryKey, nil
}
func (fakeBackend) Save(settings.Form, string) error { return nil }
func (fakeBackend) File() string                     { return "~/.crimson-ledger/viewer.json" }
func (fakeBackend) UserAgent() string                { return "test" }

func newApp(t *testing.T, c *fakeClient, cfg config.ViewerConfig, clip func(string) error) (*App, *window.FakeWindow, *config.ViewerConfig) {
	t.Helper()
	saved := cfg
	a := New(Deps{
		Backend:    fakeBackend{},
		LoadConfig: func() (config.ViewerConfig, error) { return saved, nil },
		SaveConfig: func(v config.ViewerConfig) error { saved = v; return nil },
		NewClient:  func(apiclient.Options) (Client, error) { return c, nil },
		Clipboard:  clip,
	})
	w := window.NewFake()
	if err := a.Register(w); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, w, &saved
}

func cfgWithKey() config.ViewerConfig {
	c := config.ViewerDefaults()
	c.APIKey = canaryKey
	return c
}

func TestApp_Register_BindsEveryFunctionThePageCalls(t *testing.T) {
	_, w, _ := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	for _, n := range []string{"loadSettings", "saveSettings", "revealKey", "testConnection", "connect", "setVisible", "setRuleset",
		"getCharts", "getMap", "search", "getTrail", "getBounty", "copyTarget", "getView", "saveView", "testViewer",
		"getAppearance", "saveAppearance", "resetAppearance"} {
		if !w.Has(n) {
			t.Errorf("%s not bound", n)
		}
	}
}

func TestApp_Connect_WithoutKeyAsksForSettings(t *testing.T) {
	a, _, _ := newApp(t, &fakeClient{}, config.ViewerDefaults(), nil)
	r, _ := a.Connect()
	if r.OK || r.Code != "not_configured" {
		t.Fatalf("%+v", r)
	}
}

func TestApp_Trail_RejectsBadGUIDBeforeAnyCall(t *testing.T) {
	c := &fakeClient{}
	a, _, _ := newApp(t, c, cfgWithKey(), nil)
	_, _ = a.Connect()
	for _, g := range []string{"", "../../etc", "Player-1-xyz", guidA + "?x=1"} {
		r, _ := a.Trail(g, 0)
		if r.OK || r.Code != "invalid_request" {
			t.Errorf("%q accepted", g)
		}
	}
	for _, call := range c.seen() {
		if strings.HasPrefix(call, "trail:") {
			t.Fatalf("client was called: %v", c.seen())
		}
	}
}

func TestApp_Search_ShortQueryNeverCallsServer(t *testing.T) {
	c := &fakeClient{}
	a, _, _ := newApp(t, c, cfgWithKey(), nil)
	_, _ = a.Connect()
	r, _ := a.Search(" a ")
	if !r.OK || slices.ContainsFunc(c.seen(), func(s string) bool { return strings.HasPrefix(s, "players:") }) {
		t.Fatalf("%+v %v", r, c.seen())
	}
}

func TestApp_Errors_CarryCodeAndRequestIDButNeverTheKey(t *testing.T) {
	c := &fakeClient{err: &apiclient.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "slow down " + canaryKey, RequestID: "a1b2"}}
	a, _, _ := newApp(t, c, cfgWithKey(), nil)
	_, _ = a.Connect()
	r, _ := a.Bounty()
	if r.Code != "rate_limited" || r.RequestID != "a1b2" || strings.Contains(r.Message, "CANARY") {
		t.Fatalf("%+v", r.Result)
	}
}

func TestApp_CopyTarget_OnlyPlainNames(t *testing.T) {
	var got string
	a, _, _ := newApp(t, &fakeClient{}, cfgWithKey(), func(s string) error { got = s; return nil })
	for _, bad := range []string{"", "Bob\n/run x", "a;b", "<img>", strings.Repeat("a", 30), "1abc"} {
		if r, _ := a.CopyTarget(bad); r.OK {
			t.Errorf("%q accepted", bad)
		}
	}
	if r, _ := a.CopyTarget("Mc Cheeki"); !r.OK || got != "/target Mc Cheeki" {
		t.Fatalf("%q", got)
	}
	if r, _ := a.CopyTarget("Ælfred"); !r.OK {
		t.Fatal("non-ASCII letters must work")
	}
}

func TestApp_CopyTarget_NoClipboardReturnsTextForCtrlC(t *testing.T) {
	a, _, _ := newApp(t, &fakeClient{}, cfgWithKey(), func(string) error { return errors.New("none") })
	r, _ := a.CopyTarget("Bob")
	if r.OK || r.Code != "no_clipboard" || r.Message != "/target Bob" {
		t.Fatalf("%+v", r)
	}
}

func TestApp_Publish_EscapesApiTextInsideScript(t *testing.T) {
	a, w, _ := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	_, _ = a.Connect()
	waitFor(t, func() bool { return len(w.EvalCalls()) > 0 })
	js := w.EvalCalls()[0]
	if !strings.HasPrefix(js, "window.__app.onUpdate(") || strings.Contains(js, "</script>") || strings.Contains(js, "<b>") {
		t.Fatalf("unsafe eval: %s", js)
	}
}

func TestApp_SetRuleset_RejectsUnknownAndSavesKnown(t *testing.T) {
	a, _, saved := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	if r, _ := a.SetRuleset("all"); r.OK {
		t.Fatal("accepted")
	}
	if r, _ := a.SetRuleset("pvp"); !r.OK || saved.Ruleset != "pvp" {
		t.Fatalf("saved %q", saved.Ruleset)
	}
}

func TestApp_View_AutoRulesetBecomesNormal(t *testing.T) {
	a, _, _ := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	v, _ := a.View()
	if v.Ruleset != "normal" || !v.Configured {
		t.Fatalf("%+v", v)
	}
}

func TestApp_TestViewer_ReportsUnreachableAndRejected(t *testing.T) {
	c := &fakeClient{checkErr: errors.New("dial")}
	a, _, _ := newApp(t, c, cfgWithKey(), nil)
	if r, _ := a.TestViewer(); r.Code != "unreachable" {
		t.Fatalf("%+v", r)
	}
	c.checkErr = nil
	c.setErr(&apiclient.APIError{Status: 401, Code: "unauthorized"})
	if r, _ := a.TestViewer(); r.Code != "key_rejected" {
		t.Fatalf("%+v", r)
	}
	c.setErr(nil)
	if r, _ := a.TestViewer(); !r.OK {
		t.Fatalf("%+v", r)
	}
}

func TestNoPersistedData_ConfigDirHoldsOnlyViewerJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CRIMSON_LEDGER_HOME", home)
	d := RealDeps(nil, nil)
	d.NewClient = func(apiclient.Options) (Client, error) { return &fakeClient{}, nil }
	a := New(d)
	w := window.NewFake()
	if err := a.Register(w); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Connect()
	_, _ = a.Search("mc")
	_, _ = a.Bounty()
	a.Close()
	_ = filepath.WalkDir(home, func(p string, e os.DirEntry, _ error) error {
		if !e.IsDir() && e.Name() != "viewer.json" {
			t.Errorf("unexpected file %s", p)
		}
		return nil
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestApp_ReadBindings_ReturnClientData(t *testing.T) {
	c := &fakeClient{}
	a, _, _ := newApp(t, c, cfgWithKey(), nil)
	_, _ = a.Connect()
	if r, _ := a.Charts(); !r.OK {
		t.Fatalf("charts %+v", r)
	}
	if r, _ := a.Search("mc"); !r.OK {
		t.Fatalf("search %+v", r)
	}
	if r, _ := a.Bounty(); !r.OK {
		t.Fatalf("bounty %+v", r)
	}
	if r, _ := a.Trail(guidA, 1411); !r.OK || r.Trail.GUID != guidA {
		t.Fatalf("trail %+v", r)
	}
	if v, _ := a.SetVisible(false); v {
		t.Fatal("visible")
	}
}

func TestApp_NotConnected_ReadsReportAnError(t *testing.T) {
	a, _, _ := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	if r, _ := a.Charts(); r.OK {
		t.Fatal("charts ok")
	}
	if r, _ := a.Bounty(); r.OK {
		t.Fatal("bounty ok")
	}
	if r, _ := a.Search("mc"); r.OK {
		t.Fatal("search ok")
	}
	if r, _ := a.Trail(guidA, 0); r.OK {
		t.Fatal("trail ok")
	}
}

func TestApp_SaveView_PersistsPrefsAndMapWithoutStore(t *testing.T) {
	a, _, saved := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	if r, _ := a.SaveView(ViewPrefs{Orientation: "alt", SelectedMap: 7, ShowHistory: true}); !r.OK || saved.Orientation != "alt" || saved.SelectedMap != 7 || !saved.ShowHistory {
		t.Fatalf("%+v", saved)
	}
	if r, _ := a.Map("Durotar"); r.OK {
		t.Fatal("map without store")
	}
}
