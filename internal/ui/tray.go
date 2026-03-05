package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/getlantern/systray"

	"remem/internal/guard"
	"remem/internal/logbuf"
)

type TrayOptions struct {
	Monitor  *guard.Monitor
	Logs     *logbuf.Buffer
	LogUIURL string
}

func RunTray(opts TrayOptions) {
	systray.Run(func() { onReady(opts) }, onExit)
}

func onReady(opts TrayOptions) {
	if len(trayIconBytes) > 0 {
		systray.SetIcon(trayIconBytes)
	}
	systray.SetTitle("remem")
	systray.SetTooltip("Memory guard for dev tools and browsers")

	mStatus := systray.AddMenuItem("Status: starting...", "live status")
	mStatus.Disable()
	mOpenLog := systray.AddMenuItem("Open Live Logs", "Open in-memory logs")
	mForceScan := systray.AddMenuItem("Force Scan Now", "Trigger a scan immediately")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit remem")

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			st := opts.Monitor.Stats()
			state := "idle"
			if st.Running {
				state = "running"
			}
			line := fmt.Sprintf("Status: %s | procs:%d killed:%d | %dms", state, st.LastProcessSeen, st.LastKilled, st.LastDurationMs)
			mStatus.SetTitle(line)
		}
	}()

	go func() {
		for {
			select {
			case <-mOpenLog.ClickedCh:
				opts.Logs.Addf("open log viewer: %s", opts.LogUIURL)
				if err := openBrowser(opts.LogUIURL); err != nil {
					opts.Logs.Addf("open browser failed: %v", err)
				}
			case <-mForceScan.ClickedCh:
				opts.Logs.Add("manual scan requested")
				opts.Monitor.TriggerScan("manual")
			case <-mQuit.ClickedCh:
				opts.Logs.Add("tray quit requested")
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	// nothing
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
