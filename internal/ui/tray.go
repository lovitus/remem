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
	RulesURL string
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
	mEditRules := systray.AddMenuItem("Edit Rules", "Open rules editor")
	mForceScan := systray.AddMenuItem("Force Scan Now", "Trigger a scan immediately")
	mCollectPerf := systray.AddMenuItem("Collect 10s Perf Data", "Collect performance logs for 10 seconds and open the log viewer")
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
		var perfReset <-chan time.Time
		var perfTimer *time.Timer
		for {
			select {
			case <-mOpenLog.ClickedCh:
				opts.Logs.AddActionf("open log viewer: %s", opts.LogUIURL)
				if err := openBrowser(opts.LogUIURL); err != nil {
					opts.Logs.AddErrorf("open browser failed: %v", err)
				}
			case <-mEditRules.ClickedCh:
				opts.Logs.AddActionf("open rules editor: %s", opts.RulesURL)
				if err := openBrowser(opts.RulesURL); err != nil {
					opts.Logs.AddErrorf("open browser failed: %v", err)
				}
			case <-mForceScan.ClickedCh:
				opts.Logs.AddActionf("manual scan requested")
				opts.Monitor.TriggerScan("manual")
			case <-mCollectPerf.ClickedCh:
				until := opts.Monitor.StartPerfCapture(10 * time.Second)
				opts.Logs.AddActionf("perf capture enabled for 10s until %s", until.Format("2006-01-02 15:04:05"))
				opts.Monitor.TriggerScan("perf_capture")
				if err := openBrowser(opts.LogUIURL); err != nil {
					opts.Logs.AddErrorf("open browser failed: %v", err)
				}
				mCollectPerf.SetTitle("Collect 10s Perf Data (running)")
				if perfTimer != nil {
					if !perfTimer.Stop() {
						select {
						case <-perfTimer.C:
						default:
						}
					}
				}
				perfTimer = time.NewTimer(10 * time.Second)
				perfReset = perfTimer.C
			case <-perfReset:
				mCollectPerf.SetTitle("Collect 10s Perf Data")
				perfReset = nil
			case <-mQuit.ClickedCh:
				opts.Logs.AddActionf("tray quit requested")
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
