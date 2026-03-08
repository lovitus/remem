package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	Add       []string           `json:"add"`
	Remove    []string           `json:"remove"`
	LimitsGiB map[string]float64 `json:"limitsGiB,omitempty"`
}

type LimitPatch struct {
	CommandGiB float64 `json:"commandGiB,omitempty"`
	GroupGiB   float64 `json:"groupGiB,omitempty"`
}

type FileConfig struct {
	Limits   LimitPatch `json:"limits,omitempty"`
	Commands NamePatch  `json:"commands"`
	Groups   NamePatch  `json:"groups"`
}

type Config struct {
	ScanInterval         time.Duration
	CommandLimitBytes    uint64
	GroupLimitBytes      uint64
	CommandWatchlist     map[string]struct{}
	Groups               []GroupSpec
	RoutineLogLines      int
	ImportantLogLines    int
	LogHTTPListenAddress string
	ConfigPath           string
	Warnings             []string

	EnvPatch              FileConfig
	CustomPatch           FileConfig
	GroupsPerScan         int
	GroupHotThresholdRate float64
	GroupHotTTL           time.Duration
}

func Default() Config {
	defaultInterval := 2000
	defaultGroupsPerScan := 0
	if runtime.GOOS == "windows" {
		defaultInterval = 3000
		defaultGroupsPerScan = 2
	}
	scanInterval := durationFromEnvMs("REMEM_SCAN_INTERVAL_MS", defaultInterval)
	commandLimit := bytesFromEnvGiB("REMEM_COMMAND_LIMIT_GB", 2)
	groupLimit := bytesFromEnvGiB("REMEM_GROUP_LIMIT_GB", 6)

	envPatch := FileConfig{
		Commands: NamePatch{
			Add:    parseCSVEnv("REMEM_EXTRA_COMMANDS"),
			Remove: parseCSVEnv("REMEM_REMOVE_COMMANDS"),
		},
		Groups: NamePatch{
			Add:    parseCSVEnv("REMEM_EXTRA_GROUPS"),
			Remove: parseCSVEnv("REMEM_REMOVE_GROUPS"),
		},
	}
	envPatch = NormalizeFileConfig(envPatch)

	configPath := strings.TrimSpace(os.Getenv("REMEM_CONFIG_PATH"))
	if configPath == "" {
		configPath = defaultConfigPath()
	}

	warnings := make([]string, 0)
	customPatch, err := LoadFileConfig(configPath)
	if err != nil {
		warnings = append(warnings, "load config file failed: "+err.Error())
	}

	customPatch = NormalizeFileConfig(customPatch)
	commands, groups := BuildRuleSet(envPatch, customPatch)

	return Config{
		ScanInterval:          scanInterval,
		CommandLimitBytes:     commandLimit,
		GroupLimitBytes:       groupLimit,
		CommandWatchlist:      commands,
		Groups:                groups,
		RoutineLogLines:       intFromEnv("REMEM_ROUTINE_LOG_LINES", 10),
		ImportantLogLines:     intFromEnv("REMEM_IMPORTANT_LOG_LINES", 100),
		LogHTTPListenAddress:  envOrDefault("REMEM_LOG_HTTP_ADDR", "127.0.0.1:0"),
		ConfigPath:            configPath,
		Warnings:              warnings,
		EnvPatch:              envPatch,
		CustomPatch:           customPatch,
		GroupsPerScan:         intFromEnv("REMEM_GROUPS_PER_SCAN", defaultGroupsPerScan),
		GroupHotThresholdRate: floatFromEnv("REMEM_GROUP_HOT_RATIO", 0.70),
		GroupHotTTL:           durationFromEnvSec("REMEM_GROUP_HOT_TTL_SEC", 30),
	}
}

func BuildRuleSet(envPatch FileConfig, customPatch FileConfig) (map[string]struct{}, []GroupSpec) {
	commands := defaultCommandWatchlistBase()
	groups := defaultGroups()

	applyCommandPatch(commands, envPatch.Commands.Add, envPatch.Commands.Remove)
	groups = applyGroupPatch(groups, envPatch.Groups.Add, envPatch.Groups.Remove)

	applyCommandPatch(commands, customPatch.Commands.Add, customPatch.Commands.Remove)
	groups = applyGroupPatch(groups, customPatch.Groups.Add, customPatch.Groups.Remove)

	return commands, groups
}

func LoadFileConfig(path string) (FileConfig, error) {
	var fc FileConfig
	if strings.TrimSpace(path) == "" {
		return fc, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fc, nil
		}
		return fc, err
	}
	if len(strings.TrimSpace(string(buf))) == 0 {
		return fc, nil
	}
	if err := json.Unmarshal(buf, &fc); err != nil {
		return fc, err
	}
	return NormalizeFileConfig(fc), nil
}

func SaveFileConfig(path string, fc FileConfig) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(NormalizeFileConfig(fc), "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	return os.WriteFile(path, buf, 0644)
}

