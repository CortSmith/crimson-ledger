package config

import (
	"fmt"
	"regexp"
	"slices"
	"time"
)

// Appearance is the viewer's look (viewer.json "appearance"). The zero value means "all defaults", so a viewer.json
// written before this field existed still loads; Resolved() fills every unset field before the page sees it.
type Appearance struct {
	Scheme        string            `json:"scheme,omitempty"`  // system | light | dark
	Palette       string            `json:"palette,omitempty"` // a preset name, or "custom"
	Custom        map[string]string `json:"custom,omitempty"`  // colour key -> #rrggbb, used when Palette is "custom"
	Shapes        map[string]string `json:"shapes,omitempty"`  // reaction -> shape
	DotSize       int               `json:"dot_size,omitempty"`
	SelectedRing  int               `json:"selected_ring,omitempty"`
	Outline       string            `json:"outline,omitempty"` // none | thin | thick
	Stale         string            `json:"stale,omitempty"`   // dim | hollow | grey
	StaleMinutes  int               `json:"stale_minutes,omitempty"`
	Approx        string            `json:"approx,omitempty"` // filled | hollow
	Labels        string            `json:"labels,omitempty"` // none | hover | always
	ShowTrails    *bool             `json:"show_trails,omitempty"`
	TrailStyle    string            `json:"trail_style,omitempty"` // solid | dashed | dotted
	TrailWidth    int               `json:"trail_width,omitempty"`
	TrailDot      int               `json:"trail_dot,omitempty"`
	TrailFade     *bool             `json:"trail_fade,omitempty"`
	TrailArrow    *bool             `json:"trail_arrow,omitempty"`
	GapMinutes    int               `json:"gap_minutes,omitempty"`
	Grid          string            `json:"grid,omitempty"` // off | on
	MapOpacity    int               `json:"map_opacity,omitempty"`
	Background    string            `json:"background,omitempty"` // map | panel | rings
	Density       string            `json:"density,omitempty"`    // compact | comfortable
	ShapeOnly     *bool             `json:"shape_only,omitempty"`
	ReduceMotion  *bool             `json:"reduce_motion,omitempty"`
	UIScale       int               `json:"ui_scale,omitempty"` // percent
	NeutralAsHost *bool             `json:"neutral_as_hostile,omitempty"`
}

// ColorKeys are the colours a palette defines, in the order the settings page shows them.
var ColorKeys = []string{"friendly", "hostile", "neutral", "stale", "trail", "selection", "background"}

// TrackKeys are the eight player-track colours.
var TrackKeys = []string{"track1", "track2", "track3", "track4", "track5", "track6", "track7", "track8"}

// Palette is one preset: colour key -> #rrggbb.
type Palette map[string]string

// Presets returns the built-in palettes. This is the only copy: the page gets it through getAppearance.
func Presets() map[string]Palette {
	track := []string{"#332288", "#88ccee", "#44aa99", "#117733", "#999933", "#882255", "#aa4499", "#ddcc77"}
	okabe := []string{"#0072b2", "#e69f00", "#009e73", "#cc79a7", "#56b4e9", "#d55e00", "#f0e442", "#999999"}
	hc := []string{"#00ffff", "#ffff00", "#00ff00", "#ff00ff", "#ff8800", "#ffffff", "#8888ff", "#ff8888"}
	mono := []string{"#ffffff", "#dddddd", "#bbbbbb", "#999999", "#eeeeee", "#cccccc", "#aaaaaa", "#888888"}
	with := func(base Palette, t []string) Palette {
		out := Palette{}
		for k, v := range base {
			out[k] = v
		}
		for i, k := range TrackKeys {
			out[k] = t[i]
		}
		return out
	}
	return map[string]Palette{
		"default": with(Palette{"friendly": "#3b82f6", "hostile": "#ef4444", "neutral": "#f59e0b", "stale": "#6b7280",
			"trail": "#e0455b", "selection": "#ffffff", "background": "#0a0d12"}, track),
		"colorblind": with(Palette{"friendly": "#0072b2", "hostile": "#d55e00", "neutral": "#e69f00", "stale": "#7a7a7a",
			"trail": "#cc79a7", "selection": "#ffffff", "background": "#0a0d12"}, okabe),
		"highcontrast": with(Palette{"friendly": "#00ffff", "hostile": "#ff3030", "neutral": "#ffff00", "stale": "#9a9a9a",
			"trail": "#ff00ff", "selection": "#ffffff", "background": "#000000"}, hc),
		"mono": with(Palette{"friendly": "#ffffff", "hostile": "#ffffff", "neutral": "#bbbbbb", "stale": "#666666",
			"trail": "#dddddd", "selection": "#ffffff", "background": "#0a0d12"}, mono),
	}
}

var (
	hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	schemes   = []string{"system", "light", "dark"}
	shapes    = []string{"circle", "diamond", "square", "triangle", "cross"}
	reactions = []string{"friendly", "hostile", "neutral"}
	outlines  = []string{"none", "thin", "thick"}
	staleKind = []string{"dim", "hollow", "grey"}
	approxes  = []string{"filled", "hollow"}
	labelKind = []string{"none", "hover", "always"}
	trailKind = []string{"solid", "dashed", "dotted"}
	gridKind  = []string{"off", "on"}
	backdrops = []string{"map", "panel", "rings"}
	densities = []string{"compact", "comfortable"}
)

