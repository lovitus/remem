package guard

import (
	"testing"

	"remem/internal/config"
)

func TestMatcherMatchesExactBeforeFuzzy(t *testing.T) {
	m := config.Matcher{NameContains: []string{"codex"}}
	p := Proc{NameNorm: "codex"}
	if !matcherMatches(m, p) {
		t.Fatalf("expected exact match to succeed")
	}
}

func TestMatcherMatchesFuzzyVersionSuffixForShortName(t *testing.T) {
	m := config.Matcher{NameContains: []string{"codex"}}
	p := Proc{NameNorm: "codex-1.2.3"}
	if !matcherMatches(m, p) {
		t.Fatalf("expected fuzzy suffix match to succeed")
	}
}

func TestMatcherMatchesFuzzyVersionSuffixForLongName(t *testing.T) {
	m := config.Matcher{NameContains: []string{"firefox"}}
	p := Proc{NameNorm: "firefox-135.0.1"}
	if !matcherMatches(m, p) {
		t.Fatalf("expected long-name fuzzy suffix match to succeed")
	}
}

func TestMatcherMatchesDoesNotUseMidStringContainsForShortName(t *testing.T) {
	m := config.Matcher{NameContains: []string{"code"}}
	p := Proc{NameNorm: "xcode-helper"}
	if matcherMatches(m, p) {
		t.Fatalf("expected short-name fallback to avoid mid-string false positive")
	}
}

func TestMatcherMatchesShortNameAllowsDelimitedSuffix(t *testing.T) {
	m := config.Matcher{NameContains: []string{"code"}}
	p := Proc{NameNorm: "code-insiders"}
	if !matcherMatches(m, p) {
		t.Fatalf("expected short-name delimited suffix match to succeed")
	}
}