func NormalizeFileConfig(in FileConfig) FileConfig {
	out := FileConfig{
		Limits: LimitPatch{
			CommandGiB: positiveGiBOrZero(in.Limits.CommandGiB),
			GroupGiB:   positiveGiBOrZero(in.Limits.GroupGiB),
		},
		Commands: NamePatch{
			Add:       normalizeNameList(in.Commands.Add),
			Remove:    normalizeNameList(in.Commands.Remove),
			LimitsGiB: normalizeLimitMap(in.Commands.LimitsGiB),
		},
		Groups: NamePatch{
			Add:       normalizeNameList(in.Groups.Add),
			Remove:    normalizeNameList(in.Groups.Remove),
			LimitsGiB: normalizeLimitMap(in.Groups.LimitsGiB),
		},
	}
	return out
}

func BuildLimitSet(baseCommandLimitBytes, baseGroupLimitBytes uint64, patch FileConfig, commands map[string]struct{}, groups []GroupSpec) (uint64, uint64, map[string]uint64, map[string]uint64) {
	commandLimit := baseCommandLimitBytes
	groupLimit := baseGroupLimitBytes
	if patch.Limits.CommandGiB > 0 {
		commandLimit = bytesFromGiB(patch.Limits.CommandGiB)
	}
	if patch.Limits.GroupGiB > 0 {
		groupLimit = bytesFromGiB(patch.Limits.GroupGiB)
	}

	commandOverrides := make(map[string]uint64)
	for name, gib := range patch.Commands.LimitsGiB {
		n := normalizeName(name)
		if n == "" || gib <= 0 {
			continue
		}
		if _, ok := commands[n]; !ok {
			continue
		}
		commandOverrides[n] = bytesFromGiB(gib)
	}

	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		n := normalizeName(g.Name)
		if n == "" {
			continue
		}
		groupSet[n] = struct{}{}
	}

	groupOverrides := make(map[string]uint64)
	for name, gib := range patch.Groups.LimitsGiB {
		n := normalizeName(name)
		if n == "" || gib <= 0 {
			continue
		}
		if _, ok := groupSet[n]; !ok {
			continue
		}
		groupOverrides[n] = bytesFromGiB(gib)
	}

	return commandLimit, groupLimit, commandOverrides, groupOverrides
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		home, _ := os.UserHomeDir()
		dir = home
	}
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, "remem", "rules.json")
}

func normalizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".exe")
	return s
}

func normalizeNameList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		n := normalizeName(raw)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func normalizeLimitMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for rawName, rawGiB := range in {
		name := normalizeName(rawName)
		gib := positiveGiBOrZero(rawGiB)
		if name == "" || gib == 0 {
			continue
		}
		out[name] = gib
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func positiveGiBOrZero(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return v
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

func DefaultCommandNames() []string {
	m := defaultCommandWatchlistBase()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
			Name:            "windsurf",
			RootMatchers:    []Matcher{{NameContains: []string{"windsurf"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"windsurf"}}},
		},
		{
			Name:            "vscode",
			RootMatchers:    []Matcher{{NameContains: []string{"visual studio code", "code"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"visual studio code", "code"}}},
		},
		{
			Name:            "antigravity",
			RootMatchers:    []Matcher{{NameContains: []string{"antigravity"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"antigravity"}}},
		},
		{
			Name:            "chrome",
			RootMatchers:    []Matcher{{NameContains: []string{"google chrome", "chrome"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"google chrome", "chrome"}}},
		},
		{
			Name:            "firefox",
			RootMatchers:    []Matcher{{NameContains: []string{"firefox"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"firefox"}}},
		},
		{
			Name:            "edge",
			RootMatchers:    []Matcher{{NameContains: []string{"microsoft edge", "msedge", "edge"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"microsoft edge", "msedge", "edge"}}},
		},
		{
			Name:            "safari",
			RootMatchers:    []Matcher{{NameContains: []string{"safari"}}},
			ProtectMatchers: []Matcher{{NameEquals: []string{"safari"}}},
		},
	}
}

func DefaultGroupNames() []string {
	g := defaultGroups()
	out := make([]string, 0, len(g))
	for _, it := range g {
		out = append(out, it.Name)
	}
	sort.Strings(out)
	return out
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

func floatFromEnv(key string, fallback float64) float64 {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 && v <= 1 {
			return v
		}
	}
	return fallback
}

func durationFromEnvMs(key string, fallbackMs int) time.Duration {
	ms := intFromEnv(key, fallbackMs)
	return time.Duration(ms) * time.Millisecond
}

func durationFromEnvSec(key string, fallbackSec int) time.Duration {
	sec := intFromEnv(key, fallbackSec)
	return time.Duration(sec) * time.Second
}

func bytesFromEnvGiB(key string, fallbackGiB int) uint64 {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			return bytesFromGiB(v)
		}
	}
	return bytesFromGiB(float64(fallbackGiB))
}

func bytesFromGiB(v float64) uint64 {
	return uint64(v * 1024 * 1024 * 1024)
}
