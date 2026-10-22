package fakeagentmail

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type inbox struct {
	ID          string
	PodID       string
	DisplayName *string
	ClientID    *string
	Paused      bool
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (i *inbox) wire() map[string]any {
	out := map[string]any{
		"inbox_id":   i.ID,
		"pod_id":     i.PodID,
		"email":      i.ID,
		"created_at": timestamp(i.CreatedAt),
		"updated_at": timestamp(i.UpdatedAt),
	}
	if i.DisplayName != nil {
		out["display_name"] = *i.DisplayName
	}
	if i.ClientID != nil {
		out["client_id"] = *i.ClientID
	}
	if i.Paused {
		out["status"] = "paused"
	}
	if len(i.Metadata) > 0 {
		out["metadata"] = i.Metadata
	}
	return out
}

// InboxSeed describes an inbox created outside Terraform, as through the
// console or another client.
type InboxSeed struct {
	Username    string
	Domain      string
	PodID       string
	DisplayName string
	ClientID    string
	Paused      bool
	// Metadata is a JSON object.
	Metadata string
}

// SeedInbox creates an inbox directly in the fake's state and returns its ID.
func (s *Server) SeedInbox(t testing.TB, seed InboxSeed) string {
	t.Helper()
	domain := seed.Domain
	if domain == "" {
		domain = "agentmail.to"
	}
	pod := seed.PodID
	if pod == "" {
		pod = s.defaultPod
	}
	in := &inbox{ID: seed.Username + "@" + domain, PodID: pod, Paused: seed.Paused}
	if seed.DisplayName != "" {
		in.DisplayName = &seed.DisplayName
	}
	if seed.ClientID != "" {
		in.ClientID = &seed.ClientID
	}
	if seed.Metadata != "" {
		var md map[string]any
		dec := json.NewDecoder(strings.NewReader(seed.Metadata))
		dec.UseNumber()
		if err := dec.Decode(&md); err != nil {
			t.Fatalf("fakeagentmail: seed metadata: %v", err)
		}
		in.Metadata = md
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pods[pod] {
		t.Fatalf("fakeagentmail: no pod %s", pod)
	}
	if _, taken := s.inboxes[in.ID]; taken {
		t.Fatalf("fakeagentmail: inbox %s exists", in.ID)
	}
	in.CreatedAt, in.UpdatedAt = s.now(), s.now()
	s.inboxes[in.ID] = in
	return in.ID
}

// InboxJSON returns the inbox as AgentMail serves it, or "" when it does
// not exist.
func (s *Server) InboxJSON(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.inboxes[id]
	if !ok {
		return ""
	}
	raw, err := json.Marshal(in.wire())
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// UpdateInboxOutOfBand applies a change to an inbox the way another client
// would.
func (s *Server) UpdateInboxOutOfBand(t testing.TB, id string, change func(displayName *string, paused *bool, metadata map[string]any)) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.inboxes[id]
	if !ok {
		t.Fatalf("fakeagentmail: no inbox %s", id)
	}
	var name string
	if in.DisplayName != nil {
		name = *in.DisplayName
	}
	if in.Metadata == nil {
		in.Metadata = map[string]any{}
	}
	change(&name, &in.Paused, in.Metadata)
	if in.DisplayName != nil || name != "" {
		in.DisplayName = &name
	}
	in.UpdatedAt = s.now()
}

func (s *Server) inboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v0/inboxes", func(w http.ResponseWriter, r *http.Request) {
		s.createInbox(w, r, s.defaultPod)
	})
	mux.HandleFunc("POST /v0/pods/{pod_id}/inboxes", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.pods[r.PathValue("pod_id")]
		s.mu.Unlock()
		if !ok {
			writeNotFound(w, "Pod")
			return
		}
		s.createInbox(w, r, r.PathValue("pod_id"))
	})
	mux.HandleFunc("GET /v0/inboxes/{inbox_id}", s.getInbox)
	mux.HandleFunc("PATCH /v0/inboxes/{inbox_id}", s.updateInbox)
	mux.HandleFunc("DELETE /v0/inboxes/{inbox_id}", s.deleteInbox)
}

