package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
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

// tailscale is the local Tailscale as the daemon uses it: who is calling, and
// how this machine is known on the tailnet. netaccess.LocalAPI is the real one.
type tailscale interface {
	netaccess.Identifier
	Status(ctx context.Context) (netaccess.TailnetStatus, error)
}

// options are the seams the tests replace; production uses the defaults.
type options struct {
	runCmd        func(ctx context.Context, name string, args ...string) (string, error)
	goos          string
	currentUser   func() (string, error)
	newTailscale  func(socket string) tailscale
	listenTailnet func(ip netip.Addr, port string, id netaccess.Identifier, log *slog.Logger) (net.Listener, error)
}

func defaultOptions() options {
	return options{
		runCmd:        runCommand,
		goos:          runtime.GOOS,
		currentUser:   currentUserName,
		newTailscale:  func(socket string) tailscale { return netaccess.NewLocalAPI(socket) },
		listenTailnet: netaccess.ListenTailnet,
	}
}

// run is the daemon. It returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWith(ctx, args, stdout, stderr, defaultOptions())
}

func runWith(ctx context.Context, args []string, stdout, stderr io.Writer, opts options) int {
	fs := flag.NewFlagSet("aotusd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	dataDir := fs.String("data-dir", "", "data directory (default: $AOTUS_HOME or ~/.aotus)")
	addr := fs.String("addr", netaccess.DefaultAddress, "loopback address to listen on")
	autostart := fs.String("autostart", "", "on, off or status: manage starting at login, then exit")
	tailnet := fs.Bool("tailnet", false, "also serve the API on this machine's Tailscale address, to the people on the allow-list")
	tsSocket := fs.String("tailscale-socket", netaccess.DefaultSocket(), "the local Tailscale's LocalAPI socket")
	tailnetPort := fs.String("tailnet-port", "7843", "port of the tailnet listener")
	service := fs.String("service", "", "install, uninstall or status: manage the systemd service that runs this daemon, then exit")
	serviceScope := fs.String("service-scope", "user", "user (runs as you, no root) or system (root installs it, runs as --service-user)")
	serviceUser := fs.String("service-user", "", "the user a system service runs as")
	serviceDir := fs.String("service-dir", "", "directory for the unit file (default: the systemd directory of the scope)")
	owner := fs.String("owner", "", "tailnet login of the owner of this daemon (default: the person the Tailscale node belongs to)")
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
	if *service != "" {
		return manageService(ctx, opts, serviceRequest{
			Action: *service, Scope: lifecycle.UnitScope(*serviceScope), User: *serviceUser, Dir: *serviceDir,
			DataDir: layout.Root, Tailnet: *tailnet, Port: *tailnetPort, Owner: *owner, TailscaleSocket: tsSocketIfSet(fs, *tsSocket),
		}, stdout, stderr)
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

	// The tailnet listener is optional and never takes the daemon down: if it
	// cannot start, say why and keep serving loopback.
	var tailLn net.Listener
	var tailHosts []string
	if *tailnet {
		var terr error
		tailLn, tailHosts, terr = setupTailnet(ctx, opts, st, *tsSocket, *tailnetPort, *owner, stdout)
		if terr != nil {
			fmt.Fprintf(stderr, "aotusd: the tailnet listener is not started: %v\naotusd: loopback keeps working\n", terr)
		}
	}

	handler := api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: token, Version: version.Version, Store: st, TailnetHosts: tailHosts})

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	var tailSrv *http.Server
	if tailLn != nil {
		// ConnContext is what carries the identified caller into the handlers;
		// the loopback server deliberately does not have it.
		tailSrv = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ConnContext: netaccess.ConnContext}
		go func() {
			if err := tailSrv.Serve(tailLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(stderr, "aotusd: the tailnet listener stopped:", err)
			}
		}()
	}

	disc := lifecycle.Discovery{PID: os.Getpid(), Address: ln.Addr().String(), StartedAt: time.Now(), Version: version.Version}
	if tailLn != nil {
		disc.Tailnet = tailnetName(tailHosts, tailLn)
	}
	if err := lifecycle.WriteDiscovery(layout, disc); err != nil {
		fmt.Fprintln(stderr, "aotusd:", err)
		_ = srv.Close()
		if tailSrv != nil {
			_ = tailSrv.Close()
		}
		return 1
	}
	defer func() { _ = lifecycle.RemoveDiscovery(layout) }()
	fmt.Fprintf(stdout, "aotusd %s listening on %s (data in %s)\n", version.Version, ln.Addr(), layout.Root)
	if tailLn != nil {
		fmt.Fprintf(stdout, "aotusd: also on the tailnet at %s\n", disc.Tailnet)
	}

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
	if tailSrv != nil {
		if err := tailSrv.Shutdown(shutCtx); err != nil {
			fmt.Fprintln(stderr, "aotusd: tailnet shutdown:", err)
		}
	}
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintln(stderr, "aotusd: shutdown:", err)
	}
	fmt.Fprintln(stdout, "aotusd stopped")
	return 0
}

// setupTailnet asks the local Tailscale how this machine is known, makes sure an
// owner is recorded, and opens the listener on the machine's tailnet address
// and on nothing else. It returns the names clients may use in the Host header.
func setupTailnet(ctx context.Context, opts options, st *store.Store, socket, port, ownerFlag string, stdout io.Writer) (net.Listener, []string, error) {
	ts := opts.newTailscale(socket)
	status, err := ts.Status(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot ask Tailscale about this machine (is it installed and running? socket %s): %w", socket, err)
	}
	if !status.Running {
		return nil, nil, fmt.Errorf("the local Tailscale is %q, not Running: run `tailscale up`", status.State)
	}
	if len(status.Addrs) == 0 {
		return nil, nil, errors.New("the local Tailscale gave this machine no tailnet address")
	}
	// Only a tailnet address is ever bound: ListenTailnet refuses the rest, and
	// we check first so that nothing is opened on a doubtful answer.
	ip := status.Addrs[0]
	if err := netaccess.CheckTailnetAddr(ip); err != nil {
		return nil, nil, fmt.Errorf("the address Tailscale reported is not on the tailnet: %w", err)
	}

	owner := strings.TrimSpace(ownerFlag)
	if owner == "" {
		current, err := st.OwnerLogin(ctx)
		if err != nil {
			return nil, nil, err
		}
		if current != "" {
			owner = current
		} else {
			owner = status.OwnerLogin
		}
	}
	if owner == "" {
		return nil, nil, errors.New("this Tailscale node belongs to a tag, not a person, so the owner cannot be worked out: start with --owner you@example.com")
	}
	if err := st.SetOwnerLogin(ctx, owner); err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(stdout, "aotusd: the owner of this daemon is %s\n", strings.ToLower(owner))

	ln, err := opts.listenTailnet(ip, port, ts, slog.Default())
	if err != nil {
		return nil, nil, err
	}
	return ln, status.Names, nil
}

// tailnetName is the name:port clients should dial: the first name with a dot
// (the MagicDNS name), else the address.
func tailnetName(hosts []string, ln net.Listener) string {
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	for _, h := range hosts {
		if strings.Contains(h, ".") && net.ParseIP(h) == nil {
			return net.JoinHostPort(h, port)
		}
	}
	return ln.Addr().String()
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
