package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Errors returned by Start and Interrupt. Use errors.Is.
var (
	// ErrBinaryNotFound means the binary does not exist or is not on the path.
	ErrBinaryNotFound = errors.New("binary not found")
	// ErrNotExecutable means the file exists but cannot be executed.
	ErrNotExecutable = errors.New("binary is not executable")
	// ErrInterruptUnsupported means the platform cannot deliver an interrupt
	// to the process group (Windows); use the program's own stdin protocol.
	ErrInterruptUnsupported = errors.New("interrupt is not supported on this platform")
)

// Stream tells which output a line came from.
type Stream uint8

// Output streams.
const (
	Stdout Stream = iota + 1
	Stderr
)

func (s Stream) String() string {
	switch s {
	case Stdout:
		return "stdout"
	case Stderr:
		return "stderr"
	}
	return "unknown"
}

// Line is one line of child output without its line terminator. Order is
// preserved within a stream; stdout and stderr are separate pipes and may be
// interleaved in any order relative to each other.
type Line struct {
	Stream Stream
	Text   string
	// Truncated is true when the line was longer than Spec.MaxLineBytes and
	// the rest was dropped.
	Truncated bool
}

// Spec describes the process to run.
type Spec struct {
	// Path is the binary. Relative names are resolved with the PATH of the
	// daemon, so callers that care should pass an absolute path.
	Path string
	Args []string
	// Dir is the working directory ("" means the daemon's).
	Dir string
	// Env is the complete environment of the child. Nothing is inherited:
	// an empty Env means an empty environment (Windows adds SYSTEMROOT,
	// which it cannot run without).
	Env []string

	// RingLines is how many recent lines Recent keeps (default 1000).
	RingLines int
	// MaxLineBytes truncates longer lines (default 1 MiB).
	MaxLineBytes int
	// LineBuffer is the capacity of the Lines channel (default 256).
	LineBuffer int
	// Grace is how long a cancelled process gets to exit after the polite
	// termination request before the whole tree is killed (default 2 s).
	Grace time.Duration
}

func (s Spec) withDefaults() Spec {
	if s.RingLines <= 0 {
		s.RingLines = 1000
	}
	if s.MaxLineBytes <= 0 {
		s.MaxLineBytes = 1 << 20
	}
	if s.LineBuffer <= 0 {
		s.LineBuffer = 256
	}
	if s.Grace <= 0 {
		s.Grace = 2 * time.Second
	}
	return s
}

// Exit describes how a process ended.
type Exit struct {
	// Code is the exit code, or -1 when the process was killed by a signal.
	Code int
	// Signal is the name of the terminating signal on Unix, "" otherwise.
	Signal string
	// Canceled is true when Cancel (or the context) ended the process.
	Canceled bool
}

// Process is a running child process and everything it spawned.
type Process struct {
	spec Spec
	cmd  *exec.Cmd

	stdin io.WriteCloser
	lines chan Line
	ring  *ring

	drop   chan struct{} // closed on Cancel: readers stop blocking on a full Lines channel
	exited chan struct{} // closed when the leader process has exited
	done   chan struct{} // closed when it exited and all output was delivered

	cancelOnce sync.Once
	canceled   atomic.Bool
	readers    sync.WaitGroup
	exit       Exit
	platform   // per-OS state (the Job Object on Windows)
}

