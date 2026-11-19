package app

import (
	"crimsonledger/internal/config"
)

// AppearanceView is what the settings page and the radar read: the resolved look, the built-in palettes (the
// only copy of them) and the colour keys in display order.
type AppearanceView struct {
	Appearance config.Appearance         `json:"appearance"`
	Defaults   config.Appearance         `json:"defaults"`
	Presets    map[string]config.Palette `json:"presets"`
	ColorKeys  []string                  `json:"colorKeys"`
	TrackKeys  []string                  `json:"trackKeys"`
	TimeZone   string                    `json:"timeZone"`
}

// AppearanceSave is what the Appearance and Display tabs send back.
type AppearanceSave struct {
	Appearance config.Appearance `json:"appearance"`
	TimeZone   string            `json:"timeZone"`
}

func appearanceView(cfg config.ViewerConfig) AppearanceView {
	tz := cfg.TimeZone
	if tz == "" {
		tz = "local"
	}
	return AppearanceView{Appearance: cfg.Appearance.Resolved(), Defaults: config.AppearanceDefaults().Resolved(),
		Presets: config.Presets(), ColorKeys: config.ColorKeys, TrackKeys: config.TrackKeys, TimeZone: tz}
}

// GetAppearance returns the saved look with every gap filled in.
func (a *App) GetAppearance() (AppearanceView, error) {
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return AppearanceView{}, err
	}
	return appearanceView(cfg), nil
}

// SaveAppearance validates and stores the look and the display time zone. The API key and server are untouched.
func (a *App) SaveAppearance(s AppearanceSave) (Result, error) {
	if err := s.Appearance.Validate(); err != nil {
		return Result{Code: "invalid_request", Message: err.Error()}, nil
	}
	if !config.ValidTimeZone(s.TimeZone) {
		return Result{Code: "invalid_request", Message: "unknown time zone"}, nil
	}
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return failure(err), nil
	}
	cfg.Appearance, cfg.TimeZone = s.Appearance, s.TimeZone
	if err := a.d.SaveConfig(cfg); err != nil {
		return failure(err), nil
	}
	return Result{OK: true}, nil
}

// ResetAppearance puts every appearance option back to its default.
func (a *App) ResetAppearance() (AppearanceView, error) {
	cfg, err := a.d.LoadConfig()
	if err != nil {
		return AppearanceView{}, err
	}
	cfg.Appearance = config.Appearance{}
	if err := a.d.SaveConfig(cfg); err != nil {
		return AppearanceView{}, err
	}
	return appearanceView(cfg), nil
}
