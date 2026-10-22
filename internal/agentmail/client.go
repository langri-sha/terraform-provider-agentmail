// Package agentmail is a client for the parts of AgentMail's REST API that
// the provider manages. It is written by hand against the OpenAPI document
// vendored in internal/fakeagentmail/testdata, whose contract tests check
// every request and response it exchanges with the fake server.
package agentmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is AgentMail's US API. The EU API is https://api.agentmail.eu.
const DefaultBaseURL = "https://api.agentmail.to"

const (
	maxResponseSize = 10 << 20
	maxAttempts     = 4
)

// Config configures a Client. APIKey is required.
type Config struct {
	// BaseURL defaults to DefaultBaseURL.
	BaseURL   string
	APIKey    string
	UserAgent string

	// HTTPClient is optional; the default times out after 30 seconds and
	// does not follow redirects.
	HTTPClient *http.Client
}

// Client talks to the AgentMail API with a bearer key.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
}

// New returns a Client, or an error when the configuration is unusable.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("agentmail: API key is empty")
	}
	raw := cfg.BaseURL
	if raw == "" {
		raw = DefaultBaseURL
	}
	base, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("agentmail: base URL: %w", err)
	}
	if base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return nil, fmt.Errorf("agentmail: base URL %q must be an absolute http(s) URL", raw)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{baseURL: base.String(), apiKey: cfg.APIKey, userAgent: cfg.UserAgent, http: httpClient}, nil
}

// APIError is an error response from AgentMail. Code is the stable
// snake_case identifier AgentMail asks callers to branch on.
type APIError struct {
	StatusCode int    `json:"-"`
	Name       string `json:"name"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Fix        string `json:"fix"`
	Errors     any    `json:"errors"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "agentmail: HTTP %d", e.StatusCode)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	} else if e.Name != "" {
		fmt.Fprintf(&b, " %s", e.Name)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Errors != nil {
		if raw, err := json.Marshal(e.Errors); err == nil {
			fmt.Fprintf(&b, " %s", raw)
		}
	}
	if e.Fix != "" {
		fmt.Fprintf(&b, " (fix: %s)", e.Fix)
	}
	return b.String()
}

// IsNotFound reports whether err is AgentMail answering 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// do sends a request and decodes a JSON response into out, which may be nil.
// path must already be escaped; build it with route. It retries a request
// answered with 429, waiting as long as Retry-After asks.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("agentmail: encode %s %s: %w", method, path, err)
		}
	}
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("agentmail: %s %s: %w", method, path, err)
	}
	u.RawQuery = query.Encode()

	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("agentmail: %s %s: %w", method, path, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			wait := retryAfter(resp.Header.Get("Retry-After"), attempt)
			_ = resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		return decode(resp, method, path, out)
	}
}

func decode(resp *http.Response, method, path string, out any) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("agentmail: %s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if json.Unmarshal(raw, apiErr) != nil || apiErr.Name == "" && apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("agentmail: %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func retryAfter(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

// route joins path segments into an escaped path, so that an email address
// or a domain stays one segment.
func route(segments ...string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}
