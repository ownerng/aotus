package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"aotus/internal/provider"
	"aotus/internal/workspace"
)

// SoulFile is the Markdown file in an employee's folder with its character,
// values and way of working. It is joined to the employee's instructions each
// time its session starts, so an edit (by the person or the employee) counts
// from the next session.
const SoulFile = "SOUL.md"

const (
	maxDescription = 4000
	maxSoul        = 32000
	draftTimeout   = 3 * time.Minute
)

// ErrDraft means the subscription answered, but not with a usable teammate.
var ErrDraft = errors.New("the draft could not be used")

// Draft is a proposed employee, for the person to review before hiring it.
type Draft struct {
	Name, Role, Instructions, Soul string
}

const draftSystemPrompt = "You design AI teammates for Aotus, an app where a person works with persistent AI teammates. " +
	"Each teammate has a name, a role, instructions and a SOUL.md. You only write; you never use tools, read or write files, or run commands."

func draftPrompt(description string) string {
	return "Draft one teammate for this request, written by the person who will work with it:\n<request>\n" + description + "\n</request>\n\n" +
		"Answer with one JSON object and nothing else. It has these string fields:\n" +
		"- name: a short first name for the teammate (one or two words, no title, not a famous person).\n" +
		"- role: what it does, in under 12 words.\n" +
		"- instructions: how it works for this person: what it is responsible for, what it asks before acting, how it reports back. 80 to 200 words, addressed to the teammate as \"you\".\n" +
		"- soul: the content of its SOUL.md in Markdown, with headings: its character, values, voice and working style. 120 to 300 words.\n" +
		"Write every field in the language of the request."
}

// DraftEmployee asks the subscription of profileID to draft a teammate from
// the person's description. It runs one short structured turn in a scratch
// folder that is removed afterwards; it is no employee's turn and is stored in
// no history. The answer is checked strictly: an unusable one is ErrDraft.
func (m *Manager) DraftEmployee(ctx context.Context, profileID, description string) (Draft, error) {
	description = strings.TrimSpace(description)
	switch {
	case description == "":
		return Draft{}, fmt.Errorf("%w: say in a few words what the teammate should help with", ErrInvalid)
	case utf8.RuneCountInString(description) > maxDescription:
		return Draft{}, fmt.Errorf("%w: the description is longer than %d characters", ErrInvalid, maxDescription)
	}
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return Draft{}, err
	}
	prov, ok := m.providers[p.Kind]
	if !ok {
		return Draft{}, fmt.Errorf("%w: %s", ErrNoProvider, p.Kind)
	}
	mode := provider.ModeStructured // a terminal profile still drafts as a chat
	if p.Kind == provider.KindOpenAI {
		mode = provider.ModeAPI
	}

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Draft{}, err
	}
	dir := filepath.Join(m.st.Layout().Root, "drafts", hex.EncodeToString(b[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Draft{}, fmt.Errorf("orchestrator: a scratch folder for the draft: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ctx, cancel := context.WithTimeout(ctx, draftTimeout)
	defer cancel()
	sess, err := prov.Start(ctx, provider.SessionRequest{Profile: p, Mode: mode, Dir: dir, SystemPrompt: draftSystemPrompt})
	if err != nil {
		return Draft{}, err
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Send(ctx, draftPrompt(description)); err != nil {
		return Draft{}, err
	}
	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			return Draft{}, fmt.Errorf("%w: the subscription did not answer in time", ErrDraft)
		case ev, ok := <-sess.Events():
			if !ok {
				return Draft{}, fmt.Errorf("%w: the CLI stopped before answering", ErrDraft)
			}
			switch ev.Kind {
			case provider.EventText:
				text.WriteString(ev.Text)
			case provider.EventError:
				return Draft{}, fmt.Errorf("%w: %s", ErrDraft, ev.Text)
			case provider.EventDone:
				return parseDraft(text.String())
			}
		}
	}
}

// parseDraft reads the JSON object out of a model's answer (which may wrap it
// in a fenced block or a sentence) and checks every field.
func parseDraft(answer string) (Draft, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end <= start {
		return Draft{}, fmt.Errorf("%w: the answer had no teammate in it", ErrDraft)
	}
	var raw struct {
		Name         string `json:"name"`
		Role         string `json:"role"`
		Instructions string `json:"instructions"`
		Soul         string `json:"soul"`
	}
	if err := json.Unmarshal([]byte(answer[start:end+1]), &raw); err != nil {
		return Draft{}, fmt.Errorf("%w: the teammate in the answer was not readable: %w", ErrDraft, err)
	}
	d := Draft{
		Name:         strings.Trim(strings.TrimSpace(raw.Name), `"'`),
		Role:         strings.TrimSpace(raw.Role),
		Instructions: strings.TrimSpace(raw.Instructions),
		Soul:         strings.TrimSpace(raw.Soul),
	}
	switch {
	case d.Name == "" || d.Instructions == "" || d.Soul == "":
		return Draft{}, fmt.Errorf("%w: the answer was missing a name, instructions or a soul", ErrDraft)
	case utf8.RuneCountInString(d.Name) > maxName:
		return Draft{}, fmt.Errorf("%w: the name is longer than %d characters", ErrDraft, maxName)
	case utf8.RuneCountInString(d.Instructions) > maxPrompt, utf8.RuneCountInString(d.Soul) > maxSoul:
		return Draft{}, fmt.Errorf("%w: the answer was too long", ErrDraft)
	}
	if _, err := workspace.Slug(d.Name); err != nil {
		return Draft{}, fmt.Errorf("%w: the name %q cannot name a folder", ErrDraft, d.Name)
	}
	if r := []rune(d.Role); len(r) > maxRole {
		d.Role = string(r[:maxRole])
	}
	return d, nil
}

// briefFor is what the CLI is given as the employee's standing instructions:
// its instructions and, when it has one, its SOUL.md.
func briefFor(instructions, dir string) string {
	soul := readSoul(dir)
	switch {
	case soul == "":
		return instructions
	case strings.TrimSpace(instructions) == "":
		return "Your SOUL.md (your character and way of working):\n\n" + soul
	}
	return instructions + "\n\nYour SOUL.md (your character and way of working):\n\n" + soul
}

// readSoul returns the employee's SOUL.md, bounded; a missing or unreadable
// file is no soul.
func readSoul(dir string) string {
	f, err := os.Open(filepath.Join(dir, SoulFile)) //nolint:gosec // the employee's own folder, made by Aotus
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(io.LimitReader(f, maxSoul*4))
	return strings.TrimSpace(string(b))
}
