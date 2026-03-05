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
