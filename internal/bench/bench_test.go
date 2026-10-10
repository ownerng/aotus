package bench

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"aotus/internal/api"
	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// The budgets of the PRD (docs/PRD.md, requirements N1 to N7). A test that goes
// over its budget fails the build. If measurements show a budget is not
// realistic, change the requirement with the evidence, not the test quietly.
const (
	budgetStartup       = 1 * time.Second        // N1: start to ready
	budgetTurnOverhead  = 50 * time.Millisecond  // N2: what the daemon adds to a turn
	budgetStreamLatency = 100 * time.Millisecond // N3: CLI output to client
	budgetIdleRSSMB     = 100                    // N4: idle memory of the daemon
	budgetBinaryMB      = 50                     // N6: size of the daemon binary
	budgetResume        = 1 * time.Second        // N7: resume a paused employee
)

// The test binary doubles as the fake CLI that employees run.
func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}

// report logs a measurement and, when AOTUS_BENCH_REPORT names a file, appends
// it there as a Markdown table row, so a run can refresh docs/BENCHMARKS.md.
func report(t *testing.T, what, value, budget string) {
	t.Helper()
	t.Logf("%-28s %-14s (budget %s)", what, value, budget)
	if path := os.Getenv("AOTUS_BENCH_REPORT"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "| %s | %s | %s | %s/%s |\n", what, value, budget, runtime.GOOS, runtime.GOARCH)
			_ = f.Close()
		}
	}
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

func percentile(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[min(len(s)-1, int(float64(len(s))*p))]
}

// ---- the real daemon binary ----

var builtDaemon string

// daemonBinary builds aotusd once, the way a release is built.
func daemonBinary(t *testing.T) string {
	t.Helper()
	if builtDaemon != "" {
		return builtDaemon
	}
	dir, err := os.MkdirTemp("", "aotus-bench-")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "aotusd")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/aotusd")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, b)
	}
	builtDaemon = out
	return out
}

func TestBinarySizeBudget(t *testing.T) {
	fi, err := os.Stat(daemonBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	mb := float64(fi.Size()) / (1 << 20)
	report(t, "Daemon binary size (N6)", fmt.Sprintf("%.1f MB", mb), fmt.Sprintf("under %d MB", budgetBinaryMB))
	if mb >= budgetBinaryMB {
		t.Fatalf("the daemon binary is %.1f MB, over the %d MB budget", mb, budgetBinaryMB)
	}
}

// runningDaemon is the real binary, started on a fresh data directory.
type runningDaemon struct {
	cmd    *exec.Cmd
	layout datadir.Layout
	ready  time.Duration
}

func startBinary(t *testing.T) *runningDaemon { return startBinaryWith(t) }

// startBinaryWith starts the real daemon, optionally behind a wrapper command
// such as `taskset -c 0,1` (which execs the daemon, so the PID is the daemon's).
func startBinaryWith(t *testing.T, wrapper ...string) *runningDaemon {
	return startBinaryArgs(t, wrapper)
}

// startBinaryArgs is startBinaryWith with more daemon flags.
func startBinaryArgs(t *testing.T, wrapper []string, flags ...string) *runningDaemon {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	argv := append(append([]string{}, wrapper...), daemonBinary(t), "--data-dir", l.Root)
	argv = append(argv, flags...)
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Ready means a client can use it: the discovery file exists and the status
	// endpoint answers with the token.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := client.Discover(context.Background(), l); err == nil {
			if _, err := c.Status(context.Background()); err == nil {
				return &runningDaemon{cmd: cmd, layout: l, ready: time.Since(start)}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the daemon never became ready")
	return nil
}

func TestStartupBudget(t *testing.T) {
	var times []time.Duration
	for i := 0; i < 7; i++ {
		d := startBinary(t)
		times = append(times, d.ready)
		_ = d.cmd.Process.Kill()
		_, _ = d.cmd.Process.Wait()
	}
	m, worst := median(times), slices.Max(times)
	report(t, "Startup to ready (N1)", fmt.Sprintf("median %s, worst %s", m.Round(time.Millisecond), worst.Round(time.Millisecond)), "under "+budgetStartup.String())
	if m >= budgetStartup {
		t.Fatalf("median startup %s is over the %s budget", m, budgetStartup)
	}
}

func TestIdleMemoryBudget(t *testing.T) {
	d := startBinary(t)
	time.Sleep(2 * time.Second) // let it settle, as an idle daemon would be
	kb, err := rssKB(d.cmd.Process.Pid)
	if err != nil {
		t.Skipf("cannot read the memory of a process on this platform: %v", err)
	}
	mb := float64(kb) / 1024
	report(t, "Idle RSS (N4)", fmt.Sprintf("%.1f MB", mb), fmt.Sprintf("under %d MB", budgetIdleRSSMB))
	if mb >= budgetIdleRSSMB {
		t.Fatalf("the idle daemon uses %.1f MB, over the %d MB budget", mb, budgetIdleRSSMB)
	}
}

// rssKB is the resident memory of a process, in kilobytes.
func rssKB(pid int) (int, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if err != nil {
			return 0, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				return strconv.Atoi(strings.Fields(rest)[0])
			}
		}
		return 0, fmt.Errorf("no VmRSS in /proc/%d/status", pid)
	case "darwin":
		out, err := exec.CommandContext(context.Background(), "ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(out)))
	case "windows":
		out, err := exec.CommandContext(context.Background(), "tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return 0, err
		}
		fields := strings.Split(strings.TrimSpace(string(out)), `","`)
		if len(fields) < 5 {
			return 0, fmt.Errorf("unexpected tasklist output %q", out)
		}
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, strings.TrimSuffix(fields[4], `"`))
		return strconv.Atoi(digits)
	}
	return 0, fmt.Errorf("unsupported platform %s", runtime.GOOS)
}

