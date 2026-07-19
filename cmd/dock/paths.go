package main

import (
	"os"
	"path/filepath"
	"runtime"
)

const maxLogSize = 5 << 20 // truncate launcher.log above this on launch

// stateDir is where the lock file and launcher.log live:
// ~/Library/Logs/chartplotter on macOS, %LocalAppData%\chartplotter on
// Windows, XDG state (~/.local/state/chartplotter) elsewhere.
func stateDir() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "Library", "Logs", "chartplotter")
	case "windows":
		dir = filepath.Join(os.Getenv("LocalAppData"), "chartplotter")
	default:
		if x := os.Getenv("XDG_STATE_HOME"); x != "" {
			dir = filepath.Join(x, "chartplotter")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".local", "state", "chartplotter")
		}
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// openLog opens (and, when oversized, truncates) launcher.log for appending.
// The child server's stdout/stderr are wired to the same file.
func openLog(dir string) (*os.File, error) {
	path := filepath.Join(dir, "launcher.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		os.Truncate(path, 0)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}
