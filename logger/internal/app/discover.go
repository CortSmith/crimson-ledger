package app

import (
	"path/filepath"
	"runtime"
)

// candidateLogDirs lists where WoW usually keeps combat logs on each OS, most likely first.
func candidateLogDirs(goos, home string) []string {
	switch goos {
	case "windows":
		return []string{
			`C:\Program Files (x86)\World of Warcraft\_forever_\Logs`,
			`C:\Program Files\World of Warcraft\_forever_\Logs`,
			`C:\Program Files (x86)\World of Warcraft\_retail_\Logs`,
		}
	case "darwin":
		return []string{
			"/Applications/World of Warcraft/_forever_/Logs",
			"/Applications/World of Warcraft/_retail_/Logs",
		}
	}
	return []string{
		filepath.Join(home, "Games", "World of Warcraft", "_forever_", "Logs"),
		filepath.Join(home, ".wine", "drive_c", "Program Files (x86)", "World of Warcraft", "_forever_", "Logs"),
		filepath.Join(home, "Games", "battlenet", "drive_c", "Program Files (x86)", "World of Warcraft", "_forever_", "Logs"),
	}
}

// DiscoverLogsDir returns the first known log folder for goos that isDir accepts, or "" when none exists.
func DiscoverLogsDir(goos, home string, isDir func(string) bool) string {
	for _, d := range candidateLogDirs(goos, home) {
		if isDir(d) {
			return d
		}
	}
	return ""
}

// hostOS is runtime.GOOS, named so tests read clearly.
const hostOS = runtime.GOOS
