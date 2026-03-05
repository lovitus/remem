//go:build !windows

package main

func hideConsoleWindowIfNeeded() {
	// no-op on non-Windows
}
