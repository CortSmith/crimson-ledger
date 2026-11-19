// Package app holds the viewer's page-callable functions. It is bindings only: polling lives in internal/data,
// images in internal/maps, settings in internal/ui/settings. Every datum comes from the API through Client.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/config"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/secret"
	"crimsonledger/internal/ui/settings"
	"crimsonledger/internal/ui/window"
	"crimsonledger/viewer/internal/data"
	"crimsonledger/viewer/internal/maps"
)

// Client is the part of apiclient.Client the viewer uses.
type Client interface {
	data.Sweeper
	Check(ctx context.Context) error
	Players(ctx context.Context, rs domain.Ruleset, text string, limit int) ([]apiclient.PlayerSummary, error)
	Trail(ctx context.Context, rs domain.Ruleset, guid string, f apiclient.TrailParams) (apiclient.Trail, error)
	Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error)
	Charts(ctx context.Context) ([]domain.Chart, error)
}

// Deps are the app's seams to the outside world; tests replace them.
type Deps struct {
	Backend    settings.Backend
	LoadConfig func() (config.ViewerConfig, error)
	SaveConfig func(config.ViewerConfig) error
	NewClient  func(apiclient.Options) (Client, error)
	Maps       *maps.Store
	Clipboard  func(text string) error
}

// Request limits (API maxima from PLAN 5.1).
const (
	searchMinRunes = 2
	searchLimit    = 50
	trailLimit     = 2000
	bountyLimit    = 100
	callTimeout    = 15 * time.Second
	sweepMaxAgeSec = 900
	defaultRuleset = domain.Ruleset("normal") // the API needs a concrete ruleset; "auto" in viewer.json means this
)

// targetNameRE is what may reach the clipboard: letters (any script), spaces, apostrophes and hyphens.
var targetNameRE = regexp.MustCompile(`^[\p{L}][\p{L} '\-]{0,23}$`)

// guidRE mirrors the API's GUID rule so a bad value never leaves the machine.
var guidRE = regexp.MustCompile(`^(Player|Creature|Pet|Vehicle)-\d+-[0-9A-F]{4,16}$`)

// App is the viewer controller. It is safe for concurrent use.
type App struct {
	d        Deps
	Settings *settings.Bindings

	mu     sync.Mutex
	client Client
	cfg    config.ViewerConfig
	win    window.Window
	poller *data.Poller
	cancel context.CancelFunc
}

// New returns an App that has not connected yet.
func New(d Deps) *App {
	return &App{d: d, Settings: &settings.Bindings{B: d.Backend}}
}

// Register binds every page-callable function on w.
func (a *App) Register(w window.Window) error {
	a.win = w
	if err := a.Settings.Register(w); err != nil {
		return err
	}
	for name, fn := range map[string]any{
		"connect": a.Connect, "setVisible": a.SetVisible, "setRuleset": a.SetRuleset,
		"getMap": a.Map, "copyTarget": a.CopyTarget, "getView": a.View, "saveView": a.SaveView,
		"getAppearance": a.GetAppearance, "saveAppearance": a.SaveAppearance, "resetAppearance": a.ResetAppearance,
	} {
		if err := w.Bind(name, fn); err != nil {
			return err
		}
	}
	// Everything that waits on the server runs off the UI thread so the window never freezes.
	for name, fn := range map[string]any{
		"getCharts": a.Charts, "search": a.Search, "getTrail": a.Trail, "getBounty": a.Bounty, "testViewer": a.TestViewer,
	} {
		if err := w.BindAsync(name, fn); err != nil {
			return err
		}
	}
	return nil
}

// ViewPrefs are the viewer.json fields the page changes without opening settings.
type ViewPrefs struct {
	Ruleset     string `json:"ruleset"`
	Orientation string `json:"orientation"`
	SelectedMap int    `json:"selectedMap"`
	ShowHistory bool   `json:"showHistory"`
	Configured  bool   `json:"configured"`
}

// Result is the common answer of calls that can fail in ways the user should read.
type Result struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

func failure(err error) Result {
	var ae *apiclient.APIError
	if errors.As(err, &ae) {
		return Result{Code: ae.Code, Message: secret.Redact(ae.Message), RequestID: ae.RequestID}
	}
	return Result{Message: secret.Redact(err.Error())}
}

// View returns the saved view preferences (never the key).
func (a *App) View() (ViewPrefs, error) {
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return ViewPrefs{}, err
	}
	return ViewPrefs{Ruleset: string(effectiveRuleset(cfg.Ruleset)), Orientation: cfg.Orientation, SelectedMap: cfg.SelectedMap,
		ShowHistory: cfg.ShowHistory, Configured: cfg.APIKey != ""}, nil
}

// SaveView stores orientation, the pinned map and the history toggle.
func (a *App) SaveView(p ViewPrefs) (Result, error) {
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return Result{}, err
	}
	cfg.Orientation, cfg.SelectedMap, cfg.ShowHistory = p.Orientation, p.SelectedMap, p.ShowHistory
	if err := a.d.SaveConfig(cfg); err != nil {
		return failure(err), nil
	}
	return Result{OK: true}, nil
}

