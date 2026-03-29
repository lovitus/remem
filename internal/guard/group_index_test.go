package guard

import (
	"reflect"
	"sort"
	"testing"

	"remem/internal/config"
)

func TestFindRootPIDsIndexedMatchesLinear(t *testing.T) {
	tests := []struct {
		name  string
		group config.GroupSpec
		procs []Proc
	}{
		{
			name:  "exact single root",
			group: config.GroupSpec{Name: "codex", RootMatchers: []config.Matcher{{NameContains: []string{"codex"}}}},
			procs: []Proc{
				{PID: 10, PPID: 1, NameNorm: "codex"},
				{PID: 11, PPID: 10, NameNorm: "codex-worker"},
				{PID: 12, PPID: 1, NameNorm: "other"},
			},
		},
		{
			name:  "contains long name",
			group: config.GroupSpec{Name: "chrome", RootMatchers: []config.Matcher{{NameContains: []string{"chrome"}}}},
			procs: []Proc{
				{PID: 20, PPID: 1, NameNorm: "google chrome"},
				{PID: 21, PPID: 20, NameNorm: "google chrome helper"},
				{PID: 22, PPID: 1, NameNorm: "notepad"},
			},
		},
		{
			name:  "short token stays strict",
			group: config.GroupSpec{Name: "vscode", RootMatchers: []config.Matcher{{NameContains: []string{"code"}}}},
			procs: []Proc{
				{PID: 30, PPID: 1, NameNorm: "code"},
				{PID: 31, PPID: 1, NameNorm: "xcode-helper"},
				{PID: 32, PPID: 30, NameNorm: "code-insiders"},
			},
		},
		{
			name: "multiple matchers union",
			group: config.GroupSpec{Name: "edge", RootMatchers: []config.Matcher{
				{NameContains: []string{"microsoft edge", "msedge", "edge"}},
			}},
			procs: []Proc{
				{PID: 40, PPID: 1, NameNorm: "msedge"},
				{PID: 41, PPID: 1, NameNorm: "microsoft edge"},
				{PID: 42, PPID: 40, NameNorm: "edge-tab"},
			},
		},
		{
			name:  "nested matched parent suppresses child root",
			group: config.GroupSpec{Name: "firefox", RootMatchers: []config.Matcher{{NameContains: []string{"firefox"}}}},
			procs: []Proc{
				{PID: 50, PPID: 1, NameNorm: "firefox"},
				{PID: 51, PPID: 50, NameNorm: "firefox"},
				{PID: 52, PPID: 51, NameNorm: "firefox-content"},
			},
		},
		{
			name:  "version suffix fallback preserved",
			group: config.GroupSpec{Name: "codex", RootMatchers: []config.Matcher{{NameContains: []string{"codex"}}}},
			procs: []Proc{
				{PID: 60, PPID: 1, NameNorm: "codex-1.2.3"},
				{PID: 61, PPID: 60, NameNorm: "codex-worker"},
			},
		},
		{
			name:  "no matches",
			group: config.GroupSpec{Name: "safari", RootMatchers: []config.Matcher{{NameContains: []string{"safari"}}}},
			procs: []Proc{
				{PID: 70, PPID: 1, NameNorm: "finder"},
				{PID: 71, PPID: 70, NameNorm: "mdworker"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			byPID := make(map[int32]Proc, len(tt.procs))
			for _, p := range tt.procs {
				byPID[p.PID] = p
			}

			got := findRootPIDs(tt.group, byPID, buildProcNameIndex(byPID))
			want := findRootPIDsLinear(tt.group, byPID)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("roots mismatch: got=%v want=%v", got, want)
			}
		})
	}
}

func TestFindRootPIDsFallsBackWithoutIndex(t *testing.T) {
	group := config.GroupSpec{Name: "chrome", RootMatchers: []config.Matcher{{NameContains: []string{"chrome"}}}}
	byPID := map[int32]Proc{
		1: {PID: 1, PPID: 0, NameNorm: "google chrome"},
		2: {PID: 2, PPID: 1, NameNorm: "google chrome helper"},
		3: {PID: 3, PPID: 0, NameNorm: "finder"},
	}

	got := findRootPIDs(group, byPID, nil)
	want := findRootPIDsLinear(group, byPID)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots mismatch without index: got=%v want=%v", got, want)
	}
}

func TestMatchProcIDsIndexedMatchesLinear(t *testing.T) {
	matcher := config.Matcher{NameContains: []string{"codex", "chrome"}}
	byPID := map[int32]Proc{
		1: {PID: 1, PPID: 0, NameNorm: "codex"},
		2: {PID: 2, PPID: 1, NameNorm: "codex-1.2.3"},
		3: {PID: 3, PPID: 0, NameNorm: "google chrome"},
		4: {PID: 4, PPID: 0, NameNorm: "xcode-helper"},
	}

	got := matchProcIDs(matcher, byPID, buildProcNameIndex(byPID))
	want := matchProcIDsLinear(matcher, byPID)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matched pid mismatch: got=%v want=%v", got, want)
	}
}

func findRootPIDsLinear(group config.GroupSpec, byPID map[int32]Proc) []int32 {
	matched := make(map[int32]Proc)
	for pid, p := range byPID {
		if groupRootMatch(group, p) {
			matched[pid] = p
		}
	}
	if len(matched) == 0 {
		return nil
	}

	roots := make([]int32, 0, len(matched))
	for pid, p := range matched {
		if _, parentAlsoMatched := matched[p.PPID]; !parentAlsoMatched {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		for pid := range matched {
			roots = append(roots, pid)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots
}

func matchProcIDsLinear(matcher config.Matcher, byPID map[int32]Proc) []int32 {
	matched := make([]int32, 0, len(byPID))
	for pid, p := range byPID {
		if matcherMatches(matcher, p) {
			matched = append(matched, pid)
		}
	}
	return matched
}
