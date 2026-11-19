package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/config"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/ui/settings"
	"crimsonledger/internal/ui/window"
	"crimsonledger/logger/internal/pipeline"
)

const canaryKey = "clk_i_abcd1234_CANARYCANARYCANARYCANARY"

type idleSource struct{}

func (idleSource) Run(ctx context.Context, _ func(string)) error { <-ctx.Done(); return ctx.Err() }

type fakeClient struct {
	checkErr error
	posts    int
}

func (f *fakeClient) Check(context.Context) error { return f.checkErr }
func (f *fakeClient) PostPing(context.Context, domain.Ping) (apiclient.PingResult, error) {
	f.posts++
	return apiclient.PingResult{Accepted: true}, nil
}

type fakeBackend struct{ saved settings.Form }

func (b *fakeBackend) Load() (settings.Form, string, error) {
	return settings.Form{ServerURL: "https://x.example", Extra: map[string]string{}}, canaryKey, nil
}
func (b *fakeBackend) Save(f settings.Form, _ string) error { b.saved = f; return nil }
func (b *fakeBackend) File() string                         { return "~/.crimson-ledger/logger.json" }
func (b *fakeBackend) UserAgent() string                    { return "test" }

func newApp(t *testing.T, cfg config.LoggerConfig, client *fakeClient) *App {
	t.Helper()
	return New(Deps{
		Backend:    &fakeBackend{},
		LoadConfig: func() (config.LoggerConfig, error) { return cfg, nil },
		NewClient:  func(apiclient.Options) (Client, error) { return client, nil },
		NewSource:  func(string, bool, func(string)) pipeline.Source { return idleSource{} },
		OutboxDir:  func() (string, error) { return filepath.Join(t.TempDir(), "outbox"), nil },
		Discover:   func() string { return "" },
		Now:        func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
	})
}

func goodConfig(t *testing.T) config.LoggerConfig {
	c := config.LoggerDefaults()
	c.ServerURL, c.APIKey, c.LogsDir = "https://x.example", canaryKey, t.TempDir()
	return c
}

func TestApp_StartStop_StateMachine(t *testing.T) {
	a := newApp(t, goodConfig(t), &fakeClient{})
	if st, _ := a.Status(); st.State != StateIdle || st.Running {
		t.Fatalf("initial = %+v", st)
	}
	if res, _ := a.Start(); !res.OK {
		t.Fatalf("start: %+v", res)
	}
	if st, _ := a.Status(); st.State != StateWatching || !st.Running {
		t.Fatalf("after start = %+v", st)
	}
	if res, _ := a.Stop(); !res.OK {
		t.Fatalf("stop: %+v", res)
	}
	if st, _ := a.Status(); st.State != StatePaused || st.Running {
		t.Fatalf("after stop = %+v", st)
	}
}

func TestApp_Start_Refusals(t *testing.T) {
	tests := []struct {
		name  string
		mod   func(c *config.LoggerConfig)
		check error
		want  State
		text  string
	}{
		{"no logs folder anywhere", func(c *config.LoggerConfig) { c.LogsDir = "" }, nil, StateIdle, "Choose the folder"},
		{"folder missing", func(c *config.LoggerConfig) { c.LogsDir = "/nonexistent/zzz" }, nil, StateIdle, "not a folder"},
		{"key rejected at connect", func(*config.LoggerConfig) {}, &apiclient.APIError{Status: http.StatusUnauthorized, Code: "unauthorized"}, StateError, "API key rejected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := goodConfig(t)
			tc.mod(&cfg)
			a := newApp(t, cfg, &fakeClient{checkErr: tc.check})
			res, _ := a.Start()
			st, _ := a.Status()
			if res.OK || st.State != tc.want || st.Running || !strings.Contains(res.Message, tc.text) {
				t.Errorf("res=%+v st=%+v", res, st)
			}
		})
	}
}

func TestApp_Start_ServerDownIsNotFatal(t *testing.T) {
	a := newApp(t, goodConfig(t), &fakeClient{checkErr: fmt.Errorf("connection refused")})
	if res, _ := a.Start(); !res.OK {
		t.Fatalf("start refused: %+v", res)
	}
	defer func() { _, _ = a.Stop() }()
	if st, _ := a.Status(); !strings.Contains(strings.Join(st.Activity, "\n"), "keep trying") {
		t.Errorf("activity = %v", st.Activity)
	}
}

