// Package proc starts and supervises child processes: it streams their output
// line by line, keeps a bounded buffer of recent output, and kills the whole
// process tree on cancel on every operating system (process groups on Unix,
// Job Objects on Windows).
//
// It is a leaf and one of the few packages allowed to import os/exec.
package proc