func effectiveRuleset(s string) domain.Ruleset {
	if s == "" || s == "auto" {
		return defaultRuleset
	}
	return domain.Ruleset(s)
}

// Connect (re)loads the saved settings, builds the client and starts polling. Calling it again restarts the poller.
func (a *App) Connect() (Result, error) {
	a.stopPolling()
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return failure(err), nil
	}
	if cfg.APIKey == "" {
		return Result{Code: "not_configured", Message: "Connect to a server: enter its address and your view key."}, nil
	}
	c, err := a.d.NewClient(apiclient.Options{BaseURL: cfg.ServerURL, APIKey: cfg.APIKey, CAFile: cfg.CAFile, UserAgent: a.d.Backend.UserAgent()})
	if err != nil {
		return failure(err), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := data.New(data.Options{Client: c, Interval: time.Duration(cfg.PollIntervalS) * time.Second, Params: a.sweepParams, Publish: a.publish})
	a.mu.Lock()
	a.client, a.cfg, a.poller, a.cancel = c, cfg, p, cancel
	a.mu.Unlock()
	go p.Run(ctx)
	return Result{OK: true}, nil
}

func (a *App) stopPolling() {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel, a.poller = nil, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close stops polling; call it when the window closes.
func (a *App) Close() { a.stopPolling() }

func (a *App) sweepParams() apiclient.SweepParams {
	a.mu.Lock()
	defer a.mu.Unlock()
	return apiclient.SweepParams{Ruleset: effectiveRuleset(a.cfg.Ruleset), MaxAgeSec: sweepMaxAgeSec}
}

// SetVisible pauses polling while the window is hidden (page visibilitychange) and resumes with an immediate sweep.
func (a *App) SetVisible(v bool) (bool, error) {
	a.mu.Lock()
	p := a.poller
	a.mu.Unlock()
	if p != nil {
		p.SetVisible(v)
	}
	return v, nil
}

// SetRuleset switches the viewed ruleset and sweeps at once.
func (a *App) SetRuleset(rs string) (Result, error) {
	if !domain.Ruleset(rs).Valid() {
		return Result{Code: "invalid_request", Message: "unknown ruleset"}, nil
	}
	a.mu.Lock()
	a.cfg.Ruleset = rs
	p := a.poller
	a.mu.Unlock()
	cfg, err := a.d.LoadConfig()
	if err == nil {
		cfg.Ruleset = rs
		err = a.d.SaveConfig(cfg)
	}
	if err != nil {
		return failure(err), nil
	}
	if p != nil {
		p.Wake()
	}
	return Result{OK: true}, nil
}

// update is the JSON the page's onUpdate receives.
type update struct {
	State      data.State `json:"state"`
	ServerTime int64      `json:"serverTime,omitempty"`
	Contacts   any        `json:"contacts,omitempty"`
	Error      *Result    `json:"error,omitempty"`
}

// publish serialises one update with encoding/json (which escapes < > & U+2028/9) and pushes it to the page.
// No string built from API text is ever concatenated into JS.
func (a *App) publish(u data.Update) {
	msg := update{State: u.State}
	if u.Result != nil {
		msg.ServerTime, msg.Contacts = u.Result.ServerTime, u.Result.Contacts
		if msg.Contacts == nil {
			msg.Contacts = []domain.SweepContact{}
		}
	}
	if u.Err != nil {
		r := failure(u.Err)
		msg.Error = &r
	}
	raw, err := json.Marshal(msg)
	if err != nil || a.win == nil {
		return
	}
	a.win.Eval("window.__app.onUpdate(" + string(raw) + ")")
}

func (a *App) conn() (Client, domain.Ruleset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil, "", errors.New("not connected")
	}
	return a.client, effectiveRuleset(a.cfg.Ruleset), nil
}

func callCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callTimeout)
}

// ChartsResult is the answer of getCharts.
type ChartsResult struct {
	Result
	Charts []domain.Chart `json:"charts"`
}

// Charts reads the map rectangles.
func (a *App) Charts() (ChartsResult, error) {
	c, _, err := a.conn()
	if err != nil {
		return ChartsResult{Result: failure(err)}, nil
	}
	ctx, cancel := callCtx()
	defer cancel()
	ch, err := c.Charts(ctx)
	if err != nil {
		return ChartsResult{Result: failure(err)}, nil
	}
	return ChartsResult{Result: Result{OK: true}, Charts: ch}, nil
}

// MapResult carries a bundled image as a data: URI; OK is false when no image exists for the zone.
type MapResult struct {
	OK  bool   `json:"ok"`
	URI string `json:"uri,omitempty"`
}

