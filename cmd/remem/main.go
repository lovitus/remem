package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"remem/internal/config"
	"remem/internal/guard"
	"remem/internal/logbuf"
	"remem/internal/ui"
)

var version = "dev"

func main() {
	hideConsoleWindowIfNeeded()

	cfg := config.Default()
	logs := logbuf.New(cfg.RoutineLogLines, cfg.ImportantLogLines)
	logs.AddActionf("remem guard starting")
	logs.AddActionf("version=%s scan interval=%s command_limit=%s group_limit=%s", version, cfg.ScanInterval, bytesText(cfg.CommandLimitBytes), bytesText(cfg.GroupLimitBytes))
	logs.AddActionf("watchlist=%d groups=%d groups_per_scan=%d hot_ratio=%.2f hot_ttl=%s routine_logs=%d important_logs=%d", len(cfg.CommandWatchlist), len(cfg.Groups), cfg.GroupsPerScan, cfg.GroupHotThresholdRate, cfg.GroupHotTTL, cfg.RoutineLogLines, cfg.ImportantLogLines)
	if cfg.ConfigPath != "" {
		logs.AddActionf("config file: %s", cfg.ConfigPath)
	}
	for _, w := range cfg.Warnings {
		logs.AddErrorf("config warning: %s", w)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	monitor := guard.New(cfg, logs)
	monitor.Start(ctx)

	server, err := ui.StartLogServer(cfg.LogHTTPListenAddress, logs, monitor)
	if err != nil {
		logs.AddErrorf("log server start failed: %v", err)
		panic(err)
	}
	logs.AddActionf("log ui: %s", server.URL)
	logs.AddActionf("rules ui: %s", server.RulesURL)

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = server.Shutdown(shutdownCtx)
	}()

	ui.RunTray(ui.TrayOptions{
		Monitor:  monitor,
		Logs:     logs,
		LogUIURL: server.URL,
		RulesURL: server.RulesURL,
	})
}

func bytesText(b uint64) string {
	return fmt.Sprintf("%.2fGiB", float64(b)/(1024*1024*1024))
}
