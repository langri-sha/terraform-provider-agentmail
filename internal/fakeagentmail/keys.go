package fakeagentmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

type apiKey struct {
	ID        string
	Secret    string
	Prefix    string
	Name      string
	PodID     string
	InboxID   string
	CreatedAt time.Time
	UpdatedAt time.Time
	UsedAt    *time.Time
	ExpiresAt *time.Time
	// Permissions is nil for an unrestricted key.
	Permissions map[string]bool
}

func (k *apiKey) can(permission string) bool {
	return k.Permissions == nil || k.Permissions[permission]
}

func (k *apiKey) wire(withSecret bool) map[string]any {
	out := map[string]any{
		"api_key_id": k.ID,
		"prefix":     k.Prefix,
		"name":       k.Name,
		"created_at": timestamp(k.CreatedAt),
	}
	if withSecret {
		out["api_key"] = k.Secret
	} else {
		out["type"] = "bearer"
		out["updated_at"] = timestamp(k.UpdatedAt)
		if k.UsedAt != nil {
			out["used_at"] = timestamp(*k.UsedAt)
		}
	}
	if k.PodID != "" {
		out["pod_id"] = k.PodID
	}
	if k.InboxID != "" {
		out["inbox_id"] = k.InboxID
	}
	if k.Permissions != nil {
		out["permissions"] = k.Permissions
	}
	if k.ExpiresAt != nil {
		out["expires_at"] = timestamp(*k.ExpiresAt)
	}
	return out
}

type keyContext struct{}

func withKey(ctx context.Context, k *apiKey) context.Context {
	return context.WithValue(ctx, keyContext{}, k)
}

func callerKey(ctx context.Context) *apiKey {
	k, _ := ctx.Value(keyContext{}).(*apiKey)
	return k
}

// KeySeed describes an org-scoped key made outside Terraform.
type KeySeed struct {
	// Permissions restricts the key to these; nil leaves it unrestricted.
	Permissions []string
	ExpiresAt   *time.Time
}

// SeedKey adds a key directly to the fake's state and returns its secret.
func (s *Server) SeedKey(t testing.TB, seed KeySeed) string {
	t.Helper()
	k := &apiKey{ID: "key_" + randomHex(8), Name: "seeded", ExpiresAt: seed.ExpiresAt}
	k.Secret = "am_" + randomHex(24)
	k.Prefix = k.Secret[:10]
	if seed.Permissions != nil {
		k.Permissions = map[string]bool{}
		for _, p := range seed.Permissions {
			if !slices.Contains(agentmail.Permissions, p) {
				t.Fatalf("fakeagentmail: unknown permission %s", p)
			}
			k.Permissions[p] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k.CreatedAt, k.UpdatedAt = s.now(), s.now()
	s.keys[k.Secret] = k
	return k.Secret
}

// MintedSecrets returns the secret of every key created through the API,
// revoked ones included, oldest first.
func (s *Server) MintedSecrets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.minted...)
}

// LiveKeyIDs returns the IDs of every key that exists, sorted.
func (s *Server) LiveKeyIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, k := range s.keys {
		out = append(out, k.ID)
	}
	sort.Strings(out)
	return out
}

func (s *Server) keyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v0/api-keys", s.createKey)
	mux.HandleFunc("POST /v0/pods/{pod_id}/api-keys", s.createKey)
	mux.HandleFunc("POST /v0/inboxes/{inbox_id}/api-keys", s.createKey)
	mux.HandleFunc("GET /v0/api-keys/{api_key_id}", s.getKey)
	mux.HandleFunc("DELETE /v0/api-keys/{api_key_id}", s.deleteKey)
	mux.HandleFunc("DELETE /v0/pods/{pod_id}/api-keys/{api_key_id}", s.deleteKey)
	mux.HandleFunc("DELETE /v0/inboxes/{inbox_id}/api-keys/{api_key_id}", s.deleteKey)
}

