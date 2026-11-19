// Package clip copies text to the OS clipboard by running the platform's own tool. The clipboard is a convenience:
// when no tool exists, Copy returns ErrUnavailable and the page shows the text for Ctrl+C.
package clip

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable means no clipboard tool was found.
var ErrUnavailable = errors.New("no clipboard tool available")

const copyTimeout = 3 * time.Second

// candidates lists tools per OS in preference order; each reads the text from stdin.
func candidates(goos string) [][]string {
	switch goos {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip.exe"}}
	}
	return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
}

// Copy puts text on the clipboard.
func Copy(text string) error {
	return copyWith(runtime.GOOS, exec.LookPath, text)
}

func copyWith(goos string, lookPath func(string) (string, error), text string) error {
	for _, c := range candidates(goos) {
		path, err := lookPath(c[0])
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
		cmd := exec.CommandContext(ctx, path, c[1:]...) // #nosec G204: path comes from a fixed list of tool names
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
	}
	return ErrUnavailable
}
