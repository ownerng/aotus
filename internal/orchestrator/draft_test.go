package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
)

func TestDraftEmployeeParsesTheModelsAnswer(t *testing.T) {
	const obj = `{"name": "Vera", "role": "Reviews pull requests", "instructions": "You review PRs.", "soul": "# Vera\nCalm."}`
	for name, answer := range map[string]string{
		"bare":            obj,
		"fenced":          "```json\n" + obj + "\n```",
		"in a sentence":   "Here is your teammate: " + obj + " Let me know.",
		"other language":  `{"name": "Lucía", "role": "Revisa contratos", "instructions": "Revisas cada contrato.", "soul": "# Lucía\nSerena."}`,
		"quoted name":     `{"name": "\"Vera\"", "role": "r", "instructions": "i", "soul": "s"}`,
		"surrounding ws":  "\n\n  " + obj + "  \n",
		"role is clipped": `{"name": "Vera", "role": "` + strings.Repeat("r", maxRole+50) + `", "instructions": "i", "soul": "s"}`,
	} {
		d, err := parseDraft(answer)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if d.Name == "" || strings.Contains(d.Name, `"`) || d.Instructions == "" || d.Soul == "" || len([]rune(d.Role)) > maxRole {
			t.Errorf("%s: draft = %+v", name, d)
		}
	}

	// End to end with the fake CLI: one structured turn, nothing created.
	r := newRig(t, Options{})
	p := r.profile(t, "drafter", providertest.Drafted, provider.ModeTerminal) // a terminal profile still drafts as a chat
	before, _ := r.svc.Employees(context.Background())
	d, err := r.mgr.DraftEmployee(context.Background(), p.ID, "Someone to review the PRs of the payments service")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Vera" || !strings.Contains(d.Role, "pull requests") || !strings.Contains(d.Soul, "## Character") || !strings.Contains(d.Instructions, "Ask before") {
		t.Fatalf("draft = %+v", d)
	}
	after, _ := r.svc.Employees(context.Background())
	if len(after) != len(before) {
		t.Fatal("drafting created an employee")
	}
	if left, _ := os.ReadDir(filepath.Join(r.layout.Root, "drafts")); len(left) != 0 {
		t.Fatalf("the scratch folder was left behind: %v", left)
	}
}

func TestDraftEmployeeRejectsAnUnusableAnswer(t *testing.T) {
	for name, answer := range map[string]string{
		"no json":          "Sure! Vera would be a great reviewer.",
		"broken json":      `{"name": "Vera", "role": `,
		"missing soul":     `{"name": "Vera", "role": "r", "instructions": "i"}`,
		"missing name":     `{"role": "r", "instructions": "i", "soul": "s"}`,
		"name too long":    `{"name": "` + strings.Repeat("V", maxName+1) + `", "role": "r", "instructions": "i", "soul": "s"}`,
		"unusable name":    `{"name": "!!!", "role": "r", "instructions": "i", "soul": "s"}`,
		"soul far too big": `{"name": "Vera", "role": "r", "instructions": "i", "soul": "` + strings.Repeat("s", maxSoul+1) + `"}`,
	} {
		if _, err := parseDraft(answer); !errors.Is(err, ErrDraft) {
			t.Errorf("%s: %v; want ErrDraft", name, err)
		}
	}

	r := newRig(t, Options{})
	pong := r.profile(t, "pong", providertest.Hello, provider.ModeStructured)
	if _, err := r.mgr.DraftEmployee(context.Background(), pong.ID, "a reviewer"); !errors.Is(err, ErrDraft) {
		t.Fatalf("a subscription that answers pong: %v; want ErrDraft", err)
	}
	failing := r.profile(t, "failing", providertest.Fail, provider.ModeStructured)
	if _, err := r.mgr.DraftEmployee(context.Background(), failing.ID, "a reviewer"); err == nil {
		t.Fatal("a subscription that fails gave a draft")
	}
	if _, err := r.mgr.DraftEmployee(context.Background(), pong.ID, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty description: %v; want ErrInvalid", err)
	}
}

func TestHiredEmployeeGetsItsSoul(t *testing.T) {
	r := newRig(t, Options{})
	p := r.profile(t, "argv", providertest.Argv, provider.ModeStructured)
	const soul = "# Vera\n\n## Values\nCorrectness first."
	emp, err := r.svc.CreateEmployee(context.Background(), NewEmployee{Name: "Vera", Role: "reviewer", SystemPrompt: "You review PRs.", ProfileID: p.ID, Soul: soul})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := r.svc.ws.Dir(emp.Slug)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, SoulFile))
	if err != nil || strings.TrimSpace(string(b)) != soul {
		t.Fatalf("SOUL.md = %q, %v; want the soul in the employee's folder", b, err)
	}

	// The CLI is given the instructions and the soul.
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()
	turn, err := r.mgr.Send(context.Background(), emp.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	var args strings.Builder
	waitUpdate(t, sub, func(u Update) bool {
		if u.Event != nil && strings.HasPrefix(u.Event.Text, "ARG:") {
			args.WriteString(u.Event.Text + "\n")
		}
		return u.Kind == UpdateTurnEnded && u.TurnID == turn
	})
	for _, want := range []string{"You review PRs.", "Correctness first.", "SOUL.md"} {
		if !strings.Contains(args.String(), want) {
			t.Errorf("the CLI was not given %q:\n%s", want, args.String())
		}
	}

	// Without a soul there is no file and the instructions go alone.
	plain, err := r.svc.CreateEmployee(context.Background(), NewEmployee{Name: "Plain", SystemPrompt: "Be brief.", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	pdir, _ := r.svc.ws.Dir(plain.Slug)
	if _, err := os.Stat(filepath.Join(pdir, SoulFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an employee hired without a soul got a SOUL.md")
	}
	if got := briefFor("Be brief.", pdir); got != "Be brief." {
		t.Fatalf("briefFor without a soul = %q", got)
	}
}
