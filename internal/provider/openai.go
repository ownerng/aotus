package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// KeySource returns the API key stored under a reference. The daemon wires it
// to the operating system credential store; the provider never keeps a key
// beyond a single request.
type KeySource func(ref string) (string, error)

// DefaultOpenAIBaseURL is used when a profile names no endpoint.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAICompat talks to any OpenAI-compatible chat completions endpoint with
// the user's own API key. It is the fallback when a provider's CLI cannot be
// used (docs/research/provider-terms.md). The API is stateless, so the session
// keeps the conversation in memory.
type OpenAICompat struct {
	Keys KeySource
	// HTTP is the client used for requests; nil means a client without a
	// total timeout (streams can be long).
	HTTP *http.Client
}

// Kind implements Provider.
func (OpenAICompat) Kind() Kind { return KindOpenAI }

func (o OpenAICompat) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{}
}

// Detect implements Provider. There is no CLI: the question is whether a key
// is stored for the profile.
func (o OpenAICompat) Detect(_ context.Context, p Profile) (Detection, error) {
	d := Detection{Installed: true, VersionOK: true, Modes: []Mode{ModeAPI}, Login: LoginLoggedOut}
	if p.APIKeyRef == "" || o.Keys == nil {
		d.Detail = "No API key is configured for this profile."
		return d, nil
	}
	if key, err := o.Keys(p.APIKeyRef); err != nil || key == "" {
		d.Detail = "No API key is stored for this profile. Add it in the profile settings."
		return d, nil
	}
	d.Login = LoginLoggedIn
	return d, nil
}

// Start implements Provider.
func (o OpenAICompat) Start(_ context.Context, req SessionRequest) (Session, error) {
	if m := firstMode(req.Mode, req.Profile.Mode, ModeAPI); m != ModeAPI {
		return nil, fmt.Errorf("%w: the API adapter only supports %q, not %q", ErrUnsupportedMode, ModeAPI, m)
	}
	if firstNonEmpty(req.Model, req.Profile.Model) == "" {
		return nil, fmt.Errorf("provider: profile %s needs a model for the API", req.Profile.ID)
	}
	s := &apiSession{
		o:       o,
		req:     req,
		events:  make(chan Event, 256),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
		id:      req.ResumeID,
	}
	return s, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiSession struct {
	o   OpenAICompat
	req SessionRequest

	events  chan Event
	closing chan struct{}
	done    chan struct{}

	mu      sync.Mutex
	id      string
	history []chatMessage
	cancel  context.CancelFunc // of the running turn, nil when idle
	stopped bool               // the running turn was asked to end
	closed  bool
	wg      sync.WaitGroup
}

func (s *apiSession) Mode() Mode { return ModeAPI }

func (s *apiSession) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *apiSession) Events() <-chan Event { return s.events }

func (s *apiSession) Done() <-chan struct{} { return s.done }

func (s *apiSession) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.closing:
	}
}

func (s *apiSession) Send(ctx context.Context, prompt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.cancel != nil {
		return ErrTurnActive
	}
	newID := ""
	if s.id == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		s.id = "api-" + hex.EncodeToString(b[:])
		newID = s.id
	}
	turnCtx, cancel := context.WithCancel(ctx)
	s.cancel, s.stopped = cancel, false
	messages := make([]chatMessage, 0, len(s.history)+2)
	if sp := s.req.SystemPrompt; sp != "" {
		messages = append(messages, chatMessage{Role: "system", Content: sp})
	}
	messages = append(append(messages, s.history...), chatMessage{Role: "user", Content: prompt})

	s.wg.Add(1)
	go s.run(turnCtx, cancel, newID, prompt, messages)
	return nil
}

