package app

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrNoPicker means the OS folder dialog is unavailable; the user types the path instead.
var ErrNoPicker = errors.New("no folder dialog available on this system")

const pickerTimeout = 5 * time.Minute

// PickFolder shows the operating system's folder dialog and returns the chosen path ("" if cancelled).
func PickFolder() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pickerTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		path, err := exec.LookPath("zenity")
		if err != nil {
			return "", ErrNoPicker
		}
		cmd = exec.CommandContext(ctx, path, "--file-selection", "--directory", "--title=Choose the combat log folder") // #nosec G204 -- fixed arguments
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "Choose the combat log folder")`) // #nosec G204 -- fixed script
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", windowsPickScript) // #nosec G204 -- fixed script
	default:
		return "", ErrNoPicker
	}
	out, err := cmd.Output()
	if err != nil {
		return "", nil // cancelled or failed: nothing chosen
	}
	return strings.TrimSpace(string(out)), nil
}

const windowsPickScript = `Add-Type -AssemblyName System.Windows.Forms; ` +
	`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
	`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`
