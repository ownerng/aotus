package orchestrator

import (
	"os"
	"testing"

	"aotus/internal/provider/providertest"
)

// The test binary doubles as the fake CLI that employees run.
func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}
