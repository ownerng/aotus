package credstore

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestRoundTrip(t *testing.T) {
	keyring.MockInit() // an in-memory credential store: tests never touch the real one
	var s Store
	ref := APIKeyRef("openai-api-work-1234")

	if _, err := s.Get(ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before Set = %v, want ErrNotFound", err)
	}
	if err := s.Set(ref, "sk-secret-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ref); err != nil || got != "sk-secret-1" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Set(ref, "sk-secret-2"); err != nil { // replaces
		t.Fatal(err)
	}
	if got, _ := s.Get(ref); got != "sk-secret-2" {
		t.Fatalf("Get after replace = %q", got)
	}
	if err := s.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ref); err != nil {
		t.Fatalf("deleting a missing key must not fail: %v", err)
	}
	if _, err := s.Get(ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := s.Set(ref, ""); err == nil {
		t.Fatal("an empty key must be refused")
	}
}

func TestRefsAreNotSecret(t *testing.T) {
	if got := APIKeyRef("p1"); got != "profile/p1/api-key" {
		t.Fatalf("APIKeyRef = %q", got)
	}
}
