package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxRetries = 2 // extra attempts on top of the initial request

	// maxResponseBytes bounds a successful model response body.
	maxResponseBytes = 1 << 20
	// maxErrorBodyBytes bounds the portion of an error body surfaced to callers.
	maxErrorBodyBytes = 512
)

// ErrInvalidResponse marks responses that cannot be parsed as a model result
// (HTML, invalid JSON, missing fields).
var ErrInvalidResponse = errors.New("invalid model response")

// Client issues model generation calls against an OpenAI-compatible endpoint.
type Client interface {
	GenerateJSON(ctx context.Context, request Request) (Response, error)
}

// Options configures an OpenAI-compatible client.
type Options struct {
	BaseURL string
	APIKey  string
	Model   string
	Style   APIStyle
	Timeout time.Duration
	// HTTP overrides the transport (used by tests via httptest).
	HTTP *http.Client
}

type httpClient struct {
	baseURL *url.URL
	apiKey  string
	model   string
	style   APIStyle
	timeout time.Duration
	http    *http.Client
}

// New validates the options and returns a client. The base URL must be HTTPS
// unless it points at a loopback host (tests use plain HTTP against httptest).
func New(opts Options) (Client, error) {
	if opts.Style == "" {
		opts.Style = StyleChatCompletions
	}
	if opts.Style != StyleChatCompletions && opts.Style != StyleResponses {
		return nil, fmt.Errorf("unsupported api style %q (want chat_completions or responses)", opts.Style)
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse llm base url: %w", err)
	}
	if base.Scheme != "https" && !isLoopbackHost(base.Hostname()) {
		return nil, fmt.Errorf("llm base url must use https, got %q", opts.BaseURL)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	return &httpClient{
		baseURL: base,
		apiKey:  opts.APIKey,
		model:   opts.Model,
		style:   opts.Style,
		timeout: timeout,
		http:    hc,
	}, nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" ||
		host == "::1" ||
		host == "[::1]" ||
		strings.HasPrefix(host, "127.")
}

func (c *httpClient) endpoint() string {
	if c.style == StyleResponses {
		return "/responses"
	}
	return "/chat/completions"
}

// GenerateJSON posts the request and parses the model text. Only 429, 502 and
// 503 are retried, with a 250ms/500ms backoff; context cancellation and other
// statuses never retry.
func (c *httpClient) GenerateJSON(ctx context.Context, request Request) (Response, error) {
	body, err := c.buildBody(request)
	if err != nil {
		return Response{}, err
	}

	var lastStatus int
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(retryDelay(attempt)):
			}
		}

		hd, err := c.do(ctx, body)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return Response{}, err
			}
			return Response{}, err
		}
		lastStatus = hd.status

		if hd.status == http.StatusTooManyRequests || hd.status == http.StatusBadGateway || hd.status == http.StatusServiceUnavailable {
			if attempt < maxRetries {
				continue
			}
			return Response{}, c.statusError(hd)
		}
		return c.parse(hd)
	}
	return Response{}, fmt.Errorf("llm request failed with status %d", lastStatus)
}

// retryDelay returns the backoff before the given attempt (1 or 2).
func retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 250 * time.Millisecond
	}
	return 500 * time.Millisecond
}

func (c *httpClient) buildBody(request Request) ([]byte, error) {
	if request.Model == "" {
		request.Model = c.model
	}
	var payload map[string]any
	switch c.style {
	case StyleResponses:
		input := request.Input
		if input == "" && len(request.Messages) > 0 {
			input = request.Messages[len(request.Messages)-1].Content
		}
		payload = map[string]any{"model": request.Model, "input": input}
		if request.JSONMode {
			payload["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
		}
	default:
		payload = map[string]any{"model": request.Model, "messages": request.Messages}
		if request.JSONMode {
			payload["response_format"] = map[string]any{"type": "json_object"}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal llm request: %w", err)
	}
	return raw, nil
}

// httpResponse carries a bounded, fully-read response body.
type httpResponse struct {
	status int
	body   []byte
	header http.Header
}

func (c *httpClient) do(ctx context.Context, body []byte) (*httpResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL.String()+c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build llm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBytes+1)))
	if err != nil {
		return nil, fmt.Errorf("read llm response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("llm response exceeds %d bytes", maxResponseBytes)
	}
	return &httpResponse{status: resp.StatusCode, body: raw, header: resp.Header}, nil
}

