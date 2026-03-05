//go:build windows

package main

import (
	"os"
	"syscall"
)

func hideConsoleWindowIfNeeded() {
	if os.Getenv("REMEM_SHOW_CONSOLE") == "1" {
		return
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	showWindow := user32.NewProc("ShowWindow")

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	const swHide = 0
	showWindow.Call(hwnd, swHide)
}
