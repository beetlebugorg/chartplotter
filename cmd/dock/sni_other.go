//go:build !linux

package main

// macOS and Windows always have a menu bar / notification area.
func trayAvailable() bool { return true }
