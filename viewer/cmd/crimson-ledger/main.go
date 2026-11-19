// Command crimson-ledger checks that the system can show a window, then starts the viewer window.
package main

import (
	"os"

	"crimsonledger/internal/ui/launcher"
)

func main() {
	os.Exit(launcher.Run("Crimson Ledger", "crimson-ledger-ui", os.Args[1:]))
}
