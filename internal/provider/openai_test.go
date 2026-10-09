package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
)

const apiKey = "sk-test-key-0123456789"

// fakeAPI is an OpenAI-compatible server that behaves as the scenario says.
type fakeAPI struct {
	srv      *httptest.Server
	scenario providertest.Scenario
	requests atomic.Int32
	canceled chan struct{} // closed when a request was abandoned by the client

	mu   sync.Mutex
	last map[string]any
	auth string
	path string
}

func newFakeAPI(t *testing.T, sc providertest.Scenario) *fakeAPI {
	t.Helper()
	f := &fakeAPI{scenario: sc, canceled: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.last, f.auth, f.path = req, r.Header.Get("Authorization"), r.URL.Path
	f.mu.Unlock()

	switch f.scenario {
	case providertest.Fail:
		w.WriteHeader(http.StatusUnauthorized)
		// Real servers sometimes echo the key in this message.
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, apiKey)
		return
	case providertest.Sleep, providertest.Interruptible:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			select {
			case <-f.canceled:
			default:
				close(f.canceled)
			}
		case <-time.After(time.Minute):
		}
		return
	}

	// Hello: stream the answer. On a later turn, prove the history arrived.
	answer := []string{"po", "ng"}
	if msgs, _ := req["messages"].([]any); len(msgs) > 1 {
		for _, m := range msgs {
			if mm, _ := m.(map[string]any); mm["role"] == "assistant" {
				answer = []string{"resumed:" + fmt.Sprint(mm["content"])}
			}
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, piece := range answer {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", piece)
	}
	fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func apiProfile(f *fakeAPI) provider.Profile {
	return provider.Profile{
		ID: "openai-api-test-1", Name: "api", Kind: provider.KindOpenAI, Mode: provider.ModeAPI,
		BaseURL: f.srv.URL + "/v1/", Model: "test-model", APIKeyRef: "profile/openai-api-test-1/api-key",
	}
}

func keys(calls *atomic.Int32) provider.KeySource {
	return func(ref string) (string, error) {
		calls.Add(1)
		if ref == "profile/openai-api-test-1/api-key" {
			return apiKey, nil
		}
		return "", fmt.Errorf("no key for %s", ref)
	}
}

func startAPI(t *testing.T, f *fakeAPI, calls *atomic.Int32, system string) provider.Session {
	t.Helper()
	s, err := provider.OpenAICompat{Keys: keys(calls)}.Start(context.Background(), provider.SessionRequest{Profile: apiProfile(f), SystemPrompt: system})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAICompatPassesContract(t *testing.T) {
	var calls atomic.Int32
	providertest.RunContract(t, providertest.Contract{
		Process:    false,
		ResumeWant: func(string) string { return "resumed:pong" }, // the server proves it saw the history
		New: func(t *testing.T, sc providertest.Scenario) (provider.Session, providertest.Probe) {
			t.Helper()
			return startAPI(t, newFakeAPI(t, sc), &calls, ""), providertest.Probe{}
		},
	})
}

func TestOpenAICompatRequestShape(t *testing.T) {
	f := newFakeAPI(t, providertest.Hello)
	var calls atomic.Int32
	s := startAPI(t, f, &calls, "Be brief")
	events := runTurn(t, s, "hello there")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatal(err)
	}
	if done := events[len(events)-1].Done; done.InputTokens != 7 || done.OutputTokens != 2 {
		t.Fatalf("done = %+v, want the usage the server reported", done)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "/v1/chat/completions" {
		t.Errorf("path = %q (a trailing slash in the base URL must not double up)", f.path)
	}
	if f.auth != "Bearer "+apiKey {
		t.Errorf("Authorization header = %q", f.auth)
	}
	if f.last["model"] != "test-model" || f.last["stream"] != true {
		t.Errorf("request = %v", f.last)
	}
	msgs, _ := f.last["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "hello there" {
		t.Errorf("messages = %v, want the system prompt and the user prompt", msgs)
	}
}

func TestOpenAICompatCancelsRequest(t *testing.T) {
	f := newFakeAPI(t, providertest.Sleep)
	var calls atomic.Int32
	s := startAPI(t, f, &calls, "")
	if err := s.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the request reach the server
	if err := s.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never saw the connection close: the request must be canceled, not abandoned")
	}
	for ev := range s.Events() {
		if ev.Kind == provider.EventDone {
			if ev.Done.Reason != provider.DoneCanceled {
				t.Fatalf("done = %+v, want canceled", ev.Done)
			}
			return
		}
	}
	t.Fatal("no done event")
}

func TestOpenAICompatDetect(t *testing.T) {
	f := newFakeAPI(t, providertest.Hello)
	var calls atomic.Int32
	o := provider.OpenAICompat{Keys: keys(&calls)}
	d, err := o.Detect(context.Background(), apiProfile(f))
	if err != nil || d.Login != provider.LoginLoggedIn || len(d.Modes) != 1 || d.Modes[0] != provider.ModeAPI {
		t.Fatalf("with a key: %+v, %v", d, err)
	}
	noKey := apiProfile(f)
	noKey.APIKeyRef = "profile/other/api-key"
	if d, _ := o.Detect(context.Background(), noKey); d.Login != provider.LoginLoggedOut || d.Detail == "" {
		t.Fatalf("without a key: %+v", d)
	}
	if f.requests.Load() != 0 {
		t.Fatal("detecting must not call the API")
	}
	if _, err := o.Start(context.Background(), provider.SessionRequest{Profile: provider.Profile{ID: "x", Kind: provider.KindOpenAI}}); err == nil {
		t.Fatal("a profile without a model must be refused")
	}
}

// The key lives in the credential store only: it is fetched for each request,
// never shown in events (even if the server echoes it) and never written to
// the data directory, database included.
func TestAPIKeyNeverWrittenToDatabase(t *testing.T) {
	l := layout(t)
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var calls atomic.Int32
	ok := newFakeAPI(t, providertest.Hello)
	s := startAPI(t, ok, &calls, "")
	runTurn(t, s, "one")
	runTurn(t, s, "two")
	if calls.Load() != 2 {
		t.Fatalf("the key was fetched %d times for 2 turns: it must be read per request and never cached", calls.Load())
	}

	bad := newFakeAPI(t, providertest.Fail)
	failing := startAPI(t, bad, &calls, "")
	events := runTurn(t, failing, "three")
	for _, e := range events {
		if strings.Contains(e.Text, apiKey) {
			t.Fatalf("the server echoed the key and it reached an event: %+v", e)
		}
	}
	var sawNeedsLogin bool
	for _, e := range events {
		sawNeedsLogin = sawNeedsLogin || (e.Kind == provider.EventError && e.Code == provider.CodeNeedsLogin && strings.Contains(e.Text, "[redacted]"))
	}
	if !sawNeedsLogin {
		t.Fatalf("a rejected key must be a needs_login error with the key redacted: %v", events)
	}

	// Close the store so its write-ahead log is flushed, then scan every file.
	_ = st.Close()
	err = filepath.WalkDir(l.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), apiKey) {
			t.Errorf("%s contains the API key", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
