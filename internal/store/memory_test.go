package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeNote(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func refs(hits []Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Kind+":"+h.Ref)
	}
	return strings.Join(out, ",")
}

func TestMemorySearchFindsFactsAndNotes(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	notes := t.TempDir()
	now := time.Now()

	id, err := s.AddFact(ctx, "atlas", "deploy", "Production deploys happen on Tuesdays after the standup", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFact(ctx, "atlas", "coffee", "The team prefers espresso", now); err != nil {
		t.Fatal(err)
	}
	writeNote(t, notes, "projects/billing.md", "# Billing\nThe invoice export runs nightly and writes CSV files.")
	writeNote(t, notes, "people.md", "Ana owns the deploy checklist.")
	writeNote(t, notes, "ignored.txt", "invoice but not markdown")
	res, err := s.SyncNotes(ctx, "atlas", notes)
	if err != nil || res.Indexed != 2 {
		t.Fatalf("sync = %+v, %v; want 2 notes indexed (the .txt file is not memory)", res, err)
	}

	for query, want := range map[string]string{
		"deploys tuesdays":   "fact:" + itoa(id), // all words, any order
		"tuesdays deploys":   "fact:" + itoa(id),
		"invoice CSV":        "note:projects/billing.md", // inside a note
		"espresso":           "fact:" + itoa(id+1),
		"billing":            "note:projects/billing.md", // the file name is searchable
		"depl":               "",                         // prefix on the last word only: "depl" matches deploy...
		"DEPLOY":             "",                         // case-insensitive
		"nothing like this":  "none",
		"invoice not-in-doc": "none", // every word must match
		`"; DROP TABLE x;--`: "none", // operators in the text mean nothing
		"":                   "none",
	} {
		hits, err := s.SearchMemory(ctx, "atlas", query, 10)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		got := refs(hits)
		switch want {
		case "none":
			if got != "" {
				t.Errorf("%q found %s, want nothing", query, got)
			}
		case "":
			if got == "" {
				t.Errorf("%q found nothing", query)
			}
		default:
			if !strings.Contains(got, want) {
				t.Errorf("%q found %q, want it to include %s", query, got, want)
			}
		}
	}
	hits, _ := s.SearchMemory(ctx, "atlas", "invoice", 10)
	if len(hits) != 1 || !strings.Contains(hits[0].Snippet, "[invoice]") {
		t.Fatalf("hits = %+v, want one snippet with the match highlighted", hits)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "deploy", 1); len(hits) != 1 {
		t.Errorf("limit not applied: %d hits", len(hits))
	}

	// Forgetting a fact removes it from search too.
	if err := s.DeleteFact(ctx, "atlas", id); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "tuesdays", 10); len(hits) != 0 {
		t.Errorf("a forgotten fact is still searchable: %v", hits)
	}
	if err := s.DeleteFact(ctx, "atlas", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice = %v, want ErrNotFound", err)
	}
	big := strings.Repeat("x", maxFactBytes+1)
	if _, err := s.AddFact(ctx, "atlas", "", big, now); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversized fact = %v, want ErrTooLarge", err)
	}
	if _, err := s.AddFact(ctx, "", "", "x", now); err == nil {
		t.Error("a fact needs an employee")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestMemoryIsolatedPerEmployee(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	now := time.Now()
	atlasNotes, brunoNotes := t.TempDir(), t.TempDir()

	secret, _ := s.AddFact(ctx, "atlas", "", "The vault code is swordfish", now)
	_, _ = s.AddFact(ctx, "bruno", "", "Bruno likes sailing", now)
	writeNote(t, atlasNotes, "private.md", "swordfish is also in this note")
	writeNote(t, brunoNotes, "mine.md", "sailing notes")
	for id, dir := range map[string]string{"atlas": atlasNotes, "bruno": brunoNotes} {
		if _, err := s.SyncNotes(ctx, id, dir); err != nil {
			t.Fatal(err)
		}
	}

	if hits, _ := s.SearchMemory(ctx, "bruno", "swordfish", 10); len(hits) != 0 {
		t.Fatalf("Bruno found Atlas's memory: %v", hits)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "sailing", 10); len(hits) != 0 {
		t.Fatalf("Atlas found Bruno's memory: %v", hits)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "swordfish", 10); len(hits) != 2 {
		t.Fatalf("Atlas must find its own fact and note, got %v", hits)
	}
	if facts, _ := s.Facts(ctx, "bruno"); len(facts) != 1 || strings.Contains(facts[0].Body, "swordfish") {
		t.Fatalf("Bruno's facts = %+v", facts)
	}
	if err := s.DeleteFact(ctx, "bruno", secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bruno deleting Atlas's fact = %v, want ErrNotFound", err)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "swordfish", 10); len(hits) != 2 {
		t.Fatal("Atlas's memory must be intact after Bruno's attempt")
	}
	if _, err := s.SearchMemory(ctx, "", "swordfish", 10); err == nil {
		t.Fatal("there must be no way to search without naming an employee")
	}
	// The same note path in two employees' folders stays separate.
	writeNote(t, atlasNotes, "same.md", "alpha content")
	writeNote(t, brunoNotes, "same.md", "bravo content")
	_, _ = s.SyncNotes(ctx, "atlas", atlasNotes)
	_, _ = s.SyncNotes(ctx, "bruno", brunoNotes)
	if hits, _ := s.SearchMemory(ctx, "atlas", "bravo", 10); len(hits) != 0 {
		t.Fatalf("notes with the same path leaked across employees: %v", hits)
	}
	// Syncing one employee's folder never touches another's index.
	if res, _ := s.SyncNotes(ctx, "atlas", t.TempDir()); res.Removed != 2 {
		t.Fatalf("an empty folder drops Atlas's notes: %+v", res)
	}
	if hits, _ := s.SearchMemory(ctx, "bruno", "bravo", 10); len(hits) != 1 {
		t.Fatalf("Bruno's notes must survive: %v", hits)
	}
}

func TestHandEditedNotesAreIndexed(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	dir := t.TempDir()
	writeNote(t, dir, "plan.md", "The launch is planned for March")
	if res, err := s.SyncNotes(ctx, "atlas", dir); err != nil || res.Indexed != 1 {
		t.Fatalf("first sync = %+v, %v", res, err)
	}
	if res, _ := s.SyncNotes(ctx, "atlas", dir); res.Indexed != 0 || res.Removed != 0 {
		t.Fatalf("nothing changed, so nothing is re-indexed: %+v", res)
	}

	// The user edits the file with their editor.
	writeNote(t, dir, "plan.md", "The launch moved to September, after the audit")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "plan.md"), future, future); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.SyncNotes(ctx, "atlas", dir); res.Indexed != 1 {
		t.Fatalf("the edited note must be picked up: %+v", res)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "September audit", 10); len(hits) != 1 {
		t.Fatalf("the new text must be searchable, got %v", hits)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "March", 10); len(hits) != 0 {
		t.Fatalf("the old text must be gone from the index, got %v", hits)
	}

	// New file in a subfolder; deleted file; a note that is too large; a link.
	writeNote(t, dir, "deep/er/idea.md", "A note about quasars")
	if err := os.Remove(filepath.Join(dir, "plan.md")); err != nil {
		t.Fatal(err)
	}
	writeNote(t, dir, "huge.md", strings.Repeat("a ", maxNoteBytes))
	outside := t.TempDir()
	writeNote(t, outside, "elsewhere.md", "outside content: pulsar")
	linked := os.Symlink(outside, filepath.Join(dir, "link")) == nil

	res, err := s.SyncNotes(ctx, "atlas", dir)
	if err != nil || res.Indexed != 1 || res.Removed != 1 || res.Skipped != 1 {
		t.Fatalf("sync = %+v, %v; want 1 indexed, 1 removed, 1 skipped (too large)", res, err)
	}
	if hits, _ := s.SearchMemory(ctx, "atlas", "quasars", 10); refs(hits) != "note:deep/er/idea.md" {
		t.Fatalf("hits = %v", hits)
	}
	if linked {
		if hits, _ := s.SearchMemory(ctx, "atlas", "pulsar", 10); len(hits) != 0 {
			t.Fatalf("a symbolic link must not pull outside files into memory: %v", hits)
		}
	}
	if res, err := s.SyncNotes(ctx, "atlas", filepath.Join(dir, "no-such-folder")); err != nil || res.Removed != 1 {
		t.Fatalf("a missing memory folder means no notes: %+v, %v", res, err)
	}
}