func TestApp_ActivityIsBoundedAndNeverHoldsTheKey(t *testing.T) {
	a := newApp(t, goodConfig(t), &fakeClient{})
	for i := 0; i < 80; i++ {
		a.Log(fmt.Sprintf("line %d key %s", i, canaryKey))
	}
	st, _ := a.Status()
	if len(st.Activity) != maxActivity {
		t.Errorf("activity = %d lines, want %d", len(st.Activity), maxActivity)
	}
	if strings.Contains(strings.Join(st.Activity, "\n"), canaryKey) {
		t.Error("activity contains the key")
	}
}

func TestApp_Bindings(t *testing.T) {
	a := newApp(t, goodConfig(t), &fakeClient{})
	w := window.NewFake()
	if err := a.Register(w); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"loadSettings", "saveSettings", "revealKey", "testConnection", "start", "stop", "getStatus", "pickFolder", "defaultLogsDir"} {
		if !w.Has(n) {
			t.Errorf("%s is not bound", n)
		}
	}
	v, err := w.Call("loadSettings")
	if err != nil {
		t.Fatal(err)
	}
	if view := v.(settings.View); !view.HasKey || strings.Contains(fmt.Sprintf("%+v", view), canaryKey) {
		t.Errorf("settings view leaks the key or lacks the flag: %+v", view)
	}
	if k, _ := w.Call("revealKey"); k != canaryKey {
		t.Errorf("revealKey = %v", k)
	}
	st, _ := w.Call("getStatus")
	if strings.Contains(fmt.Sprintf("%+v", st), canaryKey) {
		t.Error("status leaks the key")
	}
}

func TestDiscoverLogsDir(t *testing.T) {
	tests := []struct {
		goos, home string
		exists     []string
		want       string
	}{
		{"linux", "/home/u", []string{"/home/u/Games/World of Warcraft/_forever_/Logs"}, "/home/u/Games/World of Warcraft/_forever_/Logs"},
		{"darwin", "/Users/u", []string{"/Applications/World of Warcraft/_forever_/Logs"}, "/Applications/World of Warcraft/_forever_/Logs"},
		{"windows", `C:\Users\u`, []string{`C:\Program Files (x86)\World of Warcraft\_forever_\Logs`}, `C:\Program Files (x86)\World of Warcraft\_forever_\Logs`},
		{"linux", "/home/u", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.goos+"/"+tc.want, func(t *testing.T) {
			got := DiscoverLogsDir(tc.goos, tc.home, func(p string) bool {
				for _, e := range tc.exists {
					if e == filepath.FromSlash(p) || e == p {
						return true
					}
				}
				return false
			})
			if got != tc.want && filepath.ToSlash(got) != filepath.ToSlash(tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// slowClient holds Check until released, like a dead server address that only gives up after a timeout.
type slowClient struct{ release chan struct{} }

func (c *slowClient) Check(context.Context) error { <-c.release; return nil }
func (c *slowClient) PostPing(context.Context, domain.Ping) (apiclient.PingResult, error) {
	return apiclient.PingResult{Accepted: true}, nil
}

func TestApp_AutoStart_NeverBlocksTheCaller(t *testing.T) {
	slow := &slowClient{release: make(chan struct{})}
	a := New(Deps{
		Backend:    &fakeBackend{},
		LoadConfig: func() (config.LoggerConfig, error) { return goodConfig(t), nil },
		NewClient:  func(apiclient.Options) (Client, error) { return slow, nil },
		NewSource:  func(string, bool, func(string)) pipeline.Source { return idleSource{} },
		OutboxDir:  func() (string, error) { return filepath.Join(t.TempDir(), "outbox"), nil },
		Discover:   func() string { return "" },
		Now:        time.Now,
	})
	t.Cleanup(func() { _, _ = a.Stop() })
	returned := make(chan (<-chan struct{}))
	go func() { returned <- a.AutoStart() }()
	var done <-chan struct{}
	select {
	case done = <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoStart waited for the server check: the window would not appear until it finished")
	}
	waitState(t, a, StateConnecting) // the page can already read the status while the check runs
	close(slow.release)
	<-done
	if st, _ := a.Status(); st.State != StateWatching || !st.Running {
		t.Fatalf("after the check = %+v", st)
	}
}

func TestApp_AutoStart_NothingWantedReturnsAClosedChannel(t *testing.T) {
	cfg := goodConfig(t)
	cfg.AutoStartUpload = false
	a := newApp(t, cfg, &fakeClient{})
	select {
	case <-a.AutoStart():
	case <-time.After(time.Second):
		t.Fatal("channel not closed")
	}
	if st, _ := a.Status(); st.Running {
		t.Fatal("started although auto_start_upload is off")
	}
}

func waitState(t *testing.T, a *App, want State) {
	t.Helper()
	for range 200 {
		if st, _ := a.Status(); st.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := a.Status()
	t.Fatalf("state %q never reached, last %q", want, st.State)
}
