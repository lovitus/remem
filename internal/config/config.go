package config

import (
	"encoding/json"
	"os"
	"runtime"
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

type NamePatch struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

type FileConfig struct {
	Commands NamePatch `json:"commands"`
	Groups   NamePatch `json:"groups"`
}

type Config struct {
	ScanInterval         time.Duration
	CommandLimitBytes    uint64
	GroupLimitBytes      uint64
	CommandWatchlist     map[string]struct{}
	Groups               []GroupSpec
	MaxInMemoryLogLines  int
	LogHTTPListenAddress string
	ConfigPath           string
	Warnings             []string
}

func Default() Config {
	defaultInterval := 2000
	if runtime.GOOS == "windows" {
		defaultInterval = 3000
	}
	scanInterval := durationFromEnvMs("REMEM_SCAN_INTERVAL_MS", defaultInterval)
	commandLimit := bytesFromEnvGiB("REMEM_COMMAND_LIMIT_GB", 2)
	groupLimit := bytesFromEnvGiB("REMEM_GROUP_LIMIT_GB", 6)

	commands := defaultCommandWatchlistBase()
	groups := defaultGroups()
	warnings := make([]string, 0)

	applyCommandPatch(
		commands,
		parseCSVEnv("REMEM_EXTRA_COMMANDS"),
		parseCSVEnv("REMEM_REMOVE_COMMANDS"),
	)
	groups = applyGroupPatch(
		groups,
		parseCSVEnv("REMEM_EXTRA_GROUPS"),
		parseCSVEnv("REMEM_REMOVE_GROUPS"),
	)

	configPath := strings.TrimSpace(os.Getenv("REMEM_CONFIG_PATH"))
	if configPath != "" {
		fc, err := loadFileConfig(configPath)
		if err != nil {
			warnings = append(warnings, "load config file failed: "+err.Error())
		} else {
			applyCommandPatch(commands, fc.Commands.Add, fc.Commands.Remove)
			groups = applyGroupPatch(groups, fc.Groups.Add, fc.Groups.Remove)
		}
	}

	return Config{
		ScanInterval:         scanInterval,
		CommandLimitBytes:    commandLimit,
		GroupLimitBytes:      groupLimit,
		CommandWatchlist:     commands,
		Groups:               groups,
		MaxInMemoryLogLines:  intFromEnv("REMEM_MAX_LOG_LINES", 400),
		LogHTTPListenAddress: envOrDefault("REMEM_LOG_HTTP_ADDR", "127.0.0.1:0"),
		ConfigPath:           configPath,
		Warnings:             warnings,
	}
}

func normalizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".exe")
	return s
}

func defaultCommandWatchlistBase() map[string]struct{} {
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
	return m
}

func defaultGroups() []GroupSpec {
	return []GroupSpec{
		{
			Name: "codex",
			RootMatchers: []Matcher{
				{NameContains: []string{"codex"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"codex"}},
			},
		},
		{
			Name: "windsurf",
			RootMatchers: []Matcher{
				{NameContains: []string{"windsurf"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"windsurf"}},
			},
		},
		{
			Name: "vscode",
			RootMatchers: []Matcher{
				{NameContains: []string{"visual studio code", "code"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"visual studio code", "code"}},
			},
		},
		{
			Name: "antigravity",
			RootMatchers: []Matcher{
				{NameContains: []string{"antigravity"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"antigravity"}},
			},
		},
		{
			Name: "chrome",
			RootMatchers: []Matcher{
				{NameContains: []string{"google chrome", "chrome"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"google chrome", "chrome"}},
			},
		},
		{
			Name: "firefox",
			RootMatchers: []Matcher{
				{NameContains: []string{"firefox"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"firefox"}},
			},
		},
		{
			Name: "edge",
			RootMatchers: []Matcher{
				{NameContains: []string{"microsoft edge", "msedge", "edge"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"microsoft edge", "msedge", "edge"}},
			},
		},
		{
			Name: "safari",
			RootMatchers: []Matcher{
				{NameContains: []string{"safari"}},
			},
			ProtectMatchers: []Matcher{
				{NameEquals: []string{"safari"}},
			},
		},
	}
}

func applyCommandPatch(dst map[string]struct{}, add, remove []string) {
	for _, c := range add {
		c = normalizeName(c)
		if c == "" {
			continue
		}
		dst[c] = struct{}{}
	}
	for _, c := range remove {
		c = normalizeName(c)
		if c == "" {
			continue
		}
		delete(dst, c)
	}
}

func applyGroupPatch(base []GroupSpec, add, remove []string) []GroupSpec {
	removeSet := make(map[string]struct{}, len(remove))
	for _, r := range remove {
		r = normalizeName(r)
		if r == "" {
			continue
		}
		removeSet[r] = struct{}{}
	}

	out := make([]GroupSpec, 0, len(base)+len(add))
	nameSet := make(map[string]struct{}, len(base)+len(add))
	for _, g := range base {
		key := normalizeName(g.Name)
		if _, removed := removeSet[key]; removed {
			continue
		}
		out = append(out, g)
		nameSet[key] = struct{}{}
	}

	for _, a := range add {
		a = normalizeName(a)
		if a == "" {
			continue
		}
		if _, exists := nameSet[a]; exists {
			continue
		}
		out = append(out, simpleGroup(a))
		nameSet[a] = struct{}{}
	}

	return out
}

func simpleGroup(name string) GroupSpec {
	return GroupSpec{
		Name: name,
		RootMatchers: []Matcher{
			{NameContains: []string{name}},
		},
		ProtectMatchers: []Matcher{
			{NameEquals: []string{name}},
		},
	}
}

func loadFileConfig(path string) (FileConfig, error) {
	var fc FileConfig
	buf, err := os.ReadFile(path)
	if err != nil {
		return fc, err
	}
	if err := json.Unmarshal(buf, &fc); err != nil {
		return fc, err
	}
	return fc, nil
}

func parseCSVEnv(key string) []string {
	return parseCSV(strings.TrimSpace(os.Getenv(key)))
}

func parseCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
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
