package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Defaults for the LLM client.
const (
	DefaultTimeout  = 10 * time.Second // the task requires a 10-second limit
	DefaultCacheTTL = 5 * time.Minute  // within the 5-10 minutes the task suggests
	maxLLMBodyBytes = 1 << 20          // 1 MB is far more than any completion we ask for
)

// Errors returned by the client. The handlers map them to status codes:
// ErrUnavailable -> 503, everything else -> 500.
var (
	// ErrUnavailable means the LLM could not be reached or is overloaded:
	// connection refused, timeout, HTTP 429 or 5xx. Trying again later may work.
	ErrUnavailable = errors.New("LLM service unavailable")
	// ErrAPI means the LLM rejected the request, e.g. a wrong model name
	// or API key (HTTP 4xx). Trying again will not help.
	ErrAPI = errors.New("LLM API error")
	// ErrInvalidResponse means the LLM answered, but not in a usable form.
	ErrInvalidResponse = errors.New("invalid LLM response")
)

// Config configures the LLM backend. An empty BaseURL means mock mode.
type Config struct {
	BaseURL  string        // LLM_BASE_URL, e.g. http://localhost:11434 for Ollama
	Model    string        // LLM_MODEL, e.g. llama3.2
	APIKey   string        // LLM_API_KEY, optional (not needed for Ollama)
	Timeout  time.Duration // 0 means DefaultTimeout
	CacheTTL time.Duration // 0 means DefaultCacheTTL
}

// ConfigFromEnv reads the LLM configuration from environment variables.
func ConfigFromEnv() Config {
	return Config{
		BaseURL: strings.TrimSpace(os.Getenv("LLM_BASE_URL")),
		Model:   strings.TrimSpace(os.Getenv("LLM_MODEL")),
		APIKey:  strings.TrimSpace(os.Getenv("LLM_API_KEY")),
	}
}

// MockMode reports whether no LLM backend is configured.
func (c Config) MockMode() bool {
	return c.BaseURL == ""
}

// Client talks to a real LLM through an OpenAI-compatible chat
// completions API (Ollama, OpenAI, Groq, ...). For mock mode use Mock.
type Client struct {
	endpoint string // full URL of /v1/chat/completions
	model    string
	apiKey   string
	http     *http.Client
	cache    *cache
}

// NewClient validates cfg and returns a live Client. Both the base URL
// and the model are required; without a backend, use NewMock instead.
func NewClient(cfg Config) (*Client, error) {
	if cfg.MockMode() {
		return nil, errors.New("LLM_BASE_URL is not set (use mock mode instead)")
	}
	if cfg.Model == "" {
		return nil, errors.New("LLM_MODEL must be set when LLM_BASE_URL is set")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = DefaultCacheTTL
	}

	// Accept both "http://host:11434" and "http://host:11434/v1".
	base := strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1")
	return &Client{
		endpoint: base + "/v1/chat/completions",
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		http:     &http.Client{Timeout: cfg.Timeout},
		cache:    newCache(cfg.CacheTTL),
	}, nil
}

// Mode reports "live", for logs and the X-LLM-Mode response header.
func (c *Client) Mode() string {
	return "live"
}

// Model returns the configured model name.
func (c *Client) Model() string {
	return c.model
}

// chatRequest and chatResponse are the parts of the OpenAI-compatible
// chat completions format that we use.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// complete sends prompt to the LLM and returns the text of its answer.
// The request is cancelled after the client timeout, or earlier when ctx
// is cancelled (for example when the browser closes the connection).
func (c *Client) complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		Temperature: 0.8,
	})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout, cancelled request...
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMBodyBytes))
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", ErrUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return "", fmt.Errorf("%w: HTTP %d: %s", ErrUnavailable, resp.StatusCode, snippet(data))
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("%w: HTTP %d: %s", ErrAPI, resp.StatusCode, snippet(data))
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("%w: not JSON: %v", ErrInvalidResponse, err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("%w: empty answer", ErrInvalidResponse)
	}
	return parsed.Choices[0].Message.Content, nil
}

// snippet shortens a response body for error messages and logs.
func snippet(data []byte) string {
	const max = 200
	s := strings.TrimSpace(string(data))
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}
