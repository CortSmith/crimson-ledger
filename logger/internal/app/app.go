// Package app is the scout's brain between the window and the pipeline: a small state machine plus the
// functions the page can call. It owns settings access, start/stop, and the status the page polls.
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/config"
	"crimsonledger/internal/logparse/tailer"
	"crimsonledger/internal/secret"
	"crimsonledger/internal/ui/settings"
	"crimsonledger/internal/ui/window"
	"crimsonledger/logger/internal/pipeline"
)

// State is what the scout shows on its status line.
type State string

// States, in the order a normal run goes through them.
const (
	StateIdle       State = "idle"       // settings incomplete or stopped
	StateConnecting State = "connecting" // checking the server
	StateWatching   State = "watching"   // running, no log file yet
	StateUploading  State = "uploading"  // reading the log and sending
	StatePaused     State = "paused"     // the user pressed Stop
	StateError      State = "error"      // reason says why
)

// maxActivity is how many activity lines the page shows (T13: last 50).
const maxActivity = 50

// stopWait is how long Stop waits for the final flush (the pipeline's own bound is 3 s).
const stopWait = 5 * time.Second

// Client is the part of apiclient.Client the app uses.
type Client interface {
	pipeline.Sender
	Check(ctx context.Context) error
}

// Deps are the app's seams to the outside world; tests replace them.
type Deps struct {
	Backend    settings.Backend
	LoadConfig func() (config.LoggerConfig, error)
	NewClient  func(apiclient.Options) (Client, error)
	NewSource  func(dir string, fromStart bool, onFile func(path string)) pipeline.Source
	OutboxDir  func() (string, error)
	Discover   func() string
	Pick       func() (string, error)
	Now        func() time.Time
	Rand       io.Reader
}

// Status is what the page polls. It never contains the key or raw log lines.
type Status struct {
	State       State    `json:"state"`
	Reason      string   `json:"reason"`
	Running     bool     `json:"running"`
	Lines       int64    `json:"lines"`
	Contacts    int64    `json:"contacts"`
	PingsSent   int64    `json:"pingsSent"`
	Queued      int      `json:"queued"`
	Dropped     int64    `json:"dropped"`
	LastSuccess int64    `json:"lastSuccess"`
	Activity    []string `json:"activity"`
}

// App is the scout controller. It is safe for concurrent use.
type App struct {
	d        Deps
	Settings *settings.Bindings

	startMu  sync.Mutex // serialises Start now that it runs off the UI thread
	mu       sync.Mutex
	state    State
	reason   string
	activity []string
	cancel   context.CancelFunc
	done     chan struct{}
	pipe     *pipeline.Pipeline
	queue    *pipeline.Queue
}

// New returns an App in the Idle state.
func New(d Deps) *App {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Rand == nil {
		d.Rand = rand.Reader
	}
	return &App{d: d, state: StateIdle, reason: msgNeedSettings, Settings: &settings.Bindings{B: d.Backend}}
}

// Register binds every page-callable function on w.
func (a *App) Register(w window.Window) error {
	if err := a.Settings.Register(w); err != nil {
		return err
	}
	for name, fn := range map[string]any{
		"getStatus": a.Status, "pickFolder": a.PickFolder, "defaultLogsDir": a.DefaultLogsDir,
	} {
		if err := w.Bind(name, fn); err != nil {
			return err
		}
	}
	// Start checks the server and Stop waits for a last upload: both can take seconds, so they run off the UI thread.
	for name, fn := range map[string]any{"start": a.Start, "stop": a.Stop} {
		if err := w.BindAsync(name, fn); err != nil {
			return err
		}
	}
	return nil
}

// DefaultLogsDir returns the first known WoW log folder that exists, or "".
func (a *App) DefaultLogsDir() (string, error) { return a.discover(), nil }

