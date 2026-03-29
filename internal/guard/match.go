package guard

import (
	"fmt"
	"strings"

	"remem/internal/config"
)

func bytesToGiB(b uint64) float64 {
	return float64(b) / (1024 * 1024 * 1024)
}

func formatGiB(b uint64) string {
	return fmt.Sprintf("%.2fGiB", bytesToGiB(b))
}

func matcherMatches(m config.Matcher, p Proc) bool {
	for _, s := range m.NameEquals {
		needle := normalizeProcName(s)
		if needle == "" {
			continue
		}
		if p.NameNorm == needle {
			return true
		}
	}
	for _, s := range m.NameContains {
		needle := normalizeProcName(s)
		if needle == "" {
			continue
		}
		if shouldUseExactNameMatch(needle) {
			if p.NameNorm == needle {
				return true
			}
			continue
		}
		if strings.Contains(p.NameNorm, needle) {
			return true
		}
	}
	for _, s := range m.NameContains {
		needle := normalizeProcName(s)
		if needle == "" {
			continue
		}
		if fuzzyNameContainsMatch(p.NameNorm, needle) {
			return true
		}
	}
	for _, s := range m.ExeContains {
		needle := strings.ToLower(strings.TrimSpace(s))
		if needle == "" {
			continue
		}
		if strings.Contains(p.ExeNorm, needle) {
			return true
		}
	}
	return false
}

func shouldUseExactNameMatch(needle string) bool {
	// Short tokens are too ambiguous for substring matching (e.g. "code", "edge").
	return len(needle) <= 5 && !strings.Contains(needle, " ")
}

func fuzzyNameContainsMatch(nameNorm string, needle string) bool {
	if nameNorm == needle {
		return true
	}

	if trimmed := trimVersionLikeSuffix(nameNorm); trimmed != "" && trimmed != nameNorm {
		if trimmed == needle {
			return true
		}
		if !shouldUseExactNameMatch(needle) && strings.Contains(trimmed, needle) {
			return true
		}
	}

	if shouldUseExactNameMatch(needle) {
		return hasDelimitedAffix(nameNorm, needle)
	}
	return false
}

func hasDelimitedAffix(nameNorm string, needle string) bool {
	if len(nameNorm) <= len(needle) {
		return false
	}
	if strings.HasPrefix(nameNorm, needle) && isNameDelimiter(rune(nameNorm[len(needle)])) {
		return true
	}
	if strings.HasSuffix(nameNorm, needle) && isNameDelimiter(rune(nameNorm[len(nameNorm)-len(needle)-1])) {
		return true
	}
	return false
}

func trimVersionLikeSuffix(nameNorm string) string {
	trimmed := strings.TrimSpace(nameNorm)
	for {
		base, suffix, ok := splitTrailingSegment(trimmed)
		if !ok || !looksVersionLikeSegment(suffix) {
			return trimmed
		}
		trimmed = strings.TrimSpace(base)
	}
}

func splitTrailingSegment(s string) (string, string, bool) {
	if s == "" {
		return "", "", false
	}
	for i := len(s) - 1; i >= 0; i-- {
		if isNameDelimiter(rune(s[i])) {
			if i == len(s)-1 {
				return "", "", false
			}
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func looksVersionLikeSegment(seg string) bool {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return false
	}
	hasDigit := false
	for _, r := range seg {
		if r >= '0' && r <= '9' {
			hasDigit = true
			continue
		}
		if (r >= 'a' && r <= 'z') || r == '.' {
			continue
		}
		return false
	}
	return hasDigit
}

func isNameDelimiter(r rune) bool {
	switch r {
	case ' ', '-', '_', '.', '(', ')', '[', ']', '{', '}':
		return true
	default:
		return false
	}
}

func groupRootMatch(g config.GroupSpec, p Proc) bool {
	for _, m := range g.RootMatchers {
		if matcherMatches(m, p) {
			return true
		}
	}
	return false
}

func groupProtectMatch(g config.GroupSpec, p Proc) bool {
	for _, m := range g.ProtectMatchers {
		if matcherMatches(m, p) {
			return true
		}
	}
	return false
}
