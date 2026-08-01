package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newClient(t *testing.T, baseURL string, style APIStyle) Client {
	t.Helper()
	c, err := New(Options{
		BaseURL: baseURL,
		APIKey:  "sk-test-secret",
		Model:   "test-model",
		Style:   style,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestClientChatCompletions(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotModel = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id":"chatcmpl-1","model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"{\"ok\":true}"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	resp, err := c.GenerateJSON(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"ok":true}` {
		t.Fatalf("text=%q", resp.Text)
	}
	if resp.Usage.TotalTokens != 15 || resp.Usage.PromptTokens != 10 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
	if resp.Model != "test-model" {
		t.Fatalf("model=%q", resp.Model)
	}
	if gotAuth != "Bearer sk-test-secret" {
		t.Fatalf("authorization=%q", gotAuth)
	}
	if !strings.Contains(gotModel, "application/json") {
		t.Fatalf("content-type=%q", gotModel)
	}
}

func TestClientResponsesStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id":"resp-1","model":"test-model",
			"output_text":"{\"candidates\":[]}",
			"usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12}
		}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleResponses)
	resp, err := c.GenerateJSON(context.Background(), Request{Input: "diagnose"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"candidates":[]}` {
		t.Fatalf("text=%q", resp.Text)
	}
	if resp.Usage.TotalTokens != 12 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestClientRetriesOn429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n <= 2 {
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	start := time.Now()
	c := newClient(t, srv.URL, StyleChatCompletions)
	resp, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" {
		t.Fatalf("text=%q", resp.Text)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3", calls.Load())
	}
	elapsed := time.Since(start)
	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected ~750ms backoff, got %s", elapsed)
	}
}

func TestClientNoRetryOn500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":{"message":"server error"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1 (no retry on 500)", calls.Load())
	}
}

func TestClientRetriesThenFailsOn503(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":{"message":"unavailable"}}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error on persistent 503")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3", calls.Load())
	}
}

func TestClientRejectsNonLoopbackHTTP(t *testing.T) {
	_, err := New(Options{BaseURL: "http://example.com/v1", APIKey: "k", Model: "m"})
	if err == nil {
		t.Fatal("expected error for plain http on non-loopback host")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error=%v", err)
	}
}

func TestClientRejectsUnsupportedStyle(t *testing.T) {
	_, err := New(Options{BaseURL: "https://api.example.com", APIKey: "k", Model: "m", Style: "banana"})
	if err == nil {
		t.Fatal("expected error for unsupported style")
	}
}

func TestClientTimeout(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"slow"}}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL, APIKey: "k", Model: "m", Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1 (no retry after timeout)", calls.Load())
	}
}

func TestClientContextCancelledNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.GenerateJSON(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want context.DeadlineExceeded", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1 (context cancel must not retry)", calls.Load())
	}
}

func TestClientHTMLResponseRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>proxy login page</body></html>"))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err=%v want ErrInvalidResponse", err)
	}
}

func TestClientInvalidJSONRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err=%v want ErrInvalidResponse", err)
	}
}

func TestClientRedactsAPIKeyInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"sk-test-secret leaked in body Bearer sk-test-secret"}`, http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-test-secret") {
		t.Fatalf("api key leaked in error: %v", err)
	}
}

func TestClientErrorBodyBounded(t *testing.T) {
	big := strings.Repeat("A", maxErrorBodyBytes*4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, big, http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL, StyleChatCompletions)
	_, err := c.GenerateJSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > maxErrorBodyBytes*2+256 {
		t.Fatalf("error too long: %d bytes", len(err.Error()))
	}
}
