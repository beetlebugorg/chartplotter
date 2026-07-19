//go:build !windows

package main

import "syscall"

// errConnRefused is the errno a dial to a closed local port unwraps to.
var errConnRefused error = syscall.ECONNREFUSED
