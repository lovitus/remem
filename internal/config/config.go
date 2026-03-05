package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Matcher struct {
	NameEquals   []string
	NameContains []string
	ExeContains  []string
}

type GroupSpec struct {
	Name            string
	RootMatchers    []Matcher
	ProtectMatchers []Matcher
}

type Config struct {
	ScanInterval         time.Duration
	CommandLimitBytes    uint64
	GroupLimitBytes      uint64
	CommandWatchlist     map[string]struct{}
	Groups               []GroupSpec
	MaxInMemoryLogLines  int
	LogHTTPListenAddress string
}

func Default() Config {
	scanInterval := durationFromEnvMs("REMEM_SCAN_INTERVAL_MS", 2000)
	commandLimit := bytesFromEnvGiB("REMEM_COMMAND_LIMIT_GB", 2)
	groupLimit := bytesFromEnvGiB("REMEM_GROUP_LIMIT_GB", 6)

	return Config{
		ScanInterval:         scanInterval,
		CommandLimitBytes:    commandLimit,
		GroupLimitBytes:      groupLimit,
		CommandWatchlist:     defaultCommandWatchlist(),
		Groups:               defaultGroups(),
		MaxInMemoryLogLines:  intFromEnv("REMEM_MAX_LOG_LINES", 400),
		LogHTTPListenAddress: envOrDefault("REMEM_LOG_HTTP_ADDR", "127.0.0.1:0"),
	}
}

func normalizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".exe")
	return s
}

func defaultCommandWatchlist() map[string]struct{} {
	// Commands that should never be allowed to balloon to multi-GB RSS.
	base := []string{
		"sed", "awk", "gawk", "mawk", "nawk",
		"grep", "egrep", "fgrep", "rg", "ripgrep",
		"vi", "vim", "nvim", "nano", "less", "more", "ed", "ex",
		"cat", "head", "tail", "sort", "uniq", "cut", "tr", "wc", "tee",
		"find", "xargs", "jq", "yq",
		"sh", "bash", "zsh", "fish", "dash", "ksh",
		"python", "python3", "node", "npm", "npx", "perl", "ruby", "lua",
		"tar", "gzip", "gunzip", "zip", "unzip", "scp", "rsync",
	}

	m := make(map[string]struct{}, len(base))
	for _, c := range base {
		m[normalizeName(c)] = struct{}{}
	}
	// Optional runtime extension: REMEM_EXTRA_COMMANDS="cmd1,cmd2,cmd3".
	for _, c := range strings.Split(os.Getenv("REMEM_EXTRA_COMMANDS"), ",") {
		c = normalizeName(c)
		if c == "" {
			continue
		}
		m[c] = struct{}{}
	}
	return m
}

func defaultGroups() []GroupSpec {
	return []GroupSpec{
		{
			Name: "codex",
			RootMatchers: []Matcher{
				{NameContains: []string{"codex"}},
				{ExeContains: []string{"/applications/codex.app/"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"codex"}},
			},
		},
		{
			Name: "windsurf",
			RootMatchers: []Matcher{
				{NameContains: []string{"windsurf"}},
				{ExeContains: []string{"/applications/windsurf.app/"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"windsurf"}},
			},
		},
		{
			Name: "vscode",
			RootMatchers: []Matcher{
				{NameContains: []string{"visual studio code", "code"}},
				{ExeContains: []string{"visual studio code.app", "code.exe", "vscode"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"visual studio code", "code"}},
			},
		},
		{
			Name: "antigravity",
			RootMatchers: []Matcher{
				{NameContains: []string{"antigravity"}},
				{ExeContains: []string{"antigravity"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"antigravity"}},
			},
		},
		{
			Name: "chrome",
			RootMatchers: []Matcher{
				{NameContains: []string{"google chrome", "chrome"}},
				{ExeContains: []string{"google chrome", "chrome.exe", "/chrome"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"google chrome", "chrome"}},
			},
		},
		{
			Name: "firefox",
			RootMatchers: []Matcher{
				{NameContains: []string{"firefox"}},
				{ExeContains: []string{"firefox"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"firefox"}},
			},
		},
		{
			Name: "edge",
			RootMatchers: []Matcher{
				{NameContains: []string{"microsoft edge", "msedge", "edge"}},
				{ExeContains: []string{"microsoft edge", "msedge.exe"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"microsoft edge", "msedge", "edge"}},
			},
		},
		{
			Name: "safari",
			RootMatchers: []Matcher{
				{NameContains: []string{"safari"}},
				{ExeContains: []string{"safari.app"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"safari"}},
			},
		},
	}
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func durationFromEnvMs(key string, fallbackMs int) time.Duration {
	ms := intFromEnv(key, fallbackMs)
	return time.Duration(ms) * time.Millisecond
}

func bytesFromEnvGiB(key string, fallbackGiB int) uint64 {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			return uint64(v * 1024 * 1024 * 1024)
		}
	}
	return uint64(fallbackGiB) * 1024 * 1024 * 1024
}
