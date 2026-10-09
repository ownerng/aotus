package proc

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPTYUnsupported means pseudo-terminals are not available on this platform
// in this build.
var ErrPTYUnsupported = errors.New("proc: pseudo-terminals are not supported on this platform")

// ErrPTYClosed is returned when writing to a terminal whose program has ended.
var ErrPTYClosed = errors.New("proc: the terminal is closed")

// PTYSpec describes a program to run inside a pseudo-terminal.
type PTYSpec struct {
	Spec
	// Rows and Cols are the initial size (default 24 by 80).
	Rows, Cols uint16
	// ReplayBytes is how much recent output is kept for viewers that join
	// later (default 256 KiB).
	ReplayBytes int
}

// ptyBackend is the platform-specific part: the master side of the terminal and
// the control of the program on the other side.
type ptyBackend interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
	// Terminate ends the whole process tree: politely, or by force.
	Terminate(force bool)
	// Wait blocks until the leader exits and returns its exit information.
	Wait() Exit
	Pid() int
}

// PTYProcess is a program running in a pseudo-terminal that outlives any
// viewer: it keeps running, and its recent output is kept, while nobody is
// looking. Viewers subscribe to get the recent output first and then the live
// output; a viewer that cannot keep up is disconnected rather than allowed to
// slow the program down.
type PTYProcess struct {
	spec PTYSpec
	b    ptyBackend

	mu     sync.Mutex
	replay *byteRing
	subs   map[int]chan []byte
	nextID int
	closed bool // the program ended; no new output will come

	cancelOnce sync.Once
	canceled   atomic.Bool
	exited     chan struct{}
	done       chan struct{}
	exit       Exit
}

// subscriberBuffer is how many output chunks a viewer may fall behind by.
const subscriberBuffer = 256

// StartPTY runs the program in a pseudo-terminal. It lives until it exits,
// Cancel is called, or ctx is done; in the last two cases the whole process
// tree is terminated.
func StartPTY(ctx context.Context, spec PTYSpec) (*PTYProcess, error) {
	if spec.Path == "" {
		return nil, errors.New("proc: empty binary path")
	}
	if spec.Rows == 0 {
		spec.Rows = 24
	}
	if spec.Cols == 0 {
		spec.Cols = 80
	}
	if spec.ReplayBytes <= 0 {
		spec.ReplayBytes = 256 * 1024
	}
	spec.Spec = spec.withDefaults()

	b, err := openPTY(spec)
	if err != nil {
		return nil, err
	}
	p := &PTYProcess{
		spec:   spec,
		b:      b,
		replay: newByteRing(spec.ReplayBytes),
		subs:   map[int]chan []byte{},
		exited: make(chan struct{}),
		done:   make(chan struct{}),
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		p.pump()
	}()
	go p.supervise(ctx, readerDone)
	return p, nil
}

// pump copies the terminal's output to the replay buffer and the viewers.
func (p *PTYProcess) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.b.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			p.publish(chunk)
		}
		if err != nil {
			return // EOF, or EIO once the program and its children are gone
		}
	}
}

func (p *PTYProcess) publish(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replay.write(chunk)
	for id, ch := range p.subs {
		select {
		case ch <- chunk:
		default:
			// This viewer fell too far behind. Disconnect it: it can subscribe
			// again and will get the replay. The program is never slowed down.
			close(ch)
			delete(p.subs, id)
		}
	}
}

func (p *PTYProcess) supervise(ctx context.Context, readerDone <-chan struct{}) {
	waited := make(chan Exit, 1)
	go func() { waited <- p.b.Wait() }()

	var exit Exit
	select {
	case exit = <-waited:
	case <-ctx.Done():
		p.Cancel()
		exit = <-waited
	}
	close(p.exited)

	// The leader is gone: nothing it started may outlive it.
	p.b.Terminate(true)
	// Closing the master unblocks the reader if a straggler still held the
	// slave side open; give it a moment to deliver what is already buffered.
	select {
	case <-readerDone:
	case <-time.After(500 * time.Millisecond):
	}
	_ = p.b.Close()
	<-readerDone

	exit.Canceled = p.canceled.Load()
	p.exit = exit

	p.mu.Lock()
	p.closed = true
	for id, ch := range p.subs {
		close(ch)
		delete(p.subs, id)
	}
	p.mu.Unlock()
	close(p.done)
}

// Pid is the process ID of the program.
func (p *PTYProcess) Pid() int { return p.b.Pid() }

// Write sends input to the program, as if typed.
func (p *PTYProcess) Write(b []byte) (int, error) {
	select {
	case <-p.exited:
		return 0, ErrPTYClosed
	default:
	}
	return p.b.Write(b)
}

// Resize changes the terminal size; the program is told through SIGWINCH.
func (p *PTYProcess) Resize(rows, cols uint16) error {
	select {
	case <-p.exited:
		return ErrPTYClosed
	default:
	}
	return p.b.Resize(rows, cols)
}

// Interrupt types Ctrl+C into the terminal, which the terminal driver turns
// into SIGINT for the program in the foreground, exactly like a user would.
func (p *PTYProcess) Interrupt() error {
	_, err := p.Write([]byte{0x03})
	return err
}

// Subscribe returns the recent output and a channel with everything that
// follows, with nothing missing and nothing repeated between the two. The
// channel is closed when the program ends or when the viewer falls too far
// behind; cancel unsubscribes.
func (p *PTYProcess) Subscribe() (replay []byte, live <-chan []byte, cancel func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	replay = p.replay.snapshot()
	ch := make(chan []byte, subscriberBuffer)
	if p.closed {
		close(ch)
		return replay, ch, func() {}
	}
	id := p.nextID
	p.nextID++
	p.subs[id] = ch
	return replay, ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if c, ok := p.subs[id]; ok {
			close(c)
			delete(p.subs, id)
		}
	}
}

// Replay is the recent output, oldest first.
func (p *PTYProcess) Replay() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.replay.snapshot()
}

// Cancel terminates the whole process tree: a polite request first, then a
// kill after Spec.Grace. Safe to call more than once.
func (p *PTYProcess) Cancel() {
	p.cancelOnce.Do(func() {
		p.canceled.Store(true)
		p.b.Terminate(false)
		go func() {
			t := time.NewTimer(p.spec.Grace)
			defer t.Stop()
			select {
			case <-p.exited:
			case <-t.C:
			}
			p.b.Terminate(true)
		}()
	})
}

// Done is closed when the program has ended and the last output was delivered.
func (p *PTYProcess) Done() <-chan struct{} { return p.done }

// Wait blocks until the program has ended.
func (p *PTYProcess) Wait() Exit {
	<-p.done
	return p.exit
}

// byteRing keeps the most recent bytes written to it.
type byteRing struct {
	buf   []byte
	start int
	n     int
}

func newByteRing(capacity int) *byteRing { return &byteRing{buf: make([]byte, capacity)} }

func (r *byteRing) write(p []byte) {
	if len(p) >= len(r.buf) {
		copy(r.buf, p[len(p)-len(r.buf):])
		r.start, r.n = 0, len(r.buf)
		return
	}
	for _, c := range p {
		if r.n < len(r.buf) {
			r.buf[(r.start+r.n)%len(r.buf)] = c
			r.n++
		} else {
			r.buf[r.start] = c
			r.start = (r.start + 1) % len(r.buf)
		}
	}
}

func (r *byteRing) snapshot() []byte {
	out := make([]byte, r.n)
	for i := range out {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}
