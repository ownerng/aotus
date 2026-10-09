package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aotus/internal/datadir"
)

func newManager(t *testing.T) (*Manager, datadir.Layout) {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	return New(l), l
}

func TestSlug(t *testing.T) {
	for name, want := range map[string]string{ // an empty want means "has no usable characters"
		"Atlas":             "atlas",
		"  Code Reviewer  ": "code-reviewer",
		"Revisión & Datos":  "revision-datos",
		"Niño Çelik":        "nino-celik",
		"日本語":               "",
		"a/b\\c":            "a-b-c",
		"ALICE":             "alice",
	} {
		got, err := Slug(name)
		if want == "" {
			if !errors.Is(err, ErrBadName) {
				t.Errorf("Slug(%q) = %q, %v; want ErrBadName", name, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Slug(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	long, _ := Slug(strings.Repeat("x", 200))
	if len(long) != maxSlug {
		t.Errorf("a long name must be cut to %d characters, got %d", maxSlug, len(long))
	}
	for _, bad := range []string{"", "   ", "...", "///", "CON", "nul", "Com1"} {
		if _, err := Slug(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Slug(%q) = %v, want ErrBadName", bad, err)
		}
	}
	a, _ := Slug("Árbol")
	b, _ := Slug("arbol")
	if a != b {
		t.Errorf("names that differ only in accents must collide so they cannot share a folder: %q vs %q", a, b)
	}
}

func TestEmployeeFolderCreatedInsideDataDir(t *testing.T) {
	m, l := newManager(t)
	dir, err := m.Create("atlas")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(l.Root, "employees", "atlas"); dir != want {
		t.Fatalf("folder = %s, want %s", dir, want)
	}
	for _, p := range []string{dir, filepath.Join(dir, tmpName)} {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s was not created: %v", p, err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o, want 700", p, fi.Mode().Perm())
		}
	}
	if again, err := m.Create("atlas"); err != nil || again != dir {
		t.Fatalf("Create must be repeatable: %s, %v", again, err)
	}
	// Only names produced by Slug are accepted: no way to name a folder
	// outside employees/.
	for _, bad := range []string{"../evil", "a/b", "", "Atlas", ".", ".."} {
		if _, err := m.Create(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Create(%q) = %v, want ErrBadName", bad, err)
		}
	}
}

func TestPathEscapeIsRejected(t *testing.T) {
	m, l := newManager(t)
	dir, _ := m.Create("atlas")
	other, _ := m.Create("bob")
	granted := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}

	ok := map[string]string{
		"notes.md":                    filepath.Join(dir, "notes.md"),
		"sub/deeper/file.txt":         filepath.Join(dir, "sub", "deeper", "file.txt"),
		"./a/../b.txt":                filepath.Join(dir, "b.txt"), // stays inside after cleaning
		filepath.Join(dir, "abs.txt"): filepath.Join(dir, "abs.txt"),
		".":                           dir,
	}
	for in, want := range ok {
		if got, err := m.Resolve("atlas", in); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := m.Resolve("atlas", filepath.Join(granted, "main.go"), granted); err != nil || got != filepath.Join(granted, "main.go") {
		t.Errorf("a granted directory must be usable: %q, %v", got, err)
	}

	bad := []string{
		"",
		"..",
		"../bob/secret.txt",
		"sub/../../bob/secret.txt",
		filepath.Join(other, "secret.txt"), // another employee's folder
		filepath.Join(l.Root, "aotus.db"),  // the database
		filepath.Join(l.Root, "token"),     // the API token
		filepath.Join(filepath.Dir(dir), "bob"),
		filepath.Join(granted, "main.go"), // not granted this time
	}
	for _, in := range bad {
		if got, err := m.Resolve("atlas", in); !errors.Is(err, ErrEscapes) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrEscapes", in, got, err)
		}
	}
	// A sibling whose name starts with the same text is not "inside".
	sibling := dir + "-evil"
	if _, err := m.Resolve("atlas", filepath.Join(sibling, "x")); !errors.Is(err, ErrEscapes) {
		t.Errorf("%s must not count as inside %s", sibling, dir)
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	m, _ := newManager(t)
	dir, _ := m.Create("atlas")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "file-link")); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(dir, "real")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(dir, "ok-link")); err != nil {
		t.Fatal(err)
	}

	for _, in := range []string{
		"link",                     // the link itself leads out
		"link/secret.txt",          // through the link
		"link/not/created/yet.txt", // through the link, below a path that does not exist
		"file-link",                // a file link pointing out
		"sub/../link/secret.txt",   // cleaned first, still through the link
	} {
		if got, err := m.Resolve("atlas", in); !errors.Is(err, ErrEscapes) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrEscapes", in, got, err)
		}
	}
	if _, err := m.Resolve("atlas", "ok-link/file.txt"); err != nil {
		t.Errorf("a link that stays inside the folder is fine: %v", err)
	}
	if _, err := m.Resolve("atlas", "new-dir/new-file.txt"); err != nil {
		t.Errorf("paths that do not exist yet are fine when nothing leads out: %v", err)
	}
}

