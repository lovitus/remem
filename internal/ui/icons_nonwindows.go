//go:build !windows

package ui

import _ "embed"

//go:embed assets/remem.png
var trayIconBytes []byte