// ---- the daemon in-process, with the fake CLI as the employees' CLI ----

type stack struct {
	c      *client.Client
	mgr    *orchestrator.Manager
	layout datadir.Layout
}

func startStack(t *testing.T) *stack {
	t.Helper()
	const token = "bench-token-0123456789abcdef0123456789abcdef0123456789abcdef012345"
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	srv := httptest.NewServer(api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: token, Version: "bench"}))
	t.Cleanup(srv.Close)
	if err := os.WriteFile(l.Token(), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.WriteDiscovery(l, lifecycle.Discovery{PID: os.Getpid(), Address: strings.TrimPrefix(srv.URL, "http://"), StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c, err := client.Discover(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	return &stack{c: c, mgr: mgr, layout: l}
}

// employee creates an employee whose CLI is the fake one running a scenario.
func (s *stack) employee(t *testing.T, name string, sc providertest.Scenario) client.Employee {
	t.Helper()
	ctx := context.Background()
	p, err := s.c.CreateProfile(ctx, client.NewProfile{
		Kind: "claude", Name: name, Binary: os.Args[0], Mode: "structured",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(sc)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.c.AcceptNotice(ctx, p.ID, "claude-headless"); err != nil {
		t.Fatal(err)
	}
	e, err := s.c.CreateEmployee(ctx, client.NewEmployee{Name: name, ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// runTurn sends a prompt and waits for the turn to end, as a client would.
func runTurn(t *testing.T, s *stack, stream *client.EventStream, id string) (sent, ended time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sent = time.Now()
	turnID, err := s.c.Send(ctx, id, "go")
	if err != nil {
		t.Fatal(err)
	}
	for {
		m, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.Update != nil && m.Update.Kind == "turn_ended" && m.Update.TurnID == turnID {
			if m.Update.State != "completed" {
				t.Fatalf("the turn ended as %s", m.Update.State)
			}
			return sent, time.Now()
		}
	}
}

func TestTurnOverheadBudget(t *testing.T) {
	s := startStack(t)
	ctx := context.Background()
	e := s.employee(t, "Atlas", providertest.Hello)
	stream, err := s.c.Events(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(ctx); err != nil { // hello
		t.Fatal(err)
	}

	// The same work done by the CLI alone, without the daemon: start the fake
	// CLI, let it print its answer, wait for it to end.
	direct := func() time.Duration {
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-p", "--output-format", "stream-json")
		cmd.Env = []string{providertest.EnvFakeCLI + "=1", providertest.EnvFakeScenario + "=" + string(providertest.Hello)}
		if runtime.GOOS == "windows" {
			cmd.Env = append(cmd.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
		}
		cmd.Stdin = strings.NewReader("go")
		start := time.Now()
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}

	runTurn(t, s, stream, e.ID) // warm up: first use creates the session
	runTurn(t, s, stream, e.ID)
	var bare, through []time.Duration
	for i := 0; i < 15; i++ {
		bare = append(bare, direct())
		sent, ended := runTurn(t, s, stream, e.ID)
		through = append(through, ended.Sub(sent))
	}
	overhead := median(through) - median(bare)
	report(t, "Per-turn overhead (N2)", fmt.Sprintf("%s (CLI alone %s, through the daemon %s)", overhead.Round(100*time.Microsecond), median(bare).Round(time.Millisecond), median(through).Round(time.Millisecond)), "under "+budgetTurnOverhead.String())
	if overhead >= budgetTurnOverhead {
		t.Fatalf("the daemon adds %s to a turn, over the %s budget", overhead, budgetTurnOverhead)
	}
}

func TestStreamLatencyBudget(t *testing.T) {
	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := s.employee(t, "Atlas", providertest.Stamped)
	stream, err := s.c.Events(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	turnID, err := s.c.Send(ctx, e.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	var delays []time.Duration
	for {
		m, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		received := time.Now()
		u := m.Update
		if u == nil || u.TurnID != turnID {
			continue
		}
		if u.Kind == "event" && u.Event.Kind == "text" {
			ns, perr := strconv.ParseInt(strings.TrimSuffix(u.Event.Text, ";"), 10, 64)
			if perr != nil {
				t.Fatalf("a stamped text event carries %q", u.Event.Text)
			}
			delays = append(delays, received.Sub(time.Unix(0, ns)))
		}
		if u.Kind == "turn_ended" {
			break
		}
	}
	if len(delays) < 90 {
		t.Fatalf("only %d of 100 events were received", len(delays))
	}
	p50, p95, worst := median(delays), percentile(delays, 0.95), slices.Max(delays)
	report(t, "Stream latency (N3)", fmt.Sprintf("median %s, p95 %s, worst %s", p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond), worst.Round(100*time.Microsecond)), "p95 under "+budgetStreamLatency.String())
	if p95 >= budgetStreamLatency {
		t.Fatalf("p95 stream latency %s is over the %s budget", p95, budgetStreamLatency)
	}
}

func TestResumeBudget(t *testing.T) {
	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := s.employee(t, "Atlas", providertest.Hello)
	stream, err := s.c.Events(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	runTurn(t, s, stream, e.ID) // the employee has worked before

	var times []time.Duration
	for i := 0; i < 7; i++ {
		if err := s.c.Pause(ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := s.c.Resume(ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		// Back at work: the next prompt gets its first event from the CLI.
		turnID, err := s.c.Send(ctx, e.ID, "go")
		if err != nil {
			t.Fatal(err)
		}
		for {
			m, err := stream.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if m.Update != nil && m.Update.TurnID == turnID && m.Update.Kind == "event" {
				times = append(times, time.Since(start))
				break
			}
		}
		for { // let the turn finish before pausing again
			m, err := stream.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if m.Update != nil && m.Update.Kind == "turn_ended" && m.Update.TurnID == turnID {
				break
			}
		}
	}
	m, worst := median(times), slices.Max(times)
	report(t, "Resume a paused employee (N7)", fmt.Sprintf("median %s, worst %s", m.Round(time.Millisecond), worst.Round(time.Millisecond)), "under "+budgetResume.String())
	if worst >= budgetResume {
		t.Fatalf("resuming took up to %s, over the %s budget", worst, budgetResume)
	}
}