func (a *App) discover() string {
	if a.d.Discover != nil {
		return a.d.Discover()
	}
	home, _ := os.UserHomeDir()
	return DiscoverLogsDir(hostOS, home, isDir)
}

// PickFolder shows the OS folder dialog. An empty string means cancelled or unavailable.
func (a *App) PickFolder() (string, error) {
	if a.d.Pick == nil {
		return "", nil
	}
	p, err := a.d.Pick()
	if errors.Is(err, ErrNoPicker) {
		return "", nil
	}
	return p, err
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func (a *App) setState(s State, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state, a.reason = s, reason
}

// Log adds one activity line (time + message). Callers must never pass keys or raw log text.
func (a *App) Log(msg string) {
	line := a.d.Now().Format("15:04:05") + "  " + secret.Redact(msg)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activity = append(a.activity, line)
	if len(a.activity) > maxActivity {
		a.activity = append([]string(nil), a.activity[len(a.activity)-maxActivity:]...)
	}
}

// Status returns the current status for the page.
func (a *App) Status() (Status, error) {
	a.mu.Lock()
	st := Status{State: a.state, Reason: a.reason, Running: a.cancel != nil, Activity: append([]string{}, a.activity...)}
	p, q := a.pipe, a.queue
	a.mu.Unlock()
	if p != nil {
		st.Lines, st.Contacts, st.PingsSent = p.Stats.Lines.Load(), p.Stats.Contacts.Load(), p.Stats.PingsSent.Load()
		st.Dropped, st.LastSuccess = p.Stats.Dropped.Load(), p.Stats.LastSuccess.Load()
	}
	if q != nil {
		st.Queued = q.Len()
	}
	return st, nil
}

// Start validates the saved settings, checks the server and begins watching the log.
func (a *App) Start() (settings.Result, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return settings.Result{OK: true}, nil
	}
	a.mu.Unlock()
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return a.fail(StateIdle, secret.Redact(err.Error())), nil
	}
	dir, res := a.logsDir(cfg)
	if !res.OK {
		return res, nil
	}
	a.setState(StateConnecting, "Checking the server...")
	client, err := a.d.NewClient(apiclient.Options{BaseURL: cfg.ServerURL, APIKey: cfg.APIKey, CAFile: cfg.CAFile, UserAgent: a.d.Backend.UserAgent()})
	if err != nil {
		return a.fail(StateIdle, secret.Redact(err.Error())), nil
	}
	if err := a.checkServer(client); err != nil {
		return a.fail(StateError, msgKeyRejected), nil
	}
	return a.launch(cfg, dir, client)
}

func (a *App) fail(s State, msg string) settings.Result {
	a.setState(s, msg)
	return settings.Result{Message: msg}
}

func (a *App) logsDir(cfg config.LoggerConfig) (string, settings.Result) {
	dir := cfg.LogsDir
	if dir == "" {
		dir = a.discover()
	}
	if dir == "" {
		return "", a.fail(StateIdle, msgNeedLogsDir)
	}
	if !isDir(dir) {
		return "", a.fail(StateIdle, msgBadLogsDir)
	}
	return dir, settings.Result{OK: true}
}

// checkServer returns an error only for a rejected key; an unreachable server is not fatal (the pipeline retries).
func (a *App) checkServer(c Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.Check(ctx)
	if apiclient.IsAuth(err) {
		return err
	}
	if err != nil {
		a.Log(msgNoServerYet)
	}
	return nil
}

