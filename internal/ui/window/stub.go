//go:build !webview

package window

// New always fails in a build without -tags webview.
func New(Options) (Window, error) { return nil, ErrBuiltWithoutWebview }
