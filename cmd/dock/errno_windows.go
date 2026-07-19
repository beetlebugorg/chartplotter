//go:build windows

package main

import "golang.org/x/sys/windows"

// errConnRefused is the errno a dial to a closed local port unwraps to.
// errors.Is(err, syscall.ECONNREFUSED) never matches on Windows — Go defines
// that constant as a synthetic APPLICATION_ERROR value, while Winsock dials
// actually fail with WSAECONNREFUSED (10061, absent from stdlib syscall) — so
// match the real thing. Without this the probe classified every refused port
// as "taken by another server" and the dock exhausted 8080..8090 without ever
// spawning the server.
var errConnRefused error = windows.WSAECONNREFUSED
