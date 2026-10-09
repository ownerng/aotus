package provider

import (
	"context"
	"strings"
	"time"

	"aotus/internal/proc"
)

// onceTimeout bounds short informational commands such as --version.
const onceTimeout = 20 * time.Second

// runOnce runs a short command with an exact environment and returns its
// stdout. Stdin is closed at once.
func runOnce(ctx context.Context, binary string, args, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, onceTimeout)
	defer cancel()
	run, err := proc.Start(ctx, proc.Spec{Path: binary, Args: args, Env: env})
	if err != nil {
		return "", err
	}
	_ = run.Stdin().Close()
	var out []string
	for l := range run.Lines() {
		if l.Stream == proc.Stdout {
			out = append(out, l.Text)
		}
	}
	run.Wait()
	return strings.Join(out, "\n"), nil
}