func TestEnvironmentIsScrubbed(t *testing.T) {
	m, _ := newManager(t)
	dir, _ := m.Create("atlas")
	own, err := m.Env("atlas")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range own {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if env["AOTUS_EMPLOYEE"] != "atlas" || env["AOTUS_WORKSPACE"] != dir {
		t.Fatalf("employee variables = %v", env)
	}
	for _, name := range []string{"TMPDIR", "TEMP", "TMP"} {
		if !strings.HasPrefix(env[name], dir) {
			t.Errorf("%s = %q, must point inside the employee's folder %q", name, env[name], dir)
		}
	}

	daemon := []string{
		"PATH=/usr/bin", "HOME=/home/u", "TMPDIR=/tmp",
		"ANTHROPIC_API_KEY=sk-ant-secret", "OPENAI_API_KEY=sk-secret", "GITHUB_TOKEN=ghp_x",
		"AWS_SECRET_ACCESS_KEY=aws", "SSH_AUTH_SOCK=/run/ssh", "AOTUS_TOKEN=local-api-token",
		"malformed-entry-without-equals",
	}
	merged := Merge(daemon, own) // the employee's TMPDIR must win over the daemon's
	got := Scrub(merged, []string{"PATH", "HOME", "TMPDIR", "TEMP", "TMP", "AOTUS_EMPLOYEE", "AOTUS_WORKSPACE"})
	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PATH", "HOME", "TMPDIR", "TEMP", "TMP", "AOTUS_EMPLOYEE", "AOTUS_WORKSPACE":
		default:
			t.Errorf("%s got through the scrub", kv)
		}
		if strings.Contains(kv, "secret") || strings.Contains(kv, "ghp_") || strings.Contains(kv, "local-api-token") {
			t.Errorf("a secret got through: %s", kv)
		}
	}
	if !contains(got, "TMPDIR="+filepath.Join(dir, tmpName)) {
		t.Errorf("the employee's TMPDIR must replace the daemon's: %v", got)
	}
	if !contains(got, "PATH=/usr/bin") || !contains(got, "HOME=/home/u") {
		t.Errorf("allowed variables must survive: %v", got)
	}

	// Prefix patterns end in "*".
	pref := Scrub([]string{"AOTUS_A=1", "AOTUS_B=2", "OTHER=3"}, []string{"AOTUS_*"})
	if len(pref) != 2 {
		t.Errorf("prefix pattern kept %v", pref)
	}
	if again := Merge(daemon, own); strings.Join(again, "|") != strings.Join(merged, "|") {
		t.Error("Merge must be deterministic")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestRemovingEmployeeMovesFolderToTrash(t *testing.T) {
	m, l := newManager(t)
	dir, _ := m.Create("atlas")
	keep := filepath.Join(dir, "work.md")
	if err := os.WriteFile(keep, []byte("important"), 0o600); err != nil {
		t.Fatal(err)
	}

	trashed, err := m.Remove("atlas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder must be gone from employees/: %v", err)
	}
	if filepath.Dir(trashed) != l.TrashDir() || !strings.HasPrefix(filepath.Base(trashed), "atlas-") {
		t.Fatalf("trashed at %s, want a folder named atlas-<time> in %s", trashed, l.TrashDir())
	}
	if b, err := os.ReadFile(filepath.Join(trashed, "work.md")); err != nil || string(b) != "important" {
		t.Fatalf("the files must survive in the trash: %q, %v", b, err)
	}

	// Same name removed again in the same second: no collision, nothing lost.
	if _, err := m.Create("atlas"); err != nil {
		t.Fatal(err)
	}
	second, err := m.Remove("atlas")
	if err != nil {
		t.Fatal(err)
	}
	if second == trashed {
		t.Fatal("a second removal must not overwrite the first")
	}
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("the first trashed folder must still exist: %v", err)
	}
	if _, err := m.Remove("atlas"); err == nil {
		t.Fatal("removing a missing folder must be an error")
	}
	if _, err := m.Remove("../employees"); !errors.Is(err, ErrBadName) {
		t.Fatalf("Remove must only accept slugs, got %v", err)
	}
}
