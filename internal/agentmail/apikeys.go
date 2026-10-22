package agentmail

import (
	"context"
	"net/http"
	"time"
)

// Permissions are the keys of ApiKeyPermissions, in the document's order.
var Permissions = []string{
	"inbox_read",
	"inbox_create",
	"inbox_update",
	"inbox_delete",
	"message_read",
	"message_reply",
	"message_send",
	"message_update",
	"message_delete",
	"agent_armor_release",
	"label_spam_read",
	"label_blocked_read",
	"label_unauthenticated_read",
	"label_trash_read",
	"draft_read",
	"draft_create",
	"draft_update",
	"draft_delete",
	"draft_send",
	"webhook_read",
	"webhook_create",
	"webhook_update",
	"webhook_delete",
	"domain_read",
	"domain_create",
	"domain_update",
	"domain_delete",
	"list_entry_read",
	"list_entry_create",
	"list_entry_delete",
	"metrics_read",
	"api_key_read",
	"api_key_create",
	"api_key_update",
	"api_key_delete",
	"app_connect",
	"app_share_owner",
	"account_update",
	"pod_read",
	"pod_create",
	"pod_update",
	"pod_delete",
	"calendar_read",
	"calendar_update",
	"calendar_event_read",
	"calendar_event_create",
	"calendar_event_update",
	"calendar_event_delete",
	"file_read",
	"file_create",
	"file_update",
	"file_delete",
}

// APIKeyScope selects who a key belongs to: an inbox, a pod, or the
// organization when both are empty. A key's scope is fixed at creation.
type APIKeyScope struct {
	PodID   string
	InboxID string
}

func (s APIKeyScope) path(rest ...string) string {
	switch {
	case s.InboxID != "":
		return route(append([]string{"v0", "inboxes", s.InboxID, "api-keys"}, rest...)...)
	case s.PodID != "":
		return route(append([]string{"v0", "pods", s.PodID, "api-keys"}, rest...)...)
	}
	return route(append([]string{"v0", "api-keys"}, rest...)...)
}

// CreateAPIKeyRequest is CreateBearerApiKeyRequest. A nil Permissions grants
// every permission; otherwise only those set to true are granted. Without
// ExpiresAt the key inherits the expiry of the key that creates it, and an
// ExpiresAt later than that key's own is refused with 403.
type CreateAPIKeyRequest struct {
	Name        *string          `json:"name,omitempty"`
	Permissions *map[string]bool `json:"permissions,omitempty"`
	ExpiresAt   *time.Time       `json:"expires_at,omitempty"`
}

// CreatedAPIKey is CreateApiKeyResponse, the only response that carries the
// secret.
type CreatedAPIKey struct {
	APIKeyID    string          `json:"api_key_id"`
	APIKey      string          `json:"api_key"`
	Prefix      string          `json:"prefix"`
	Name        string          `json:"name"`
	PodID       *string         `json:"pod_id,omitempty"`
	InboxID     *string         `json:"inbox_id,omitempty"`
	Permissions map[string]bool `json:"permissions,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
}

// APIKey is BearerApiKey, which never carries the secret.
type APIKey struct {
	Type        string          `json:"type"`
	APIKeyID    string          `json:"api_key_id"`
	Prefix      string          `json:"prefix"`
	Name        string          `json:"name"`
	PodID       *string         `json:"pod_id,omitempty"`
	InboxID     *string         `json:"inbox_id,omitempty"`
	UsedAt      *time.Time      `json:"used_at,omitempty"`
	Permissions map[string]bool `json:"permissions,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
}

// CreateAPIKey mints a bearer key in scope.
func (c *Client) CreateAPIKey(ctx context.Context, scope APIKeyScope, req CreateAPIKeyRequest) (*CreatedAPIKey, error) {
	var key CreatedAPIKey
	if err := c.do(ctx, http.MethodPost, scope.path(), nil, req, &key); err != nil {
		return nil, err
	}
	return &key, nil
}

// GetAPIKey reads a key of any scope.
func (c *Client) GetAPIKey(ctx context.Context, apiKeyID string) (*APIKey, error) {
	var key APIKey
	if err := c.do(ctx, http.MethodGet, route("v0", "api-keys", apiKeyID), nil, nil, &key); err != nil {
		return nil, err
	}
	return &key, nil
}

// DeleteAPIKey revokes a key through the route of the scope it was created in.
func (c *Client) DeleteAPIKey(ctx context.Context, scope APIKeyScope, apiKeyID string) error {
	return c.do(ctx, http.MethodDelete, scope.path(apiKeyID), nil, nil, nil)
}
