package harness

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GateOptions tunes ProjectChecks.
type GateOptions struct {
	// Full adds the slow checks: race detector, cross-compilation, govulncheck.
	Full bool
	// Strict turns "tool not installed" from a skip into a failure (CI).
	Strict bool
}

// crossTargets are the platforms the product must build for (PRD section 10).
var crossTargets = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// ProjectChecks runs the quality gates every change must pass: formatting,
// vet, build, tests, lint, harness consistency and architecture rules. With
// Full it also checks the race detector, all target platforms and known
// vulnerabilities. Every step runs even if an earlier one fails.
func ProjectChecks(root string, opt GateOptions) []Result {
	var out []Result
	add := func(name string, f func() (Outcome, string)) {
		start := time.Now()
		o, msg := f()
		out = append(out, Result{Name: name, Outcome: o, Output: msg, Elapsed: time.Since(start)})
	}
	cmd := func(argv ...string) func() (Outcome, string) {
		return func() (Outcome, string) {
			o, err := runCmd(root, nil, argv...)
			if err != nil {
				return Fail, strings.TrimSpace(o + "\n" + err.Error())
			}
			return Pass, ""
		}
	}
	optional := func(tool string, argv ...string) func() (Outcome, string) {
		return func() (Outcome, string) {
			if _, err := exec.LookPath(tool); err != nil {
				if opt.Strict {
					return Fail, tool + " is not installed (required with --strict)"
				}
				return Skip, tool + " is not installed; CI runs it"
			}
			return cmd(argv...)()
		}
	}

	add("gofmt", func() (Outcome, string) {
		o, err := runCmd(root, nil, "gofmt", "-l", ".")
		if err != nil {
			return Fail, o + "\n" + err.Error()
		}
		if o != "" {
			return Fail, "these files are not formatted (run gofmt -w .):\n" + o
		}
		return Pass, ""
	})
	add("go vet", cmd("go", "vet", "./..."))
	add("go build", cmd("go", "build", "./..."))
	add("go test", cmd("go", "test", "-count=1", "./..."))
	add("golangci-lint", optional("golangci-lint", "golangci-lint", "run"))
	add("harness lint", func() (Outcome, string) {
		m, err := Load(root)
		if err != nil {
			return Fail, err.Error()
		}
		var errs []string
		for _, i := range Lint(m) {
			if i.Level == LevelError {
				errs = append(errs, i.String())
			}
		}
		if len(errs) > 0 {
			return Fail, strings.Join(errs, "\n")
		}
		return Pass, ""
	})
	add("architecture", func() (Outcome, string) {
		v, err := Arch(root)
		if err != nil {
			return Fail, err.Error()
		}
		if len(v) > 0 {
			return Fail, strings.Join(v, "\n")
		}
		return Pass, ""
	})

	if !opt.Full {
		return out
	}

	add("go test -race", func() (Outcome, string) {
		cgo, _ := runCmd(root, nil, "go", "env", "CGO_ENABLED")
		if cgo != "1" {
			if opt.Strict {
				return Fail, "the race detector needs CGO_ENABLED=1 and a C compiler (required with --strict)"
			}
			return Skip, "needs CGO_ENABLED=1 and a C compiler; CI runs it"
		}
		return cmd("go", "test", "-race", "-count=1", "./...")()
	})
	for _, t := range crossTargets {
		t := t
		name := fmt.Sprintf("cross-build %s/%s", t.goos, t.goarch)
		add(name, func() (Outcome, string) {
			env := []string{"CGO_ENABLED=0", "GOOS=" + t.goos, "GOARCH=" + t.goarch}
			o, err := runCmd(root, env, "go", "build", "./...")
			if err != nil {
				return Fail, strings.TrimSpace(o + "\n" + err.Error())
			}
			return Pass, ""
		})
	}
	add("govulncheck", optional("govulncheck", "govulncheck", "./..."))
	return out
}
