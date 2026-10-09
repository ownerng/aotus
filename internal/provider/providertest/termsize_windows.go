//go:build windows

package providertest

// termSize is not available on Windows until the ConPTY work (task P1-021).
func termSize() string { return "n/a" }
