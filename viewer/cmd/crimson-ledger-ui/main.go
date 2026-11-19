// Command crimson-ledger-ui is the viewer's window. Run it through crimson-ledger, which checks the system first.
package main

import (
	"fmt"
	"os"

	"crimsonledger/internal/ui/page"
	"crimsonledger/internal/ui/selftest"
	"crimsonledger/internal/ui/window"
	"crimsonledger/viewer/internal/app"
	"crimsonledger/viewer/internal/clip"
	"crimsonledger/viewer/internal/maps"
	bundle "crimsonledger/viewer/maps"
	"crimsonledger/viewer/web"
)

const (
	windowWidth  = 1100
	windowHeight = 760
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crimson-ledger:", err)
		os.Exit(1)
	}
}

func run() error {
	store, err := maps.Open(bundle.FS)
	if err != nil {
		return err
	}
	w, err := window.New(window.Options{Title: "Crimson Ledger", Width: windowWidth, Height: windowHeight})
	if err != nil {
		return err
	}
	defer w.Destroy()
	a := app.New(app.RealDeps(store, clip.Copy))
	defer a.Close()
	if err := a.Register(w); err != nil {
		return err
	}
	html, nonce := page.Build(web.Assets())
	test := selftest.Wanted(os.Args[1:])
	if test {
		html = selftest.Inject(html, nonce)
		if err := selftest.Bind(w, os.Stdout, os.Exit); err != nil {
			return err
		}
		selftest.Watchdog(os.Stdout, os.Exit)
	}
	if err := w.SetHTML(html); err != nil {
		return err
	}
	w.Run()
	return nil
}
