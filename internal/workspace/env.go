package workspace

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Env returns the variables that tie a child process to its employee: its own
// temporary directory (so scratch files stay inside the folder) and the names
// of the employee and the folder. They are added on top of the provider's
// allow-listed environment.
func (m *Manager) Env(slug string) ([]string, error) {
	dir, err := m.Dir(slug)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(dir, tmpName)
	return []string{
		"AOTUS_EMPLOYEE=" + slug,
		"AOTUS_WORKSPACE=" + dir,
		"TMPDIR=" + tmp, // Unix
		"TEMP=" + tmp,   // Windows
		"TMP=" + tmp,
	}, nil
}

// Merge combines environments; a later value for a name replaces an earlier
// one. The result is sorted, so it is deterministic.
func Merge(parts ...[]string) []string {
	byName := map[string]string{}
	for _, env := range parts {
		for _, kv := range env {
			if name, _, ok := strings.Cut(kv, "="); ok && name != "" {
				byName[key(name)] = kv
			}
		}
	}
	out := make([]string, 0, len(byName))
	for _, kv := range byName {
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}

// Scrub keeps only the variables whose names are in allowed (and any whose name
// starts with an allowed prefix ending in "*"). It is the last line of defense
// before an environment reaches a child process: whatever an earlier step let
// in, a variable that is not on the list does not get through.
func Scrub(env []string, allowed []string) []string {
	exact := map[string]bool{}
	var prefixes []string
	for _, a := range allowed {
		if p, ok := strings.CutSuffix(a, "*"); ok {
			prefixes = append(prefixes, key(p))
		} else {
			exact[key(a)] = true
		}
	}
	var out []string
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		k := key(name)
		keep := exact[k]
		for _, p := range prefixes {
			keep = keep || strings.HasPrefix(k, p)
		}
		if keep {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}

// key normalizes a variable name: Windows ignores case in them.
func key(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
