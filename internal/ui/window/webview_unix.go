//go:build webview && !windows

package window

import webview "github.com/webview/webview_go"

type wv struct{ w webview.WebView }

// New opens a native window (WebKitGTK on Linux, WKWebView on macOS).
func New(o Options) (Window, error) {
	w := webview.New(o.Debug)
	if w == nil {
		return nil, ErrNoWebView
	}
	x := &wv{w: w}
	x.SetTitle(o.Title)
	x.SetSize(o.Width, o.Height)
	return x, nil
}

func (x *wv) SetTitle(t string)           { x.w.SetTitle(t) }
func (x *wv) SetSize(w, h int)            { x.w.SetSize(w, h, webview.HintNone) }
func (x *wv) Bind(n string, fn any) error { return x.w.Bind(n, fn) }
func (x *wv) SetHTML(h string) error      { x.w.SetHtml(h); return nil }
func (x *wv) Eval(js string)              { x.w.Dispatch(func() { x.w.Eval(js) }) } // safe from any goroutine
func (x *wv) BindAsync(n string, fn any) error {
	return bindAsync(lowLevel{bind: x.w.Bind, init: x.w.Init, eval: x.Eval}, n, fn)
}
func (x *wv) Run()       { x.w.Run() }
func (x *wv) Terminate() { x.w.Terminate() }
func (x *wv) Destroy()   { x.w.Destroy() }
