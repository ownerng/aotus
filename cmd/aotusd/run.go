package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"aotus/internal/api"
	"aotus/internal/credstore"
	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
	"aotus/internal/netaccess"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/store"
	"aotus/internal/version"
	"aotus/internal/workspace"
)

// shutdownGrace is how long in-flight requests get to finish on shutdown.
const shutdownGrace = 5 * time.Second

// run is the daemon. It returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aotusd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	dataDir := fs.String("data-dir", "", "data directory (default: $AOTUS_HOME or ~/.aotus)")
	addr := fs.String("addr", netaccess.DefaultAddress, "loopback address to listen on")
	autostart := fs.String("autostart", "", "on, off or status: manage starting at login, then exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "aotusd", version.Version)
		return 0
	}

	layout, err := resolveLayout(*dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	if *autostart != "" {
		return manageAutostart(*autostart, stdout, stderr)
	}

	// One daemon per data directory.
	lock, err := lifecycle.Acquire(layout)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	defer func() { _ = lock.Release() }()

	st, err := store.Open(ctx, layout)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	token, err := netaccess.LoadOrCreateToken(layout)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	ln, err := netaccess.Listen(*addr)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}

	// API keys live in the operating system's credential store only.
	creds := credstore.Store{}
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(layout)), map[provider.Kind]provider.Provider{
		provider.KindClaude: provider.Claude{},
		provider.KindCodex:  provider.Codex{},
		provider.KindOpenAI: provider.OpenAICompat{Keys: creds.Get},
	}, orchestrator.Options{Keys: creds})
	rec, err := mgr.Start(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	if rec.Interrupted > 0 || len(rec.Resumed) > 0 || len(rec.Failed) > 0 {
		fmt.Fprintf(stdout, "aotusd: recovered: %d turn(s) interrupted by the last stop, %d terminal session(s) resumed, %d could not be resumed\n",
			rec.Interrupted, len(rec.Resumed), len(rec.Failed))
	}
	handler := api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: token, Version: version.Version, Store: st})

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	if err := lifecycle.WriteDiscovery(layout, lifecycle.Discovery{
		PID: os.Getpid(), Address: ln.Addr().String(), StartedAt: time.Now(), Version: version.Version,
	}); err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		_ = srv.Close()
		return 1
	}
	defer func() { _ = lifecycle.RemoveDiscovery(layout) }()
	fmt.Fprintf(stdout, "aotusd %s listening on %s (data in %s)\n", version.Version, ln.Addr(), layout.Root)

	select {
	case <-ctx.Done():
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "aotusd:", err)
			return 1
		}
	}

	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	// Stop the employees' work first (terminal sessions are remembered so that
	// they come back on the next start), then the server.
	if err := mgr.Shutdown(shutCtx); err != nil {
		fmt.Fprintln(stderr, "aotusd: stopping the employees:", err)
	}
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintln(stderr, "aotusd: shutdown:", err)
	}
	fmt.Fprintln(stdout, "aotusd stopped")
	return 0
}

func resolveLayout(dir string) (datadir.Layout, error) {
	if dir != "" {
		return datadir.Layout{Root: dir}, nil
	}
	return datadir.Default()
}

func manageAutostart(action string, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "aotusd: cannot find its own path:", err)
		return 1
	}
	a, err := lifecycle.NewAutostart(exe)
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	switch action {
	case "on":
		err = a.Enable()
	case "off":
		err = a.Disable()
	case "status":
		var on bool
		if on, err = a.Enabled(); err == nil {
			fmt.Fprintln(stdout, "autostart:", map[bool]string{true: "on", false: "off"}[on])
		}
	default:
		fmt.Fprintf(stderr, "aotusd: --autostart takes on, off or status, not %q\n", action)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		return 1
	}
	return 0
}
