//go:build !windows

package client_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aotus/internal/client"
	"aotus/internal/provider/providertest"
)

func TestClientAttachesToTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := startDaemon(t).client(t)
	p := fakeProfile(t, c, "tui", providertest.TUI, "terminal")
	e, _ := c.CreateEmployee(ctx, client.NewEmployee{Name: "Atlas", ProfileID: p.ID})
	if _, err := c.Attach(ctx, e.ID); !client.HasCode(err, "http_409") {
		t.Fatalf("attaching before the terminal runs: %v, want a 409", err)
	}
	if err := c.StartTerminal(ctx, e.ID, 24, 80); err != nil {
		t.Fatal(err)
	}
	term, err := c.Attach(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = term.Close() }()
	read := func(want string) {
		t.Helper()
		var got string
		for !strings.Contains(got, want) {
			b, err := term.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %q: %v (got %q)", want, err, got)
			}
			got += string(b)
		}
	}
	read("FAKE-TUI ready")
	if err := term.Write(ctx, []byte("hi\r")); err != nil {
		t.Fatal(err)
	}
	read("you said: hi")
	if err := term.Resize(ctx, 40, 120); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	_ = term.Write(ctx, []byte("/size\r"))
	read("size:40x120")
	if err := c.StopTerminal(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := term.Read(ctx); err != nil {
			if !client.Ended(err) {
				t.Fatalf("the stream should end normally, got %v", err)
			}
			break
		}
	}
}
