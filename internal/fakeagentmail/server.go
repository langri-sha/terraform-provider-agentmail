// Package fakeagentmail is an in-memory stand-in for the parts of AgentMail's
// REST API that the provider uses, shaped after the OpenAPI document in
// testdata. It exists for tests only and nothing that ships in the provider
// binary imports it.
//
// The contract tests check every exchange the fake serves against the
// document: the route and method exist, the status is declared, and both
// bodies match their schemas. Behaviour the document states in prose, such as
// metadata's merge-patch semantics, is implemented from that prose.
package fakeagentmail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Exchange is one request the fake answered.
type Exchange struct {
	Method string
	// Route is the OpenAPI path template the request matched.
	Route    string
	Status   int
	Request  []byte
	Response []byte
}

// Server is a running fake. Its zero value is not usable; call New.
type Server struct {
	srv *httptest.Server

	mu         sync.Mutex
	now        func() time.Time
	org        string
	defaultPod string
	pods       map[string]bool
	domains    map[string]bool
	keys       map[string]*apiKey
	minted     []string
	inboxes    map[string]*inbox
	lists      map[listKey]map[string]*listEntry
	exchanges  []Exchange
	pageSize   int
	emptyPages bool
	keyAnswer  func(apiKeyID string) string
	keyGrant   func(*KeyGrant)
}

// New starts a fake with a default pod, the agentmail.to domain and an
// unrestricted, non-expiring root key. It stops when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		now:        func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) },
		org:        "org_" + randomHex(8),
		defaultPod: "pod_" + randomHex(8),
		pods:       map[string]bool{},
		domains:    map[string]bool{"agentmail.to": true},
		keys:       map[string]*apiKey{},
		inboxes:    map[string]*inbox{},
		pageSize:   100,
	}
	s.pods[s.defaultPod] = true
	root := &apiKey{ID: "key_root", Secret: "am_root_" + randomHex(16), Prefix: "am_root", Name: "root", CreatedAt: s.now(), UpdatedAt: s.now()}
	s.keys[root.Secret] = root

	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = httptest.NewServer(s.record(s.authenticate(mux)))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the API base URL.
func (s *Server) URL() string { return s.srv.URL }

// RootKey is the secret of the unrestricted key every fake starts with.
func (s *Server) RootKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for secret, k := range s.keys {
		if k.ID == "key_root" {
			return secret
		}
	}
	panic("fakeagentmail: root key is gone")
}

// DefaultPod is the ID of the pod org-level creates land in.
func (s *Server) DefaultPod() string { return s.defaultPod }

// AddPod adds a pod and returns its ID.
func (s *Server) AddPod() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "pod_" + randomHex(8)
	s.pods[id] = true
	return id
}

// AddDomain registers a verified domain that inboxes may use.
func (s *Server) AddDomain(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains[domain] = true
}

// SetPageSize sets the default page size of every list.
func (s *Server) SetPageSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSize = n
}

// ServeEmptyPages makes every list answer an empty page carrying a
// next_page_token before each page with items, which AgentMail documents as
// possible: "a page can be empty and still carry a next_page_token".
func (s *Server) ServeEmptyPages() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyPages = true
}

// Exchanges returns every answered request, oldest first.
func (s *Server) Exchanges() []Exchange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Exchange(nil), s.exchanges...)
}

func (s *Server) routes(mux *http.ServeMux) {
	s.inboxRoutes(mux)
	s.listRoutes(mux)
	s.keyRoutes(mux)
}

func (s *Server) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.mu.Lock()
		s.exchanges = append(s.exchanges, Exchange{
			Method:   r.Method,
			Route:    rec.route,
			Status:   rec.status,
			Request:  reqBody,
			Response: rec.body.Bytes(),
		})
		s.mu.Unlock()
	})
}

type recorder struct {
	http.ResponseWriter
	route  string
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (s *Server) authenticate(next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := next.Handler(r)
		if rec, ok := w.(*recorder); ok {
			rec.route = strings.TrimPrefix(pattern, r.Method+" ")
		}
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "UnauthorizedError", "missing_authorization", "The Authorization header is missing.")
			return
		}
		s.mu.Lock()
		k := s.keys[secret]
		expired := k != nil && k.ExpiresAt != nil && !s.now().Before(*k.ExpiresAt)
		if k != nil && !expired {
			used := s.now()
			k.UsedAt = &used
		}
		s.mu.Unlock()
		switch {
		case k == nil:
			writeError(w, http.StatusUnauthorized, "UnauthorizedError", "unknown_api_key", "The API key is not recognized.")
			return
		case expired:
			writeError(w, http.StatusUnauthorized, "UnauthorizedError", "api_key_expired", "The API key has expired.")
			return
		}
		next.ServeHTTP(w, r.WithContext(withKey(r.Context(), k)))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, name, code, message string) {
	writeJSON(w, status, map[string]any{"name": name, "code": code, "message": message})
}

func writeValidationError(w http.ResponseWriter, path, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"name":    "ValidationError",
		"code":    "validation_error",
		"message": message,
		"errors":  []map[string]string{{"path": path, "message": message}},
	})
}

func writeNotFound(w http.ResponseWriter, what string) {
	writeError(w, http.StatusNotFound, "NotFoundError", "not_found", what+" not found")
}

// decodeBody decodes a JSON request body, rejecting fields the document does
// not define for it so that a client sending an invented field fails here.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		writeValidationError(w, "body", fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
}

// page slices items for one page of a list, honouring limit and page_token.
func page[T any](s *Server, r *http.Request, items []T) ([]T, *string, bool) {
	s.mu.Lock()
	size, empty := s.pageSize, s.emptyPages
	s.mu.Unlock()
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return nil, nil, false
		}
		size = n
	}
	token := r.URL.Query().Get("page_token")
	offset := 0
	if token != "" {
		rest, isEmpty := strings.CutPrefix(token, "empty-")
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "at-"))
		if err != nil || n < 0 || n > len(items) {
			return nil, nil, false
		}
		offset = n
		if isEmpty {
			empty = false
		}
	}
	if empty && offset < len(items) {
		next := fmt.Sprintf("empty-at-%d", offset)
		return []T{}, &next, true
	}
	end := min(offset+size, len(items))
	var next *string
	if end < len(items) {
		t := fmt.Sprintf("at-%d", end)
		next = &t
	}
	return items[offset:end], next, true
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func timestamp(t time.Time) string { return t.Format(time.RFC3339Nano) }
