// Command aotus is a small command line client of the daemon, used for
// development, tests and headless use. The product interface is the desktop
// app (cmd/aotus-desktop). It talks to the daemon only through internal/client.
package main

import (
	"context"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
