package provider

import (
	"regexp"
	"strconv"
)

// MinVersion is the oldest CLI version each adapter was tested with
// (docs/research/cli-contracts.md). Older ones get CodeUnsupportedVersion.
var MinVersion = map[Kind]string{
	KindClaude: "2.1.295",
	KindCodex:  "0.160.0",
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion finds the first x.y.z in a CLI's --version output, for example
// "2.1.295 (Claude Code)" or "codex-cli 0.160.0".
func ParseVersion(output string) (string, bool) {
	m := versionPattern.FindString(output)
	return m, m != ""
}

// CompareVersions returns -1, 0 or 1 comparing two x.y.z versions. Strings
// that are not versions compare as 0.
func CompareVersions(a, b string) int {
	pa, pb := versionPattern.FindStringSubmatch(a), versionPattern.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return 0
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}