func TestMemorySurvivesProfileChange(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	now := time.Now()
	for _, id := range []string{"p-claude", "p-codex"} {
		if err := s.PutProfile(ctx, profileRow(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateEmployee(ctx, employeeRow("e1", "atlas", "p-claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFact(ctx, "e1", "", "Remember the quarterly report", now); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeNote(t, dir, "n.md", "quarterly numbers live in the spreadsheet")
	if _, err := s.SyncNotes(ctx, "e1", dir); err != nil {
		t.Fatal(err)
	}

	if err := s.SetEmployeeProfile(ctx, "e1", "p-codex", now); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Employee(ctx, "e1"); e.ProfileID != "p-codex" {
		t.Fatalf("profile = %s", e.ProfileID)
	}
	if hits, _ := s.SearchMemory(ctx, "e1", "quarterly", 10); len(hits) != 2 {
		t.Fatalf("memory must survive a change of profile, got %v", hits)
	}
	if facts, _ := s.Facts(ctx, "e1"); len(facts) != 1 {
		t.Fatalf("facts = %v", facts)
	}
	if err := s.SetEmployeeProfile(ctx, "e1", "no-such-profile", now); !errors.Is(err, ErrInUse) {
		t.Fatalf("an unknown profile = %v, want the foreign key to refuse it", err)
	}
	if err := s.SetEmployeeProfile(ctx, "ghost", "p-claude", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown employee = %v", err)
	}
}
