// Package window abstracts the native web view window. Real implementations are behind the "webview" build tag;
// tests use FakeWindow and builds without the tag get a stub that reports ErrBuiltWithoutWebview.
package window

import "errors"

// ErrBuiltWithoutWebview is returned by New in binaries compiled without -tags webview.
var ErrBuiltWithoutWebview = errors.New("this build has no window support (compile with -tags webview)")

// ErrNoWebView is returned when the system web view could not be created.
var ErrNoWebView = errors.New("could not create the web view window")

// Options configures a new window. Debug must be false in release builds (devtools off).
type Options struct {
	Title         string
	Width, Height int
	Debug         bool
}

// Window is the smallest method set both apps use. SetHTML never navigates to a URL.
type Window interface {
	SetTitle(title string)
	SetSize(w, h int)
	// Bind exposes a Go func to the page as a promise-returning JS function.
	Bind(name string, fn any) error
	// BindAsync is Bind for calls that can take a while (network): the func runs off the UI thread, so the window stays responsive.
	// Calls may overlap, so fn must be safe for concurrent use.
	BindAsync(name string, fn any) error
	SetHTML(html string) error
	// Eval runs JS; used only to push pre-serialised JSON.
	Eval(js string)
	Run()
	// Terminate makes Run return; safe to call from a bound func.
	Terminate()
	Destroy()
}