func (c *httpClient) statusError(hd *httpResponse) error {
	return fmt.Errorf("llm returned status %d: %s", hd.status, c.redact(string(hd.body)))
}

// redact masks credential material in model-provided text before it reaches
// callers or logs: pattern-based sanitization plus the configured API key.
func (c *httpClient) redact(text string) string {
	text = sanitizeErrorText(text)
	if c.apiKey != "" {
		text = strings.ReplaceAll(text, c.apiKey, "[REDACTED]")
	}
	return text
}

// parse turns a successful response into a normalized Response, rejecting HTML
// and invalid JSON with ErrInvalidResponse.
func (c *httpClient) parse(hd *httpResponse) (Response, error) {
	if strings.Contains(strings.ToLower(hd.header.Get("Content-Type")), "text/html") ||
		looksLikeHTML(hd.body) {
		return Response{}, fmt.Errorf("%w: model returned html instead of json", ErrInvalidResponse)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(hd.body, &envelope); err != nil {
		return Response{}, fmt.Errorf("%w: %s", ErrInvalidResponse, c.redact(string(hd.body)))
	}

	if c.style == StyleResponses {
		return parseResponses(envelope)
	}
	return parseChatCompletions(envelope)
}

func parseChatCompletions(envelope map[string]json.RawMessage) (Response, error) {
	raw, ok := envelope["choices"]
	if !ok {
		return Response{}, fmt.Errorf("%w: missing choices", ErrInvalidResponse)
	}
	var choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &choices); err != nil {
		return Response{}, fmt.Errorf("%w: choices: %v", ErrInvalidResponse, err)
	}
	if len(choices) == 0 {
		return Response{}, fmt.Errorf("%w: empty choices", ErrInvalidResponse)
	}
	text := choices[0].Message.Content
	if text == "" {
		text = choices[0].Text
	}
	resp := Response{Text: text}
	if raw, ok := envelope["model"]; ok {
		_ = json.Unmarshal(raw, &resp.Model)
	}
	if raw, ok := envelope["usage"]; ok {
		_ = json.Unmarshal(raw, &resp.Usage)
	}
	return resp, nil
}

func parseResponses(envelope map[string]json.RawMessage) (Response, error) {
	resp := Response{}
	if raw, ok := envelope["output_text"]; ok {
		if err := json.Unmarshal(raw, &resp.Text); err != nil {
			return Response{}, fmt.Errorf("%w: output_text: %v", ErrInvalidResponse, err)
		}
	} else {
		raw, ok := envelope["output"]
		if !ok {
			return Response{}, fmt.Errorf("%w: missing output", ErrInvalidResponse)
		}
		var output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &output); err != nil {
			return Response{}, fmt.Errorf("%w: output: %v", ErrInvalidResponse, err)
		}
		for _, item := range output {
			if item.Type != "message" {
				continue
			}
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					resp.Text += part.Text
				}
			}
		}
	}
	if raw, ok := envelope["model"]; ok {
		_ = json.Unmarshal(raw, &resp.Model)
	}
	if raw, ok := envelope["usage"]; ok {
		var usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}
		if err := json.Unmarshal(raw, &usage); err == nil {
			resp.Usage = Usage{
				PromptTokens:     usage.InputTokens,
				CompletionTokens: usage.OutputTokens,
				TotalTokens:      usage.InputTokens + usage.OutputTokens,
			}
		}
	}
	return resp, nil
}

func looksLikeHTML(body []byte) bool {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	trimmed := strings.TrimSpace(strings.ToLower(string(head)))
	return strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html") ||
		strings.HasPrefix(trimmed, "<head")
}

var (
	bearerTokenRe = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`)
	bareJWTRe     = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}`)
	keyValueRe    = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|authorization)[^a-z0-9]*[:=][^a-z0-9]*([^"',\s;]+)`)
)

// sanitizeErrorText caps an error body and strips likely credential material
// (the configured API key, Bearer tokens, JWT segments and secret key=value
// pairs) before it reaches callers or logs.
func sanitizeErrorText(body string) string {
	body = bearerTokenRe.ReplaceAllString(body, "Bearer [REDACTED]")
	body = bareJWTRe.ReplaceAllString(body, "[REDACTED]")
	body = keyValueRe.ReplaceAllString(body, "${1}:[REDACTED]")
	if len(body) > maxErrorBodyBytes {
		body = body[:maxErrorBodyBytes]
	}
	return body
}
