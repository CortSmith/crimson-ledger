// Command crimson-scout-ui is the scout's window. Run it through crimson-scout, which checks the system first.
package main

import (
	"fmt"
	"os"

	"crimsonledger/internal/ui/page"
	"crimsonledger/internal/ui/selftest"
	"crimsonledger/internal/ui/window"
	"crimsonledger/logger/internal/app"
	"crimsonledger/logger/web"
)

const (
	windowWidth  = 720
	windowHeight = 860
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crimson-scout:", err)
		os.Exit(1)
	}
}

func run() error {
	w, err := window.New(window.Options{Title: "Crimson Scout", Width: windowWidth, Height: windowHeight})
	if err != nil {
		return err
	}
	defer w.Destroy()
	a := app.New(app.RealDeps())
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
	if !test {
		a.AutoStart() // in the background: a slow or dead server must not hold the window back
	}
	w.Run()
	_, _ = a.Stop()
	return nil
}
