package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppearance_ZeroValueIsValidAndMeansDefaults(t *testing.T) {
	var a Appearance
	if err := a.Validate(); err != nil {
		t.Fatalf("zero appearance must be valid (old viewer.json files have none): %v", err)
	}
	r := a.Resolved()
	d := AppearanceDefaults()
	got, _ := json.Marshal(r)
	want, _ := json.Marshal(d.Resolved())
	if string(got) != string(want) {
		t.Errorf("zero.Resolved() != defaults:\n got %s\nwant %s", got, want)
	}
	if r.DotSize != 5 || r.Shapes["hostile"] != "diamond" || r.Palette != "default" {
		t.Errorf("defaults must keep today's look, got %+v", r)
	}
}

func TestAppearance_DefaultsAreValid(t *testing.T) {
	if err := AppearanceDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAppearance_ResolvedKeepsUserChoicesAndMergesShapes(t *testing.T) {
	off := false
	a := Appearance{DotSize: 8, TrailFade: &off, Shapes: map[string]string{"friendly": "triangle"}}.Resolved()
	if a.DotSize != 8 || *a.TrailFade {
		t.Errorf("explicit values were overwritten: %+v", a)
	}
	if a.Shapes["friendly"] != "triangle" || a.Shapes["hostile"] != "diamond" {
		t.Errorf("shapes not merged: %v", a.Shapes)
	}
}

func TestAppearance_ValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Appearance)
	}{
		{"scheme", func(a *Appearance) { a.Scheme = "purple" }},
		{"palette", func(a *Appearance) { a.Palette = "nope" }},
		{"dot too small", func(a *Appearance) { a.DotSize = 2 }},
		{"dot too big", func(a *Appearance) { a.DotSize = 13 }},
		{"trail width", func(a *Appearance) { a.TrailWidth = 9 }},
		{"opacity low", func(a *Appearance) { a.MapOpacity = 10 }},
		{"ui scale", func(a *Appearance) { a.UIScale = 300 }},
		{"shape name", func(a *Appearance) { a.Shapes = map[string]string{"friendly": "star"} }},
		{"shape key", func(a *Appearance) { a.Shapes = map[string]string{"boss": "circle"} }},
		{"custom key", func(a *Appearance) { a.Custom = map[string]string{"sparkle": "#ffffff"} }},
		{"custom colour", func(a *Appearance) { a.Custom = map[string]string{"friendly": "red"} }},
		{"css injection", func(a *Appearance) { a.Custom = map[string]string{"friendly": "#fff;}</style>"} }},
		{"labels", func(a *Appearance) { a.Labels = "x" }},
		{"gap", func(a *Appearance) { a.GapMinutes = 999 }},
	}
	for _, c := range cases {
		a := AppearanceDefaults()
		c.mutate(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestPresets_AreCompleteAndValid(t *testing.T) {
	want := append(append([]string{}, ColorKeys...), TrackKeys...)
	for name, p := range Presets() {
		for _, k := range want {
			if !hexColour.MatchString(p[k]) {
				t.Errorf("preset %s: %s = %q", name, k, p[k])
			}
		}
		a := AppearanceDefaults()
		a.Palette = name
		if err := a.Validate(); err != nil {
			t.Errorf("preset %s not selectable: %v", name, err)
		}
	}
	if _, ok := Presets()["default"]; !ok {
		t.Error("no default preset")
	}
}

func TestViewerConfig_OldFileWithoutAppearanceStillLoads(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(planViewerJSON))
	dec.DisallowUnknownFields()
	var v ViewerConfig
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("version-1 viewer.json must still validate: %v", err)
	}
}

func TestViewerConfig_TimeZone(t *testing.T) {
	for z, ok := range map[string]bool{"": true, "local": true, "UTC": true, "America/New_York": true, "Mars/Base": false, "../etc": false} {
		v := ViewerDefaults()
		v.TimeZone = z
		if (v.Validate() == nil) != ok {
			t.Errorf("time zone %q: valid=%v want %v", z, !ok, ok)
		}
	}
}

func TestViewerConfig_AppearanceErrorIsReported(t *testing.T) {
	v := ViewerDefaults()
	v.Appearance.DotSize = 99
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "dot_size") {
		t.Fatalf("got %v", err)
	}
}