// Map returns the bundled image for a zone name.
func (a *App) Map(zone string) (MapResult, error) {
	if a.d.Maps == nil {
		return MapResult{}, nil
	}
	uri, err := a.d.Maps.DataURI(zone)
	return MapResult{OK: err == nil, URI: uri}, nil
}

// SearchResult is the answer of search.
type SearchResult struct {
	Result
	Players []apiclient.PlayerSummary `json:"players"`
}

// Search looks players up by name (at least two characters).
func (a *App) Search(q string) (SearchResult, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < searchMinRunes {
		return SearchResult{Result: Result{OK: true}, Players: []apiclient.PlayerSummary{}}, nil
	}
	c, rs, err := a.conn()
	if err != nil {
		return SearchResult{Result: failure(err)}, nil
	}
	ctx, cancel := callCtx()
	defer cancel()
	ps, err := c.Players(ctx, rs, q, searchLimit)
	if err != nil {
		return SearchResult{Result: failure(err)}, nil
	}
	return SearchResult{Result: Result{OK: true}, Players: ps}, nil
}

// TrailResult is the answer of getTrail.
type TrailResult struct {
	Result
	Trail apiclient.Trail `json:"trail"`
}

// Trail reads one contact's history; mapID 0 means every map. The GUID is validated before any call.
func (a *App) Trail(guid string, mapID int) (TrailResult, error) {
	if !guidRE.MatchString(guid) {
		return TrailResult{Result: Result{Code: "invalid_request", Message: "not a player id"}}, nil
	}
	c, rs, err := a.conn()
	if err != nil {
		return TrailResult{Result: failure(err)}, nil
	}
	f := apiclient.TrailParams{Limit: trailLimit}
	if mapID > 0 {
		f.MapID = &mapID
	}
	ctx, cancel := callCtx()
	defer cancel()
	t, err := c.Trail(ctx, rs, guid, f)
	if err != nil {
		return TrailResult{Result: failure(err)}, nil
	}
	return TrailResult{Result: Result{OK: true}, Trail: t}, nil
}

// BountyResult is the answer of getBounty.
type BountyResult struct {
	Result
	Ranking []domain.KillTotal `json:"ranking"`
}

// Bounty reads the kill ranking.
func (a *App) Bounty() (BountyResult, error) {
	c, rs, err := a.conn()
	if err != nil {
		return BountyResult{Result: failure(err)}, nil
	}
	ctx, cancel := callCtx()
	defer cancel()
	r, err := c.Bounty(ctx, rs, bountyLimit)
	if err != nil {
		return BountyResult{Result: failure(err)}, nil
	}
	return BountyResult{Result: Result{OK: true}, Ranking: r}, nil
}

// CopyTarget puts "/target Name" on the OS clipboard. Names that are not plain letters are refused, so nothing
// the server sends can smuggle a command into the clipboard. OK is false when no clipboard is available; the page
// then shows the text for Ctrl+C.
func (a *App) CopyTarget(name string) (Result, error) {
	if !targetNameRE.MatchString(name) {
		return Result{Code: "invalid_request", Message: "name has characters that cannot be targeted"}, nil
	}
	text := "/target " + name
	if a.d.Clipboard == nil {
		return Result{Code: "no_clipboard", Message: text}, nil
	}
	if err := a.d.Clipboard(text); err != nil {
		return Result{Code: "no_clipboard", Message: text}, nil
	}
	return Result{OK: true, Message: text}, nil
}

// TestViewer checks the server and then the key: health first, then an authenticated read.
func (a *App) TestViewer() (Result, error) {
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return failure(err), nil
	}
	c, err := a.d.NewClient(apiclient.Options{BaseURL: cfg.ServerURL, APIKey: cfg.APIKey, CAFile: cfg.CAFile, UserAgent: a.d.Backend.UserAgent()})
	if err != nil {
		return failure(err), nil
	}
	ctx, cancel := callCtx()
	defer cancel()
	if err := c.Check(ctx); err != nil {
		return Result{Code: "unreachable", Message: secret.Redact(fmt.Sprintf("Could not reach the server: %v", err))}, nil
	}
	if _, err := c.Charts(ctx); err != nil {
		if apiclient.IsAuth(err) {
			return Result{Code: "key_rejected", Message: "Server reachable, but it rejected the key."}, nil
		}
		return failure(err), nil
	}
	return Result{OK: true, Message: "Server reachable, key accepted."}, nil
}

// RealDeps wires the production implementations.
func RealDeps(store *maps.Store, clipboard func(string) error) Deps {
	return Deps{
		Backend:    settings.ViewerBackend{},
		LoadConfig: func() (config.ViewerConfig, error) { return config.Load("viewer.json", config.ViewerDefaults) },
		SaveConfig: func(c config.ViewerConfig) error { return config.Save("viewer.json", c) },
		NewClient: func(o apiclient.Options) (Client, error) {
			c, err := apiclient.New(o)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		Maps:      store,
		Clipboard: clipboard,
	}
}
