package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens a URL (or file path — used for Show Logs) with the
// platform's default opener. Errors are ignored: there is nowhere useful to
// surface them from a tray app, and the menu stays functional.
func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	cmd.Start()
}