type createKeyBody struct {
	Name        *string                    `json:"name"`
	Permissions map[string]json.RawMessage `json:"permissions"`
	ExpiresAt   *time.Time                 `json:"expires_at"`
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	caller := callerKey(r.Context())
	if !caller.can("api_key_create") {
		writeError(w, http.StatusForbidden, "ForbiddenError", "missing_permission", "The API key lacks the api_key_create permission.")
		return
	}
	var body createKeyBody
	if !decodeBody(w, r, &body) {
		return
	}
	var permissions map[string]bool
	if body.Permissions != nil {
		permissions = map[string]bool{}
		for name, raw := range body.Permissions {
			if !slices.Contains(agentmail.Permissions, name) {
				writeValidationError(w, "permissions."+name, fmt.Sprintf("unknown permission %q", name))
				return
			}
			var granted *bool
			if err := json.Unmarshal(raw, &granted); err != nil {
				writeValidationError(w, "permissions."+name, "a permission is true, false or null")
				return
			}
			if granted != nil && *granted {
				permissions[name] = true
			}
		}
	}
	if caller.Permissions != nil {
		if permissions == nil {
			writeError(w, http.StatusForbidden, "ForbiddenError", "permission_escalation", "A restricted key cannot create an unrestricted one.")
			return
		}
		for name := range permissions {
			if !caller.Permissions[name] {
				writeError(w, http.StatusForbidden, "ForbiddenError", "permission_escalation", fmt.Sprintf("The creating key lacks %s.", name))
				return
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	expiresAt := body.ExpiresAt
	switch {
	case expiresAt != nil && !expiresAt.After(now):
		writeValidationError(w, "expires_at", "expires_at must be in the future")
		return
	case expiresAt != nil && caller.ExpiresAt != nil && expiresAt.After(*caller.ExpiresAt):
		writeError(w, http.StatusForbidden, "ForbiddenError", "forbidden", "A key cannot create one that outlives it.")
		return
	case expiresAt == nil:
		expiresAt = caller.ExpiresAt
	}
	k := &apiKey{
		ID:          "key_" + randomHex(8),
		Secret:      "am_" + randomHex(24),
		Permissions: permissions,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   expiresAt,
	}
	k.Prefix = k.Secret[:10]
	if body.Name != nil {
		k.Name = *body.Name
	}
	switch {
	case r.PathValue("inbox_id") != "":
		in, ok := s.inboxes[r.PathValue("inbox_id")]
		if !ok {
			writeNotFound(w, "Inbox")
			return
		}
		k.InboxID, k.PodID = in.ID, in.PodID
	case r.PathValue("pod_id") != "":
		if !s.pods[r.PathValue("pod_id")] {
			writeNotFound(w, "Pod")
			return
		}
		k.PodID = r.PathValue("pod_id")
	}
	s.keys[k.Secret] = k
	s.minted = append(s.minted, k.Secret)
	if s.keyAnswer != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.keyAnswer(k.ID)))
		return
	}
	writeJSON(w, http.StatusOK, k.wire(true))
}

// AnswerKeyCreationsWith makes the fake create keys as usual but answer with
// body instead of the key, as a server whose response is cut short or
// changes shape would.
func (s *Server) AnswerKeyCreationsWith(body func(apiKeyID string) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyAnswer = body
}

func (s *Server) findKey(id string) *apiKey {
	for _, k := range s.keys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (s *Server) getKey(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findKey(r.PathValue("api_key_id"))
	if k == nil {
		writeNotFound(w, "API key")
		return
	}
	writeJSON(w, http.StatusOK, k.wire(false))
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findKey(r.PathValue("api_key_id"))
	inScope := k != nil &&
		(r.PathValue("pod_id") == "" || r.PathValue("pod_id") == k.PodID && k.InboxID == "") &&
		(r.PathValue("inbox_id") == "" || r.PathValue("inbox_id") == k.InboxID)
	if !inScope {
		writeNotFound(w, "API key")
		return
	}
	delete(s.keys, k.Secret)
	w.WriteHeader(http.StatusNoContent)
}

// APIKeyJSON returns the key as AgentMail serves it, without its secret, or
// "" when it does not exist.
func (s *Server) APIKeyJSON(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findKey(id)
	if k == nil {
		return ""
	}
	raw, err := json.Marshal(k.wire(false))
	if err != nil {
		panic(err)
	}
	return string(raw)
}