type createInboxBody struct {
	Username    *string         `json:"username"`
	Domain      *string         `json:"domain"`
	DisplayName *string         `json:"display_name"`
	ClientID    *string         `json:"client_id"`
	Status      *string         `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
}

func (s *Server) createInbox(w http.ResponseWriter, r *http.Request, pod string) {
	var body createInboxBody
	if !decodeBody(w, r, &body) {
		return
	}
	paused, ok := parseStatus(w, body.Status)
	if !ok {
		return
	}
	var metadata map[string]any
	if len(body.Metadata) > 0 && string(body.Metadata) != "null" {
		md, problem := parseMetadata(body.Metadata, false)
		if problem != "" {
			writeValidationError(w, "metadata", problem)
			return
		}
		metadata = md
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if body.ClientID != nil {
		for _, in := range s.inboxes {
			if in.ClientID != nil && *in.ClientID == *body.ClientID {
				writeJSON(w, http.StatusOK, in.wire())
				return
			}
		}
	}
	username := "agent-" + randomHex(4)
	if body.Username != nil {
		username = *body.Username
	}
	domain := "agentmail.to"
	if body.Domain != nil {
		domain = *body.Domain
	}
	if !s.domains[domain] {
		writeError(w, http.StatusUnprocessableEntity, "DomainNotVerifiedError", "domain_not_verified", fmt.Sprintf("Domain %s is not verified.", domain))
		return
	}
	id := username + "@" + domain
	if _, taken := s.inboxes[id]; taken {
		writeError(w, http.StatusUnprocessableEntity, "ResourceTakenError", "resource_taken", fmt.Sprintf("Inbox %s is taken.", id))
		return
	}
	in := &inbox{
		ID:          id,
		PodID:       pod,
		DisplayName: body.DisplayName,
		ClientID:    body.ClientID,
		Paused:      paused,
		Metadata:    metadata,
		CreatedAt:   s.now(),
		UpdatedAt:   s.now(),
	}
	s.inboxes[id] = in
	writeJSON(w, http.StatusOK, in.wire())
}

func (s *Server) getInbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.inboxes[r.PathValue("inbox_id")]
	if !ok {
		writeNotFound(w, "Inbox")
		return
	}
	writeJSON(w, http.StatusOK, in.wire())
}

type updateInboxBody struct {
	DisplayName *string         `json:"display_name"`
	Status      *string         `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
}

func (s *Server) updateInbox(w http.ResponseWriter, r *http.Request) {
	var body updateInboxBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.DisplayName == nil && body.Status == nil && body.Metadata == nil {
		writeValidationError(w, "body", "Each update must include at least one of display_name, status, or metadata.")
		return
	}
	paused, ok := parseStatus(w, body.Status)
	if !ok {
		return
	}
	var patch map[string]any
	clearAll := string(body.Metadata) == "null"
	if body.Metadata != nil && !clearAll {
		md, problem := parseMetadata(body.Metadata, true)
		if problem != "" {
			writeValidationError(w, "metadata", problem)
			return
		}
		if len(md) == 0 {
			writeValidationError(w, "metadata", "Sending an empty object is rejected; use null to clear.")
			return
		}
		patch = md
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	in, found := s.inboxes[r.PathValue("inbox_id")]
	if !found {
		writeNotFound(w, "Inbox")
		return
	}
	if body.DisplayName != nil {
		in.DisplayName = body.DisplayName
	}
	if body.Status != nil {
		in.Paused = paused
	}
	switch {
	case clearAll:
		in.Metadata = nil
	case patch != nil:
		if in.Metadata == nil {
			in.Metadata = map[string]any{}
		}
		for k, v := range patch {
			if v == nil {
				delete(in.Metadata, k)
			} else {
				in.Metadata[k] = v
			}
		}
	}
	in.UpdatedAt = s.now()
	writeJSON(w, http.StatusOK, in.wire())
}

func (s *Server) deleteInbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("inbox_id")
	if _, ok := s.inboxes[id]; !ok {
		writeNotFound(w, "Inbox")
		return
	}
	delete(s.inboxes, id)
	s.dropInboxLists(id)
	w.WriteHeader(http.StatusNoContent)
}

func parseStatus(w http.ResponseWriter, status *string) (paused, ok bool) {
	if status == nil {
		return false, true
	}
	switch *status {
	case "active":
		return false, true
	case "paused":
		return true, true
	}
	writeValidationError(w, "status", fmt.Sprintf("status must be active or paused, not %q", *status))
	return false, false
}

// parseMetadata enforces the documented limits: up to 256 keys, keys and
// string values of at most 256 characters, and string, number or boolean
// values. A patch may also carry null values.
func parseMetadata(raw json.RawMessage, patch bool) (map[string]any, string) {
	var md map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&md); err != nil {
		return nil, "metadata must be an object"
	}
	if len(md) > 256 {
		return nil, "metadata holds at most 256 keys"
	}
	for k, v := range md {
		if utf8.RuneCountInString(k) > 256 {
			return nil, fmt.Sprintf("metadata key %q is longer than 256 characters", k)
		}
		switch v := v.(type) {
		case string:
			if utf8.RuneCountInString(v) > 256 {
				return nil, fmt.Sprintf("metadata value of %q is longer than 256 characters", k)
			}
		case json.Number, bool:
		case nil:
			if !patch {
				return nil, fmt.Sprintf("metadata value of %q is null", k)
			}
		default:
			return nil, fmt.Sprintf("metadata value of %q must be a string, number or boolean", k)
		}
	}
	return md, ""
}
