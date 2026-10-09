package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ArchRules is harness/architecture.json: the enforceable half of
// docs/ARCHITECTURE.md.
type ArchRules struct {
	// Layers lists, for every package (and its sub-packages), which other
	// packages of this module it may import. "*" allows everything.
	Layers []Layer `json:"layers"`
	// Restricted limits sensitive imports to a few packages.
	Restricted []Restriction `json:"restricted_imports"`
}

// Layer is one dependency rule.
type Layer struct {
	Package   string   `json:"package"`
	Why       string   `json:"why,omitempty"`
	MayImport []string `json:"may_import"`
}

// Restriction limits an import (for example os/exec) to some packages.
type Restriction struct {
	Import    string   `json:"import"`
	Why       string   `json:"why,omitempty"`
	AllowedIn []string `json:"allowed_in"`
}

// Pkg is the subset of `go list -json` the architecture check needs.
type Pkg struct {
	ImportPath string
	Imports    []string
	CgoFiles   []string
}

// LoadArchRules reads harness/architecture.json.
func LoadArchRules(root string) (*ArchRules, error) {
	var r ArchRules
	if err := readJSON(filepath.Join(root, filepath.FromSlash(ArchitectureFile)), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListPackages runs `go list -json ./...` and returns the module path and its
// packages.
func ListPackages(root string) (string, []Pkg, error) {
	mod, err := runCmd(root, nil, "go", "list", "-m")
	if err != nil {
		return "", nil, fmt.Errorf("go list -m: %w\n%s", err, mod)
	}
	out, err := runCmd(root, nil, "go", "list", "-json=ImportPath,Imports,CgoFiles", "./...")
	if err != nil {
		return "", nil, fmt.Errorf("go list: %w\n%s", err, out)
	}
	var pkgs []Pkg
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	for {
		var p Pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return "", nil, err
		}
		pkgs = append(pkgs, p)
	}
	return strings.TrimSpace(mod), pkgs, nil
}

// matches reports whether rel is pattern or one of its sub-packages.
func matches(pattern, rel string) bool {
	return rel == pattern || strings.HasPrefix(rel, pattern+"/")
}

// CheckArch applies the rules to the packages and returns every violation.
func CheckArch(rules *ArchRules, module string, pkgs []Pkg) []string {
	rel := func(p string) (string, bool) {
		if p == module {
			return ".", true
		}
		if strings.HasPrefix(p, module+"/") {
			return strings.TrimPrefix(p, module+"/"), true
		}
		return p, false
	}
	var v []string
	for _, p := range pkgs {
		from, _ := rel(p.ImportPath)

		// Layer rule: the most specific matching layer wins.
		var layer *Layer
		for i := range rules.Layers {
			l := &rules.Layers[i]
			if matches(l.Package, from) && (layer == nil || len(l.Package) > len(layer.Package)) {
				layer = l
			}
		}
		if layer == nil {
			v = append(v, fmt.Sprintf("%s: package is not covered by any layer in %s", from, ArchitectureFile))
		}

		for _, imp := range p.Imports {
			to, internal := rel(imp)
			if internal && layer != nil && !allowed(layer.MayImport, to) {
				v = append(v, fmt.Sprintf("%s imports %s, which layer %q does not allow", from, to, layer.Package))
			}
			for _, r := range rules.Restricted {
				if imp == r.Import && !anyMatch(r.AllowedIn, from) {
					v = append(v, fmt.Sprintf("%s imports %s, which is only allowed in %s", from, imp, strings.Join(r.AllowedIn, ", ")))
				}
			}
		}
		if len(p.CgoFiles) > 0 {
			for _, r := range rules.Restricted {
				if r.Import == "C" && !anyMatch(r.AllowedIn, from) {
					v = append(v, fmt.Sprintf("%s uses cgo, which is not allowed (single static binaries are a product requirement)", from))
				}
			}
		}
	}
	sort.Strings(v)
	return v
}

func allowed(mayImport []string, to string) bool {
	for _, a := range mayImport {
		if a == "*" || matches(a, to) {
			return true
		}
	}
	return false
}

func anyMatch(patterns []string, rel string) bool {
	for _, p := range patterns {
		if matches(p, rel) {
			return true
		}
	}
	return false
}

// Arch loads the rules and the real import graph and returns the violations.
func Arch(root string) ([]string, error) {
	rules, err := LoadArchRules(root)
	if err != nil {
		return nil, err
	}
	module, pkgs, err := ListPackages(root)
	if err != nil {
		return nil, err
	}
	return CheckArch(rules, module, pkgs), nil
}