func (a *App) launch(cfg config.LoggerConfig, dir string, client Client) (settings.Result, error) {
	outbox, err := a.d.OutboxDir()
	if err != nil {
		return a.fail(StateIdle, "Could not prepare the upload folder."), nil
	}
	q, err := pipeline.OpenQueue(outbox, pipeline.MaxQueueBytes)
	if err != nil {
		return a.fail(StateIdle, "Could not prepare the upload folder."), nil
	}
	p := pipeline.New(pipeline.Options{
		Source: a.d.NewSource(dir, cfg.TailFromStart, func(path string) { a.Log("Reading " + filepath.Base(path)); a.noteFile() }),
		Sender: client, Queue: q, Interval: time.Duration(cfg.UploadIntervalMS) * time.Millisecond, Rand: a.d.Rand,
		Batch:   pipeline.BatcherOptions{Source: cfg.SourceName, Ruleset: cfg.Ruleset, MaxContacts: cfg.BatchMaxContacts},
		OnState: a.onPipelineState, OnActivity: a.Log,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.mu.Lock()
	a.cancel, a.done, a.pipe, a.queue = cancel, done, p, q
	a.mu.Unlock()
	a.setState(StateWatching, msgStarted)
	a.Log(msgStarted)
	if strings.HasPrefix(cfg.ServerURL, "http://") {
		a.Log(msgInsecureLAN)
	}
	go a.run(ctx, p, done)
	return settings.Result{OK: true}, nil
}

func (a *App) noteFile() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == StateWatching {
		a.state, a.reason = StateUploading, msgReading
	}
}

func (a *App) onPipelineState(s pipeline.State, reason string) {
	switch s {
	case pipeline.StateError:
		a.setState(StateError, reason)
		a.Log(reason)
	case pipeline.StateUploading:
		a.setState(StateUploading, msgReading)
	}
}

func (a *App) run(ctx context.Context, p *pipeline.Pipeline, done chan struct{}) {
	err := p.Run(ctx)
	if n := p.Stats.Dropped.Load(); n > 0 {
		a.Log(fmt.Sprintf(msgDroppedPattern, n))
	}
	a.mu.Lock()
	a.cancel = nil
	stopped := ctx.Err() != nil
	a.mu.Unlock()
	switch {
	case err != nil:
		a.setState(StateError, msgKeyRejected)
		a.Log(msgKeyRejected)
	case stopped:
		a.setState(StatePaused, msgStopped)
	}
	close(done)
}

// Stop ends the run after one last upload attempt (at most a few seconds).
func (a *App) Stop() (settings.Result, error) {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if cancel == nil {
		return settings.Result{OK: true}, nil
	}
	cancel()
	select {
	case <-done:
	case <-time.After(stopWait):
	}
	a.Log(msgStopped)
	return settings.Result{OK: true}, nil
}

// RealDeps wires the production implementations.
func RealDeps() Deps {
	return Deps{
		Backend:    settings.LoggerBackend{},
		LoadConfig: func() (config.LoggerConfig, error) { return config.Load("logger.json", config.LoggerDefaults) },
		NewClient: func(o apiclient.Options) (Client, error) {
			c, err := apiclient.New(o)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		NewSource: func(dir string, fromStart bool, onFile func(string)) pipeline.Source {
			return tailer.NewFollower("scout", tailer.Options{Dir: dir, FromStart: fromStart, OnFile: onFile})
		},
		OutboxDir: func() (string, error) {
			home, err := config.EnsureHome()
			if err != nil {
				return "", err
			}
			return filepath.Join(home, "data", "outbox"), nil
		},
		Pick: PickFolder,
	}
}

// AutoStart begins uploading in the background when the settings ask for it, and returns at once. Start checks the
// server first (up to 10 s when the address is dead), so it must never run before the window's event loop: the
// window would not appear until it returned. The page shows "Checking the server..." from the status poll meanwhile.
// The returned channel closes when the attempt is over (immediately when nothing was wanted).
func (a *App) AutoStart() <-chan struct{} {
	done := make(chan struct{})
	if !a.AutoStartWanted() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		_, _ = a.Start()
	}()
	return done
}

// AutoStartWanted reports whether logger.json asks for uploading to begin at launch and the key is set.
func (a *App) AutoStartWanted() bool {
	cfg, err := a.d.LoadConfig()
	return err == nil && cfg.AutoStartUpload && cfg.APIKey != ""
}
