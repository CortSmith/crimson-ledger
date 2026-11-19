// Command crimson-scout checks that the system can show a window, then starts the scout window.
package main

import (
	"os"

	"crimsonledger/internal/ui/launcher"
)

func main() {
	os.Exit(launcher.Run("Crimson Scout", "crimson-scout-ui", os.Args[1:]))
}