func (s *apiSession) run(ctx context.Context, cancel context.CancelFunc, newID, prompt string, messages []chatMessage) {
	defer s.wg.Done()
	defer cancel()
	if newID != "" {
		s.emit(Event{Kind: EventSession, SessionID: newID})
	}

	answer, done, errEv := s.stream(ctx, messages)

	s.mu.Lock()
	stopped := s.stopped || ctx.Err() != nil
	if done.Reason == DoneCompleted && !stopped {
		s.history = append(s.history, chatMessage{Role: "user", Content: prompt}, chatMessage{Role: "assistant", Content: answer})
	}
	s.cancel = nil // the session is free before the consumer sees "done"
	s.mu.Unlock()

	switch {
	case stopped:
		done.Reason = DoneCanceled
	case errEv != nil:
		s.emit(*errEv)
		done.Reason = DoneFailed
	}
	s.emit(Event{Kind: EventDone, Done: &done})
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// stream performs one request and emits text events as they arrive. It
// returns the full answer, the done details and the error to report, if any.
func (s *apiSession) stream(ctx context.Context, messages []chatMessage) (string, Done, *Event) {
	done := Done{Reason: DoneCompleted}
	fail := func(code ErrorCode, format string, a ...any) (string, Done, *Event) {
		return "", done, &Event{Kind: EventError, Code: code, Text: fmt.Sprintf(format, a...)}
	}

	p := s.req.Profile
	if s.o.Keys == nil || p.APIKeyRef == "" {
		return fail(CodeNeedsLogin, "No API key is configured for this profile.")
	}
	key, err := s.o.Keys(p.APIKeyRef)
	if err != nil || key == "" {
		return fail(CodeNeedsLogin, "No API key is stored for this profile. Add it in the profile settings.")
	}

	body, _ := json.Marshal(map[string]any{
		"model":          firstNonEmpty(s.req.Model, p.Model),
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	})
	url := strings.TrimRight(firstNonEmpty(p.BaseURL, DefaultOpenAIBaseURL), "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fail(CodeInternal, "Invalid endpoint %q.", url)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := s.o.client().Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", done, nil // canceled
		}
		return fail(CodeCLI, "Could not reach %s: %s", redact(url, key), redact(err.Error(), key))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		msg := apiErrorMessage(resp.Body, key)
		switch {
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
			return fail(CodeNeedsLogin, "The API rejected the key (%d): %s", resp.StatusCode, msg)
		case resp.StatusCode == http.StatusTooManyRequests:
			return fail(CodeRateLimited, "The API is rate limiting this key: %s", msg)
		case resp.StatusCode == http.StatusNotFound, strings.Contains(strings.ToLower(msg), "model"):
			return fail(CodeModelUnsupported, "The API does not accept this model (%d): %s", resp.StatusCode, msg)
		}
		return fail(CodeCLI, "The API answered %d: %s", resp.StatusCode, msg)
	}

	var answer strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue // tolerate lines that are not ours
		}
		if chunk.Usage != nil {
			done.InputTokens, done.OutputTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				answer.WriteString(c.Delta.Content)
				s.emit(Event{Kind: EventText, Text: c.Delta.Content})
			}
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return answer.String(), done, &Event{Kind: EventError, Code: CodeCLI, Text: "The stream was interrupted: " + redact(err.Error(), key)}
	}
	return answer.String(), done, nil
}

// apiErrorMessage reads the message of an OpenAI-style error body.
func apiErrorMessage(r io.Reader, key string) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 4096))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return redact(msg, key)
}

// redact removes the key from text that may be shown to the user or logged:
// some servers echo part of the credential in their errors.
func redact(text, key string) string {
	if key == "" {
		return text
	}
	return strings.ReplaceAll(text, key, "[redacted]")
}

func (s *apiSession) Interrupt() error { return s.CancelTurn() }

func (s *apiSession) CancelTurn() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return ErrNoTurn
	}
	s.stopped = true
	s.cancel()
	return nil
}

func (s *apiSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.stopped = true
		s.cancel()
	}
	s.mu.Unlock()

	close(s.closing)
	s.wg.Wait()
	close(s.events)
	close(s.done)
	return nil
}