// Start launches the process. The process lives until it exits, Cancel is
// called, or ctx is done; in the last two cases the whole process tree is
// terminated. The caller must drain Lines (or call Cancel), otherwise a full
// channel applies back-pressure to the child.
func Start(ctx context.Context, spec Spec) (*Process, error) {
	if spec.Path == "" {
		return nil, errors.New("proc: empty binary path")
	}
	spec = spec.withDefaults()

	cmd := exec.Command(spec.Path, spec.Args...) //nolint:gosec,noctx // running a caller-chosen binary is this package's purpose; Process manages cancellation itself to kill the whole tree
	if cmd.Err != nil {
		return nil, startError(spec.Path, cmd.Err)
	}
	cmd.Dir = spec.Dir
	cmd.Env = append([]string{}, spec.Env...) // non-nil: never inherit
	prepare(cmd)

	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("proc: creating stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(outR, outW)
		return nil, fmt.Errorf("proc: creating stderr pipe: %w", err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		closeAll(outR, outW, errR, errW)
		return nil, fmt.Errorf("proc: creating stdin pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = outW, errW, inR

	if err := cmd.Start(); err != nil {
		closeAll(outR, outW, errR, errW, inR, inW)
		return nil, startError(spec.Path, err)
	}
	// The child owns its ends now.
	closeAll(outW, errW, inR)

	p := &Process{
		spec:   spec,
		cmd:    cmd,
		stdin:  inW,
		lines:  make(chan Line, spec.LineBuffer),
		ring:   newRing(spec.RingLines),
		drop:   make(chan struct{}),
		exited: make(chan struct{}),
		done:   make(chan struct{}),
	}
	if err := p.attach(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		closeAll(outR, errR, inW)
		return nil, fmt.Errorf("proc: supervising %q: %w", spec.Path, err)
	}

	p.readers.Add(2)
	go p.readLines(outR, Stdout)
	go p.readLines(errR, Stderr)
	go p.supervise(ctx)
	return p, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func startError(path string, err error) error {
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("proc: %q: %w: %w", path, ErrBinaryNotFound, err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("proc: %q: %w: %w", path, ErrNotExecutable, err)
	}
	return fmt.Errorf("proc: starting %q: %w", path, err)
}

// Pid is the operating system process ID of the leader.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Lines delivers output as it is produced and is closed once the process has
// exited and all its output was delivered.
func (p *Process) Lines() <-chan Line { return p.lines }

// Done is closed when the process has exited and Lines is closed.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stdin is the write end of the child's standard input.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// Recent returns the last Spec.RingLines lines, oldest first.
func (p *Process) Recent() []Line { return p.ring.snapshot() }

// Wait blocks until the process has exited and its output was delivered.
func (p *Process) Wait() Exit {
	<-p.done
	return p.exit
}

// Cancel terminates the whole process tree: a polite request first, then a
// kill after Spec.Grace. It is safe to call more than once and from several
// goroutines.
func (p *Process) Cancel() {
	p.cancelOnce.Do(func() {
		p.canceled.Store(true)
		close(p.drop)
		p.terminate(false)
		go func() {
			t := time.NewTimer(p.spec.Grace)
			defer t.Stop()
			select {
			case <-p.exited:
			case <-t.C:
			}
			p.terminate(true)
		}()
	})
}

// Interrupt asks the process group to stop what it is doing (SIGINT on Unix),
// the way a user pressing Ctrl+C would. It does not end the process tree.
func (p *Process) Interrupt() error { return p.interrupt() }

func (p *Process) supervise(ctx context.Context) {
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()

	select {
	case <-waited:
	case <-ctx.Done():
		p.Cancel()
		<-waited
	}
	close(p.exited)
	_ = p.stdin.Close()

	// The leader is gone: nothing it started may outlive it.
	p.terminate(true)
	p.readers.Wait()

	p.exit = p.exitInfo()
	p.closePlatform()
	close(p.lines)
	close(p.done)
}

func (p *Process) exitInfo() Exit {
	e := Exit{Code: -1, Canceled: p.canceled.Load()}
	if ps := p.cmd.ProcessState; ps != nil {
		e.Code = ps.ExitCode()
		e.Signal = signalName(ps)
	}
	return e
}

func (p *Process) readLines(r *os.File, s Stream) {
	defer p.readers.Done()
	defer func() { _ = r.Close() }()

	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	truncated := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			return // EOF: every writer of the pipe is gone
		}
		if room := p.spec.MaxLineBytes - len(buf); room > 0 {
			n := min(len(chunk), room)
			buf = append(buf, chunk[:n]...)
			truncated = truncated || n < len(chunk)
		} else if len(chunk) > 0 {
			truncated = true
		}
		if isPrefix {
			continue
		}
		l := Line{Stream: s, Text: string(buf), Truncated: truncated}
		buf, truncated = buf[:0], false
		p.ring.add(l)
		select {
		case p.lines <- l:
		case <-p.drop:
		}
	}
}

// ring keeps the most recent lines in a fixed amount of memory.
type ring struct {
	mu    sync.Mutex
	buf   []Line
	start int
	n     int
}

func newRing(capacity int) *ring { return &ring{buf: make([]Line, capacity)} }

func (r *ring) add(l Line) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n < len(r.buf) {
		r.buf[(r.start+r.n)%len(r.buf)] = l
		r.n++
		return
	}
	r.buf[r.start] = l
	r.start = (r.start + 1) % len(r.buf)
}

func (r *ring) snapshot() []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Line, r.n)
	for i := range out {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}
