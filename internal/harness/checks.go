package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Outcome is the result state of a check.
type Outcome string

// Check outcomes.
const (
	Pass Outcome = "pass"
	Fail Outcome = "fail"
	Skip Outcome = "skip"
)

// Result is what running one check produced.
type Result struct {
	Name    string
	Outcome Outcome
	Output  string
	Elapsed time.Duration
}

// Failed reports whether the result is a failure.
func (r Result) Failed() bool { return r.Outcome == Fail }

// FirstFailure returns the first failed result, if any.
func FirstFailure(rs []Result) (Result, bool) {
	for _, r := range rs {
		if r.Failed() {
			return r, true
		}
	}
	return Result{}, false
}

const (
	checkTimeout = 10 * time.Minute
	maxOutput    = 6000
)

// RunCheck executes a task or gate check from the repository root.
func RunCheck(root string, c Check) Result {
	start := time.Now()
	res := Result{Name: c.Name, Outcome: Pass}
	switch {
	case len(c.Cmd) > 0:
		out, err := runCmd(root, nil, c.Cmd...)
		res.Output = out
		if err != nil {
			res.Outcome = Fail
			res.Output = strings.TrimSpace(out + "\n" + err.Error())
		}
	case c.File != "":
		if msg := fileCheck(root, c); msg != "" {
			res.Outcome = Fail
			res.Output = msg
		}
	case c.Pkg != "":
		if msg := goTestsCheck(root, c); msg != "" {
			res.Outcome = Fail
			res.Output = msg
		}
	default:
		res.Outcome = Fail
		res.Output = "check has no cmd, file or pkg"
	}
	res.Elapsed = time.Since(start)
	return res
}

func fileCheck(root string, c Check) string {
	path := filepath.Join(root, filepath.FromSlash(c.File))
	b, err := os.ReadFile(path) //nolint:gosec // path comes from reviewed harness files
	if err != nil {
		return fmt.Sprintf("%s does not exist", c.File)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return fmt.Sprintf("%s is empty", c.File)
	}
	var missing []string
	for _, want := range c.Contains {
		if !bytes.Contains(b, []byte(want)) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("%s is missing required content: %s", c.File, strings.Join(missing, ", "))
	}
	return ""
}

// goTestsCheck runs the named tests of a package and verifies that each one
// exists and passed.
func goTestsCheck(root string, c Check) string {
	if len(c.Tests) == 0 {
		return "check has pkg but no tests"
	}
	pattern := "^(" + strings.Join(c.Tests, "|") + ")$"
	args := []string{"go", "test", "-count=1", "-v", "-run", pattern}
	if len(c.Tags) > 0 {
		args = append(args, "-tags", strings.Join(c.Tags, ","))
	}
	out, err := runCmd(root, nil, append(args, c.Pkg)...)
	var missing []string
	for _, name := range c.Tests {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			missing = append(missing, name)
		}
	}
	switch {
	case len(missing) > 0:
		return fmt.Sprintf("these tests did not run and pass in %s: %s\n%s", c.Pkg, strings.Join(missing, ", "), out)
	case err != nil:
		return out + "\n" + err.Error()
	}
	return ""
}

// runCmd runs a command in dir and returns its combined output, truncated to
// the tail so failures stay readable.
func runCmd(dir string, env []string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // commands come from reviewed harness files
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > maxOutput {
		out = "...(truncated)...\n" + out[len(out)-maxOutput:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", checkTimeout)
	}
	return strings.TrimSpace(out), err
}

// RunChecks runs checks in order and always runs all of them.
func RunChecks(root string, checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, RunCheck(root, c))
	}
	return out
}
