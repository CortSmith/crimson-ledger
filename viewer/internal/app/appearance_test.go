package app

import (
	"testing"

	"crimsonledger/internal/config"
)

func TestApp_GetAppearance_FillsDefaultsAndShipsPresets(t *testing.T) {
	a, _, _ := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	v, err := a.GetAppearance()
	if err != nil {
		t.Fatal(err)
	}
	if v.Appearance.DotSize != 5 || v.Appearance.Shapes["hostile"] != "diamond" || v.TimeZone != "local" {
		t.Errorf("defaults not applied: %+v", v)
	}
	if len(v.Presets) < 4 || v.Presets["default"]["friendly"] == "" || len(v.ColorKeys) != 7 || len(v.TrackKeys) != 8 {
		t.Errorf("presets or keys missing: %+v", v)
	}
}

func TestApp_SaveAppearance_PersistsAndKeepsConnectionSettings(t *testing.T) {
	a, _, saved := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	ap := config.AppearanceDefaults()
	ap.DotSize, ap.Palette = 9, "colorblind"
	r, _ := a.SaveAppearance(AppearanceSave{Appearance: ap, TimeZone: "UTC"})
	if !r.OK || saved.Appearance.DotSize != 9 || saved.Appearance.Palette != "colorblind" || saved.TimeZone != "UTC" {
		t.Fatalf("%+v %+v", r, saved)
	}
	if saved.APIKey != canaryKey || saved.ServerURL != cfgWithKey().ServerURL {
		t.Error("saving appearance changed the connection settings")
	}
}

func TestApp_SaveAppearance_RejectsBadInputAndSavesNothing(t *testing.T) {
	a, _, saved := newApp(t, &fakeClient{}, cfgWithKey(), nil)
	bad := config.AppearanceDefaults()
	bad.Custom = map[string]string{"friendly": "url(x)"}
	if r, _ := a.SaveAppearance(AppearanceSave{Appearance: bad}); r.OK || r.Code != "invalid_request" {
		t.Errorf("bad colour accepted: %+v", r)
	}
	if r, _ := a.SaveAppearance(AppearanceSave{TimeZone: "Nowhere/City"}); r.OK {
		t.Error("bad time zone accepted")
	}
	if saved.Appearance.Custom != nil {
		t.Error("rejected input was saved")
	}
}

func TestApp_ResetAppearance_BackToDefaults(t *testing.T) {
	cfg := cfgWithKey()
	cfg.Appearance.DotSize = 11
	a, _, saved := newApp(t, &fakeClient{}, cfg, nil)
	v, err := a.ResetAppearance()
	if err != nil || v.Appearance.DotSize != 5 || saved.Appearance.DotSize != 0 {
		t.Fatalf("%+v %v saved=%+v", v, err, saved.Appearance)
	}
}
