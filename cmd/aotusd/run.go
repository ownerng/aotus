package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
	"aotus/internal/netaccess"
	"aotus/internal/store"
	"aotus/internal/version"
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

	srv := &http.Server{Handler: handler(token), ReadHeaderTimeout: 10 * time.Second}
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

// handler is a placeholder until internal/api (task P1-014): it only answers
// the health check, and only to a caller with the token.
func handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !netaccess.TokenValid(got, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version.Version})
	})
	return mux
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
