package agentmail

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

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "am_test", UserAgent: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRejectsAnEmptyAPIKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted an empty API key")
	}
}

func TestNewRejectsABaseURLThatIsNotAbsoluteHTTP(t *testing.T) {
	for _, base := range []string{"api.agentmail.to", "ftp://api.agentmail.to", "https://"} {
		if _, err := New(Config{APIKey: "k", BaseURL: base}); err == nil {
			t.Errorf("New accepted base URL %q", base)
		}
	}
}

func TestNewDefaultsToTheUSAPI(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("base URL is %q, want %q", c.baseURL, DefaultBaseURL)
	}
}

func TestRequestsCarryTheBearerKeyAndUserAgent(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer am_test" {
			t.Errorf("Authorization is %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "test-agent" {
			t.Errorf("User-Agent is %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.do(context.Background(), http.MethodDelete, route("v0", "inboxes", "a@agentmail.to"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRouteKeepsAnAddressAsOneSegment(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	})
	err := c.do(context.Background(), http.MethodDelete, route("v0", "lists", "receive", "allow", "a/b@example.com"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/v0/lists/receive/allow/a%2Fb@example.com"; gotPath != want {
		t.Errorf("path is %q, want %q", gotPath, want)
	}
}

func TestErrorResponsesDecodeIntoAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"name":"NotFoundError","code":"not_found","message":"Inbox not found","fix":"Check the inbox ID"}`))
	})
	err := c.do(context.Background(), http.MethodGet, route("v0", "inboxes", "missing@agentmail.to"), nil, nil, &struct{}{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != "not_found" {
		t.Errorf("decoded %+v", apiErr)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound is false for a 404")
	}
	if want := "agentmail: HTTP 404 not_found: Inbox not found (fix: Check the inbox ID)"; err.Error() != want {
		t.Errorf("message is %q, want %q", err.Error(), want)
	}
}

func TestANonJSONErrorKeepsItsBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	})
	err := c.do(context.Background(), http.MethodGet, route("v0", "inboxes"), nil, nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502: upstream unavailable") {
		t.Errorf("error is %v", err)
	}
}

func TestRateLimitedRequestsAreRetriedAfterRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.do(context.Background(), http.MethodDelete, route("v0", "inboxes", "a@agentmail.to"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d calls, want 3", got)
	}
}

func TestRateLimitingGivesUpAfterTheLastAttempt(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"name":"RateLimitError","code":"rate_limit_exceeded","message":"slow down"}`))
	})
	err := c.do(context.Background(), http.MethodGet, route("v0", "inboxes"), nil, nil, &struct{}{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "rate_limit_exceeded" {
		t.Fatalf("error is %v", err)
	}
	if got := calls.Load(); got != maxAttempts {
		t.Errorf("server saw %d calls, want %d", got, maxAttempts)
	}
}

func TestRateLimitWaitsStopWhenTheContextEnds(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.do(ctx, http.MethodGet, route("v0", "inboxes"), nil, nil, &struct{}{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error is %v, want a deadline", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/elsewhere", http.StatusFound)
	})
	err := c.do(context.Background(), http.MethodGet, route("v0", "inboxes"), nil, nil, &struct{}{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusFound {
		t.Errorf("error is %v, want the 302 itself", err)
	}
}

func TestAKeyWhoseResponseCannotBeDecodedKeepsItsID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key_id":"key_1","api_key":"am_1","prefix":"am_1","name":"ci","created_at":"yesterday"}`))
	})
	_, err := c.CreateAPIKey(context.Background(), APIKeyScope{}, CreateAPIKeyRequest{})
	var undecoded *UndecodedKeyError
	if !errors.As(err, &undecoded) || undecoded.APIKeyID != "key_1" {
		t.Fatalf("got %v, want an UndecodedKeyError for key_1", err)
	}
}

func TestAKeyWhoseResponseIsNotJSONIsStillReportedAsCreated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api_key_id":"key_1","api_k`))
	})
	_, err := c.CreateAPIKey(context.Background(), APIKeyScope{}, CreateAPIKeyRequest{})
	var undecoded *UndecodedKeyError
	if !errors.As(err, &undecoded) || undecoded.APIKeyID != "" {
		t.Fatalf("got %v, want an UndecodedKeyError without an ID", err)
	}
}

func TestARefusedKeyIsNotReportedAsCreated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"name":"ForbiddenError","message":"no"}`))
	})
	_, err := c.CreateAPIKey(context.Background(), APIKeyScope{}, CreateAPIKeyRequest{})
	var undecoded *UndecodedKeyError
	if errors.As(err, &undecoded) {
		t.Fatalf("got %v, want an APIError", err)
	}
}