// AppearanceDefaults is the look that matches the viewer before this setting existed.
func AppearanceDefaults() Appearance {
	t := true
	f := false
	return Appearance{
		Scheme: "system", Palette: "default",
		Shapes:  map[string]string{"friendly": "circle", "hostile": "diamond", "neutral": "square"},
		DotSize: 5, SelectedRing: 9, Outline: "thin", Stale: "dim", StaleMinutes: 5, Approx: "filled", Labels: "hover",
		ShowTrails: &t, TrailStyle: "dashed", TrailWidth: 2, TrailDot: 3, TrailFade: &t, TrailArrow: &t, GapMinutes: 15,
		Grid: "off", MapOpacity: 100, Background: "map", Density: "comfortable",
		ShapeOnly: &f, ReduceMotion: &f, UIScale: 100, NeutralAsHost: &f,
	}
}

// Resolved returns a copy with every unset field replaced by its default, so the page never handles gaps.
func (a Appearance) Resolved() Appearance {
	d := AppearanceDefaults()
	str := func(v *string, def string) {
		if *v == "" {
			*v = def
		}
	}
	num := func(v *int, def int) {
		if *v == 0 {
			*v = def
		}
	}
	flag := func(v **bool, def *bool) {
		if *v == nil {
			*v = def
		}
	}
	str(&a.Scheme, d.Scheme)
	str(&a.Palette, d.Palette)
	str(&a.Outline, d.Outline)
	str(&a.Stale, d.Stale)
	str(&a.Approx, d.Approx)
	str(&a.Labels, d.Labels)
	str(&a.TrailStyle, d.TrailStyle)
	str(&a.Grid, d.Grid)
	str(&a.Background, d.Background)
	str(&a.Density, d.Density)
	num(&a.DotSize, d.DotSize)
	num(&a.SelectedRing, d.SelectedRing)
	num(&a.StaleMinutes, d.StaleMinutes)
	num(&a.TrailWidth, d.TrailWidth)
	num(&a.TrailDot, d.TrailDot)
	num(&a.GapMinutes, d.GapMinutes)
	num(&a.MapOpacity, d.MapOpacity)
	num(&a.UIScale, d.UIScale)
	flag(&a.ShowTrails, d.ShowTrails)
	flag(&a.TrailFade, d.TrailFade)
	flag(&a.TrailArrow, d.TrailArrow)
	flag(&a.ShapeOnly, d.ShapeOnly)
	flag(&a.ReduceMotion, d.ReduceMotion)
	flag(&a.NeutralAsHost, d.NeutralAsHost)
	merged := map[string]string{}
	for k, v := range d.Shapes {
		merged[k] = v
	}
	for k, v := range a.Shapes {
		merged[k] = v
	}
	a.Shapes = merged
	return a
}

// Validate checks every field that is set. Unset (zero) fields are fine: they mean "default".
func (a Appearance) Validate() error {
	const f = "viewer.json appearance"
	one := func(field, v string, allowed []string) error {
		if v == "" || slices.Contains(allowed, v) {
			return nil
		}
		return fmt.Errorf("%s: %s must be one of %v", f, field, allowed)
	}
	rng := func(field string, v, lo, hi int) error {
		if v == 0 || (v >= lo && v <= hi) {
			return nil
		}
		return fmt.Errorf("%s: %s must be between %d and %d (got %d)", f, field, lo, hi, v)
	}
	if err := firstError(
		one("scheme", a.Scheme, schemes), one("outline", a.Outline, outlines), one("stale", a.Stale, staleKind),
		one("approx", a.Approx, approxes), one("labels", a.Labels, labelKind), one("trail_style", a.TrailStyle, trailKind),
		one("grid", a.Grid, gridKind), one("background", a.Background, backdrops), one("density", a.Density, densities),
		rng("dot_size", a.DotSize, 3, 12), rng("selected_ring", a.SelectedRing, 5, 20), rng("stale_minutes", a.StaleMinutes, 1, 120),
		rng("trail_width", a.TrailWidth, 1, 4), rng("trail_dot", a.TrailDot, 1, 6), rng("gap_minutes", a.GapMinutes, 1, 240),
		rng("map_opacity", a.MapOpacity, 30, 100), rng("ui_scale", a.UIScale, 90, 150),
	); err != nil {
		return err
	}
	if a.Palette != "" && a.Palette != "custom" {
		if _, ok := Presets()[a.Palette]; !ok {
			return fmt.Errorf("%s: palette must be a preset name or custom", f)
		}
	}
	for k, v := range a.Shapes {
		if !slices.Contains(reactions, k) || !slices.Contains(shapes, v) {
			return fmt.Errorf("%s: shapes must map friendly, hostile or neutral to one of %v", f, shapes)
		}
	}
	valid := append(slices.Clone(ColorKeys), TrackKeys...)
	for k, v := range a.Custom {
		if !slices.Contains(valid, k) {
			return fmt.Errorf("%s: unknown custom colour %q", f, k)
		}
		if !hexColour.MatchString(v) {
			return fmt.Errorf("%s: custom colour %s must look like #rrggbb", f, k)
		}
	}
	return nil
}

// ValidTimeZone accepts "" and "local" (the machine's zone), "UTC", or any IANA name the system knows.
func ValidTimeZone(z string) bool {
	if z == "" || z == "local" || z == "UTC" {
		return true
	}
	if len(z) > 64 {
		return false
	}
	_, err := time.LoadLocation(z)
	return err == nil
}
