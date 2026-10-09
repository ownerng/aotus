package datadir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultHonorsEnvironment(t *testing.T) {
	t.Setenv(EnvHome, "/custom/place")
	l, err := Default()
	if err != nil || l.Root != "/custom/place" {
		t.Fatalf("Default = %+v, %v", l, err)
	}
	t.Setenv(EnvHome, "")
	l, err = Default()
	if err != nil || filepath.Base(l.Root) != ".aotus" {
		t.Fatalf("Default without env = %+v, %v; want a .aotus folder", l, err)
	}
}

func TestEnsureCreatesOwnerOnlyFolders(t *testing.T) {
	l := Layout{Root: filepath.Join(t.TempDir(), "data")}
	if err := os.MkdirAll(l.Root, 0o755); err != nil { // pre-existing and too open
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Root, l.ProfilesDir(), l.EmployeesDir(), l.TrashDir()} {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s was not created: %v", dir, err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o, want 700", dir, fi.Mode().Perm())
		}
	}
}

func TestPathsAreUnderRoot(t *testing.T) {
	l := Layout{Root: filepath.Join("r")}
	for _, p := range []string{l.Database(), l.Discovery(), l.Lock(), l.Token(), l.ProfileDir("a"), l.EmployeeDir("b"), l.TrashDir()} {
		if rel, err := filepath.Rel(l.Root, p); err != nil || rel == "" || rel[0] == '.' {
			t.Fatalf("%s is not inside the root", p)
		}
	}
}
