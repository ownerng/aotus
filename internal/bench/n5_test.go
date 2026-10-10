package bench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/client"
	"aotus/internal/provider/providertest"
)

// N5: ten employees working at once on a 2 vCPU server must not degrade the
// interface. The bounds below are what "not degrade" means in numbers; the
// daemon runs as its own process, limited to two CPUs where the system allows
// it (taskset on Linux), together with everything it starts.
const (
	n5Employees       = 10
	n5APIp95          = 100 * time.Millisecond // GET /status and /employees while all work
	n5APIMax          = 500 * time.Millisecond
	n5TurnStart       = 500 * time.Millisecond // Send to the turn being started
	n5StreamP95       = budgetStreamLatency    // N3 must still hold under load
	n5DaemonMemoryMB  = budgetIdleRSSMB        // the daemon stays under N4's bound under load
	n5ProbeRounds     = 4
	n5BusyEmployees   = 4
	n5TermEmployees   = 5
	n5SettleBeforeRun = time.Second
)

// n5Employee creates an employee on its own profile running a scripted CLI.
func n5Employee(t *testing.T, c *client.Client, name string, sc providertest.Scenario, mode string) client.Employee {
	t.Helper()
	ctx := context.Background()
	p, err := c.CreateProfile(ctx, client.NewProfile{
		Kind: "claude", Name: name, Binary: os.Args[0], Mode: mode,
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(sc)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode == "structured" {
		if err := c.AcceptNotice(ctx, p.ID, "claude-headless"); err != nil {
			t.Fatal(err)
		}
	}
	e, err := c.CreateEmployee(ctx, client.NewEmployee{Name: name, ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// cpuSeconds is the CPU time a process has used (Linux only).
func cpuSeconds(pid int) (float64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndex(s, ")")+2:])
	if len(f) < 13 {
		return 0, false
	}
	u, _ := strconv.ParseFloat(f[11], 64)
	k, _ := strconv.ParseFloat(f[12], 64)
	return (u + k) / 100, true // clock ticks at 100 Hz
}

// descendantsRSSMB adds up the memory of everything the daemon started.
func descendantsRSSMB(pid int) float64 {
	out, err := exec.CommandContext(context.Background(), "ps", "-o", "pid=,ppid=,rss=", "-A").Output()
	if err != nil {
		return 0
	}
	kids := map[int][]int{}
	rss := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		p, _ := strconv.Atoi(f[0])
		pp, _ := strconv.Atoi(f[1])
		r, _ := strconv.Atoi(f[2])
		kids[pp] = append(kids[pp], p)
		rss[p] = r
	}
	total := 0
	var walk func(int)
	walk = func(p int) {
		for _, k := range kids[p] {
			total += rss[k]
			walk(k)
		}
	}
	walk(pid)
	return float64(total) / 1024
}

func TestTenParallelEmployeesStayResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("a load test: skipped with -short")
	}
	var wrapper []string
	limited := "all CPUs of this machine (no way to limit them here)"
	if path, err := exec.LookPath("taskset"); err == nil && runtime.GOOS == "linux" && runtime.NumCPU() > 2 {
		wrapper = []string{path, "-c", "0,1"}
		limited = "2 CPUs (taskset -c 0,1, inherited by every employee)"
	}
	// The default allows four turns at once and queues the rest; N5 is about ten
	// working together, so the daemon is told to run ten.
	d := startBinaryArgs(t, wrapper, "--max-turns", strconv.Itoa(n5Employees))
	pid := d.cmd.Process.Pid
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := client.Discover(ctx, d.layout)
	if err != nil {
		t.Fatal(err)
	}

	// Ten employees: four working in turns, one that is measured, five hosting a
	// busy interactive program that a window is watching.
	var busy []client.Employee
	for i := 0; i < n5BusyEmployees; i++ {
		busy = append(busy, n5Employee(t, c, fmt.Sprintf("Worker%d", i), providertest.Busy, "structured"))
	}
	probe := n5Employee(t, c, "Probe", providertest.Stamped, "structured")
	var terms []client.Employee
	for i := 0; i < n5TermEmployees; i++ {
		terms = append(terms, n5Employee(t, c, fmt.Sprintf("Term%d", i), providertest.TermBusy, "terminal"))
	}
	if got := len(busy) + 1 + len(terms); got != n5Employees {
		t.Fatalf("the load has %d employees, want %d", got, n5Employees)
	}

	load, stopLoad := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var starts []time.Duration
	var termBytes int64
	defer func() { stopLoad(); wg.Wait() }()

	for _, e := range terms {
		if err := c.StartTerminal(ctx, e.ID, 24, 80); err != nil {
			t.Fatal(err)
		}
		term, err := c.Attach(ctx, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { // a window watching the terminal
			defer wg.Done()
			defer func() { _ = term.Close() }()
			for {
				b, err := term.Read(load)
				mu.Lock()
				termBytes += int64(len(b))
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
	}

	// turns runs prompts on an employee, one after another, until the load ends,
	// recording how long each one took to start.
	turns := func(e client.Employee, record bool, rounds int) {
		stream, err := c.Events(load, e.ID)
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		if _, err := stream.Next(load); err != nil { // hello
			return
		}
		for i := 0; load.Err() == nil && (rounds == 0 || i < rounds); i++ {
			sent := time.Now()
			id, err := c.Send(load, e.ID, "go")
			if err != nil {
				return
			}
			started := false
			for {
				m, err := stream.Next(load)
				if err != nil {
					return
				}
				u := m.Update
				if u == nil || u.TurnID != id {
					continue
				}
				if !started && (u.Kind == "turn_started" || u.Kind == "event") {
					started = true
					if record {
						mu.Lock()
						starts = append(starts, time.Since(sent))
						mu.Unlock()
					}
				}
				if u.Kind == "turn_ended" {
					break
				}
			}
		}
	}
	for _, e := range busy {
		wg.Add(1)
		go func() { defer wg.Done(); turns(e, true, 0) }()
	}
	time.Sleep(n5SettleBeforeRun)

	cpu0, haveCPU := cpuSeconds(pid)
	began := time.Now()

	// The interface probe: what a window does all the time.
	var api []time.Duration
	probeDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-probeDone:
				return
			case <-time.After(50 * time.Millisecond):
			}
			t0 := time.Now()
			_, e1 := c.Status(ctx)
			_, e2 := c.Employees(ctx)
			if e1 == nil && e2 == nil {
				mu.Lock()
				api = append(api, time.Since(t0)/2)
				mu.Unlock()
			}
		}
	}()

	// The streaming probe: a stamped answer, several times, while all work.
	var delays []time.Duration
	stream, err := c.Events(ctx, probe.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < n5ProbeRounds; r++ {
		sent := time.Now()
		id, err := c.Send(ctx, probe.ID, "go")
		if err != nil {
			t.Fatal(err)
		}
		first := true
		for {
			m, err := stream.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			received := time.Now()
			u := m.Update
			if u == nil || u.TurnID != id {
				continue
			}
			if first && (u.Kind == "turn_started" || u.Kind == "event") {
				first = false
				mu.Lock()
				starts = append(starts, received.Sub(sent))
				mu.Unlock()
			}
			if u.Kind == "event" && u.Event.Kind == "text" {
				if ns, perr := strconv.ParseInt(strings.TrimSuffix(u.Event.Text, ";"), 10, 64); perr == nil {
					delays = append(delays, received.Sub(time.Unix(0, ns)))
				}
			}
			if u.Kind == "turn_ended" {
				break
			}
		}
	}
	close(probeDone)
	window := time.Since(began)
	cpu1, _ := cpuSeconds(pid)
	rss, rerr := rssKB(pid)
	kids := descendantsRSSMB(pid)

	mu.Lock()
	defer mu.Unlock()
	if len(api) < 20 || len(delays) < 300 || len(starts) < 8 {
		t.Fatalf("too few samples to say anything: %d api, %d stream, %d starts", len(api), len(delays), len(starts))
	}
	apiP95, apiMax := percentile(api, 0.95), slices.Max(api)
	streamP95 := percentile(delays, 0.95)
	startP95, startMax := percentile(starts, 0.95), slices.Max(starts)
	if termBytes == 0 {
		t.Error("the watched terminals showed nothing: the load was not what it claims")
	}

	cpuText := "n/a here"
	if haveCPU {
		cpuText = fmt.Sprintf("%.0f%% of one CPU", (cpu1-cpu0)/window.Seconds()*100)
	}
	memText := "n/a here"
	if rerr == nil {
		memText = fmt.Sprintf("%.1f MB", float64(rss)/1024)
	}
	report(t, "N5 ten employees: interface", fmt.Sprintf("GET status+employees p95 %s, max %s", apiP95.Round(100*time.Microsecond), apiMax.Round(100*time.Microsecond)), fmt.Sprintf("p95 under %s, max under %s", n5APIp95, n5APIMax))
	report(t, "N5 ten employees: turn start", fmt.Sprintf("p95 %s, max %s over %d turns", startP95.Round(100*time.Microsecond), startMax.Round(100*time.Microsecond), len(starts)), "max under "+n5TurnStart.String())
	report(t, "N5 ten employees: stream latency", fmt.Sprintf("p95 %s over %d events", streamP95.Round(100*time.Microsecond), len(delays)), "p95 under "+n5StreamP95.String())
	report(t, "N5 ten employees: daemon memory", fmt.Sprintf("%s (its employees: %.0f MB more); daemon CPU %s", memText, kids, cpuText), fmt.Sprintf("daemon under %d MB; limited to %s", n5DaemonMemoryMB, limited))

	if apiP95 >= n5APIp95 || apiMax >= n5APIMax {
		t.Errorf("the interface degraded: p95 %s (bound %s), max %s (bound %s)", apiP95, n5APIp95, apiMax, n5APIMax)
	}
	if startMax >= n5TurnStart {
		t.Errorf("a turn took %s to start (bound %s)", startMax, n5TurnStart)
	}
	if streamP95 >= n5StreamP95 {
		t.Errorf("stream latency p95 %s under load is over the %s budget (N3)", streamP95, n5StreamP95)
	}
	if rerr == nil && float64(rss)/1024 >= n5DaemonMemoryMB {
		t.Errorf("the daemon uses %.1f MB under load, over %d MB", float64(rss)/1024, n5DaemonMemoryMB)
	}
}
