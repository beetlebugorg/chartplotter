//go:build linux

package main

import "github.com/godbus/dbus/v5"

// trayAvailable reports whether a StatusNotifierItem host is on the session
// bus. Stock GNOME has none without the AppIndicator extension; in that case
// the dock degrades to headless instead of letting
// systray fail with no icon anywhere.
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var owner string
	err = conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0,
		"org.kde.StatusNotifierWatcher").Store(&owner)
	return err == nil && owner != ""
}
