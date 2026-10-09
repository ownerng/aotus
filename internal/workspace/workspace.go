package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"aotus/internal/datadir"
)

// Errors returned by Resolve and Slug. Use errors.Is.
var (
	// ErrEscapes means a path leads outside the folders an employee may use.
	ErrEscapes = errors.New("path is outside the employee's folders")
	// ErrBadName means a name cannot be used as a folder name.
	ErrBadName = errors.New("invalid employee name")
)

// Manager owns the working folders of the employees.
type Manager struct {
	layout datadir.Layout
}

// New returns a manager for a data directory.
func New(l datadir.Layout) *Manager { return &Manager{layout: l} }

var (
	slugChars     = regexp.MustCompile(`[^a-z0-9]+`)
	reservedNames = map[string]bool{
		// Names Windows reserves for devices, whatever the extension.
		"con": true, "prn": true, "aux": true, "nul": true,
		"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
		"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
	}
)

// accents maps the common Latin accented letters to plain ones, so that
// "Revisión" becomes "revision" and not "revisi-n". The standard
// library has no Unicode normalization, and these cover Spanish, Portuguese,
// French, Italian and German names.
var accents = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o", "ø", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c", "ß", "ss", "æ", "ae", "œ", "oe",
)

// maxSlug keeps folder names (and the paths below them) short enough for every
// operating system.
const maxSlug = 48

// Slug turns an employee's display name into the name of its folder: lowercase
// letters and digits (accents folded) separated by single hyphens. Two names that differ only in case,
// accents or punctuation give the same slug, which is what stops two employees
// from sharing a folder on case-insensitive file systems.
func Slug(name string) (string, error) {
	s := strings.Trim(slugChars.ReplaceAllString(accents.Replace(strings.ToLower(name)), "-"), "-")
	if s == "" {
		return "", fmt.Errorf("%w: %q has no letters or digits", ErrBadName, name)
	}
	if len(s) > maxSlug {
		s = strings.TrimRight(s[:maxSlug], "-")
	}
	if reservedNames[s] {
		return "", fmt.Errorf("%w: %q is reserved by the operating system", ErrBadName, s)
	}
	return s, nil
}

// Dir is the folder of the employee with this slug.
func (m *Manager) Dir(slug string) (string, error) {
	if err := checkSlug(slug); err != nil {
		return "", err
	}
	return m.layout.EmployeeDir(slug), nil
}

func checkSlug(slug string) error {
	if got, err := Slug(slug); err != nil || got != slug {
		return fmt.Errorf("%w: %q is not a folder name produced by Slug", ErrBadName, slug)
	}
	return nil
}

// Create makes the employee's folder (owner-only) and its private temporary
// directory. It is safe to call again.
func (m *Manager) Create(slug string) (string, error) {
	dir, err := m.Dir(slug)
	if err != nil {
		return "", err
	}
	if err := m.layout.Ensure(); err != nil {
		return "", err
	}
	for _, d := range []string{dir, filepath.Join(dir, tmpName)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", fmt.Errorf("workspace: creating %s: %w", d, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(d, 0o700); err != nil { //nolint:gosec // directories need the execute bit; 0700 is owner-only
				return "", fmt.Errorf("workspace: restricting %s: %w", d, err)
			}
		}
	}
	return dir, nil
}

// tmpName is the employee's private temporary directory inside its folder.
const tmpName = ".tmp"

// Resolve checks a path an employee (or the model driving it) wants to use
// and returns it as an absolute, cleaned path. The path may be relative to the
// employee's folder or absolute, and must stay inside the folder or inside one
// of the extra directories the user granted. ".." segments are rejected, and
// so are symbolic links that lead outside, including links in the middle of
// a path that does not exist yet.
func (m *Manager) Resolve(slug, path string, granted ...string) (string, error) {
	dir, err := m.Dir(slug)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrEscapes)
	}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	target = filepath.Clean(target)

	roots := append([]string{dir}, granted...)
	for _, root := range roots {
		if !inside(root, target) {
			continue
		}
		ok, err := insideAfterLinks(root, target)
		if err != nil {
			return "", err
		}
		if ok {
			return target, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrEscapes, path)
}

// inside reports whether target is root or lies under it, comparing cleaned
// paths textually (this also rejects "../" climbs, which Clean resolves).
func inside(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// insideAfterLinks resolves symbolic links in the longest part of target that
// exists and checks that the real location is still inside the real root.
func insideAfterLinks(root, target string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			realRoot = filepath.Clean(root) // nothing to follow yet
		} else {
			return false, fmt.Errorf("workspace: resolving %s: %w", root, err)
		}
	}
	existing, rest := target, ""
	for {
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			return inside(realRoot, filepath.Join(real, rest)), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("workspace: resolving %s: %w", existing, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false, nil
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

// Remove moves the employee's folder to the trash instead of deleting it, so a
// mistake can be undone. It returns the new location.
func (m *Manager) Remove(slug string) (string, error) {
	dir, err := m.Dir(slug)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("workspace: %s: %w", dir, err)
	}
	if err := os.MkdirAll(m.layout.TrashDir(), 0o700); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for i := 0; ; i++ {
		name := slug + "-" + stamp
		if i > 0 {
			name = fmt.Sprintf("%s-%d", name, i)
		}
		dest := filepath.Join(m.layout.TrashDir(), name)
		if _, err := os.Lstat(dest); err == nil {
			continue // taken: try the next suffix
		}
		if err := os.Rename(dir, dest); err != nil {
			return "", fmt.Errorf("workspace: moving %s to the trash: %w", dir, err)
		}
		return dest, nil
	}
}
