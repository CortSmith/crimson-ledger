//go:build webview && windows

package window

import webview2 "github.com/jchv/go-webview2"

type wv struct{ w webview2.WebView }

// New opens a native window (Edge WebView2).
func New(o Options) (Window, error) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: o.Debug, AutoFocus: true,
		WindowOptions: webview2.WindowOptions{Title: o.Title, Width: uint(o.Width), Height: uint(o.Height), Center: true},
	})
	if w == nil {
		return nil, ErrNoWebView
	}
	return &wv{w: w}, nil
}

func (x *wv) SetTitle(t string)           { x.w.SetTitle(t) }
func (x *wv) SetSize(w, h int)            { x.w.SetSize(w, h, webview2.HintNone) }
func (x *wv) Bind(n string, fn any) error { return x.w.Bind(n, fn) }
func (x *wv) SetHTML(h string) error      { x.w.SetHtml(h); return nil }
func (x *wv) Eval(js string)              { x.w.Dispatch(func() { x.w.Eval(js) }) } // safe from any goroutine
func (x *wv) BindAsync(n string, fn any) error {
	return bindAsync(lowLevel{bind: x.w.Bind, init: x.w.Init, eval: x.Eval}, n, fn)
}
func (x *wv) Run()       { x.w.Run() }
func (x *wv) Terminate() { x.w.Terminate() }
func (x *wv) Destroy()   { x.w.Destroy() }
