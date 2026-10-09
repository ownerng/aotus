// Command aotusd is the daemon: a single static binary with no window. It
// keeps the employees' CLI sessions running in the background and serves the
// clients (the desktop app and the aotus command) on loopback.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
