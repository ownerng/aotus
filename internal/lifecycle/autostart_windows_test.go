//go:build windows

package lifecycle

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// Runs on the Windows CI runner and on any Windows machine: it uses a scratch
// registry key, never the real Run key.
func TestWindowsAutostartRegistry(t *testing.T) {
	old := runKeyPath
	runKeyPath = `Software\AotusTest\Run`
	t.Cleanup(func() {
		runKeyPath = old
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\AotusTest\Run`)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\AotusTest`)
	})

	a, err := NewAutostart(`C:\Program Files\Aotus\aotusd.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if on, err := a.Enabled(); err != nil || on {
		t.Fatalf("before Enable: %v, %v", on, err)
	}
	if err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := a.Enable(); err != nil {
		t.Fatalf("enabling twice must be harmless: %v", err)
	}
	if on, _ := a.Enabled(); !on {
		t.Fatal("after Enable it must report enabled")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = k.Close() }()
	if v, _, err := k.GetStringValue(runValueName); err != nil || v != `"C:\Program Files\Aotus\aotusd.exe"` {
		t.Fatalf("registered command = %q, %v; the path must be quoted", v, err)
	}
	if err := a.Disable(); err != nil {
		t.Fatal(err)
	}
	if err := a.Disable(); err != nil {
		t.Fatalf("disabling twice must be harmless: %v", err)
	}
	if on, _ := a.Enabled(); on {
		t.Fatal("after Disable it must report disabled")
	}
}
