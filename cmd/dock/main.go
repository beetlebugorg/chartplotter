// Command dock is the desktop launcher: a menu bar item (macOS) /
// notification-area icon (Windows) / StatusNotifierItem (Linux) that spawns the
// sibling `chartplotter` binary's `serve` subcommand and manages its lifecycle.
// It links no libtile57 and adds no server logic; the CLI binary stays the
// single engine.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/systray"
	"github.com/alecthomas/kong"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type cli struct {
	Engine  string `type:"existingfile" help:"Path to the chartplotter binary (default: sibling of this executable)."`
	Version bool   `help:"Print version and exit."`
}

func main() {
	var c cli
	kong.Parse(&c,
		kong.Name("dock"),
		kong.Description("Desktop launcher for chartplotter (tray/menu-bar app)."),
		kong.UsageOnError(),
	)
	if c.Version {
		fmt.Printf("dock %s\n", version)
		return
	}

	dir, err := stateDir()
	if err != nil {
		fatal("state dir: %v", err)
	}
	logf, err := openLog(dir)
	if err != nil {
		fatal("open log: %v", err)
	}
	defer logf.Close()

	m := &manager{log: logf, engine: c.Engine}

	// Single instance: if another launcher holds the lock, just open the browser
	// at whatever it's serving and exit — double-clicking again "opens the app".
	lock, err := acquireLock(dir)
	if err != nil {
		if url := findRunning(); url != "" {
			openBrowser(url)
		}
		return
	}
	defer lock.release()

	if !trayAvailable() {
		// No tray host (Linux without a StatusNotifierItem watcher, e.g. stock
		// GNOME sans AppIndicator extension): degrade to headless — start the
		// server, open the browser, and stay resident so it keeps running.
		m.logf("no system tray available (on GNOME, install the AppIndicator extension); running headless")
		m.startup()
		waitForSignal()
		m.stop()
		return
	}

	// systray.Run must own the main goroutine (Cocoa main thread on macOS).
	systray.Run(func() { onReady(m) }, func() { m.stop() })
}

// onReady builds the menu and wires the state machine to it. Runs once on the
// systray's ready callback; all launcher logic stays in goroutines.
func onReady(m *manager) {
	setTrayIcon(false)
	systray.SetTooltip("Chart Plotter")

	open := systray.AddMenuItem("Open Chart Plotter", "Open in the browser")
	open.Disable()
	status := systray.AddMenuItem("Starting…", "")
	status.Disable()
	systray.AddSeparator()
	startStop := systray.AddMenuItem("Stop", "Stop the chart plotter server")
	logs := systray.AddMenuItem("Show Logs", "Open the launcher log")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Stop the server and quit")

	changed := make(chan struct{}, 1)
	m.onChange = func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}

	render := func() {
		st, url := m.state()
		switch st {
		case stateStarting:
			status.SetTitle("Starting…")
			open.Disable()
			startStop.SetTitle("Stop")
			startStop.Show()
			setTrayIcon(false)
		case stateRunning, stateAdopted:
			status.SetTitle("Running on " + hostPort(url))
			open.Enable()
			if st == stateAdopted {
				// Not our child — Stop/Start would lie. Hide it (spec §Menu).
				startStop.Hide()
			} else {
				startStop.SetTitle("Stop")
				startStop.Show()
			}
			setTrayIcon(false)
		case stateStopped:
			status.SetTitle("Stopped")
			open.Disable()
			startStop.SetTitle("Start")
			startStop.Show()
			setTrayIcon(false)
		case stateError:
			status.SetTitle("Error — see logs")
			open.Disable()
			startStop.SetTitle("Start")
			startStop.Show()
			setTrayIcon(true)
		}
	}

	go func() {
		for {
			select {
			case <-changed:
				render()
			case <-open.ClickedCh:
				if _, url := m.state(); url != "" {
					openBrowser(url)
				}
			case <-startStop.ClickedCh:
				st, _ := m.state()
				if st == stateRunning || st == stateStarting {
					go m.stop()
				} else {
					go m.startup()
				}
			case <-logs.ClickedCh:
				openBrowser(m.log.Name())
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()

	// Ctrl-C / SIGTERM behaves like Quit so no child is orphaned.
	go func() {
		waitForSignal()
		systray.Quit()
	}()

	go m.startup()
}

func hostPort(url string) string {
	const pfx, sfx = "http://", "/"
	s := url
	if len(s) > len(pfx) && s[:len(pfx)] == pfx {
		s = s[len(pfx):]
	}
	if len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dock: "+format+"\n", args...)
	os.Exit(1)
}
