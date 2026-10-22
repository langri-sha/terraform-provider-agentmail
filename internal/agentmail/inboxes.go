package agentmail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Inbox is AgentMail's inboxesInbox. Its inbox_id is the email address.
type Inbox struct {
	InboxID     string    `json:"inbox_id"`
	PodID       string    `json:"pod_id"`
	Email       string    `json:"email"`
	DisplayName *string   `json:"display_name,omitempty"`
	ClientID    *string   `json:"client_id,omitempty"`
	Status      *string   `json:"status,omitempty"`
	Metadata    Metadata  `json:"metadata,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// InboxStatusActive is what an inbox without a status is: AgentMail omits
// status when the inbox is active.
const (
	InboxStatusActive = "active"
	InboxStatusPaused = "paused"
)

// EffectiveStatus is the inbox's status with the omitted active filled in.
func (i *Inbox) EffectiveStatus() string {
	if i.Status == nil || *i.Status != InboxStatusPaused {
		return InboxStatusActive
	}
	return InboxStatusPaused
}

// Metadata is an inbox's custom metadata. Values are strings, bools, or
// numbers decoded as json.Number so that their text survives a round trip.
type Metadata map[string]any

func (m *Metadata) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	*m = v
	return nil
}

// CreateInboxRequest is inboxesCreateInboxRequest.
type CreateInboxRequest struct {
	Username    *string  `json:"username,omitempty"`
	Domain      *string  `json:"domain,omitempty"`
	DisplayName *string  `json:"display_name,omitempty"`
	ClientID    *string  `json:"client_id,omitempty"`
	Status      *string  `json:"status,omitempty"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// UpdateInboxRequest is inboxesUpdateInboxRequest. AgentMail rejects an
// update that sets none of its fields.
type UpdateInboxRequest struct {
	DisplayName *string        `json:"display_name,omitempty"`
	Status      *string        `json:"status,omitempty"`
	Metadata    *MetadataPatch `json:"metadata,omitempty"`
}

// MetadataPatch is a merge-patch of an inbox's metadata. AgentMail adds or
// overwrites the keys in Set, removes the keys in Remove, and keeps the rest.
// Clear removes every key; AgentMail rejects an empty object, so Clear is
// sent as null.
type MetadataPatch struct {
	Clear  bool
	Set    map[string]any
	Remove []string
}

func (p MetadataPatch) MarshalJSON() ([]byte, error) {
	if p.Clear {
		return []byte("null"), nil
	}
	body := make(map[string]any, len(p.Set)+len(p.Remove))
	for k, v := range p.Set {
		body[k] = v
	}
	for _, k := range p.Remove {
		body[k] = nil
	}
	return json.Marshal(body)
}

// CreateInbox creates an inbox in the default pod, or in podID when set.
func (c *Client) CreateInbox(ctx context.Context, podID string, req CreateInboxRequest) (*Inbox, error) {
	path := route("v0", "inboxes")
	if podID != "" {
		path = route("v0", "pods", podID, "inboxes")
	}
	var inbox Inbox
	if err := c.do(ctx, http.MethodPost, path, nil, req, &inbox); err != nil {
		return nil, err
	}
	return &inbox, nil
}

// GetInbox reads an inbox by its ID, the email address.
func (c *Client) GetInbox(ctx context.Context, inboxID string) (*Inbox, error) {
	var inbox Inbox
	if err := c.do(ctx, http.MethodGet, route("v0", "inboxes", inboxID), nil, nil, &inbox); err != nil {
		return nil, err
	}
	return &inbox, nil
}

// UpdateInbox patches an inbox's display name, status or metadata.
func (c *Client) UpdateInbox(ctx context.Context, inboxID string, req UpdateInboxRequest) (*Inbox, error) {
	var inbox Inbox
	if err := c.do(ctx, http.MethodPatch, route("v0", "inboxes", inboxID), nil, req, &inbox); err != nil {
		return nil, err
	}
	return &inbox, nil
}

// DeleteInbox deletes an inbox with its messages, threads and drafts.
func (c *Client) DeleteInbox(ctx context.Context, inboxID string) error {
	return c.do(ctx, http.MethodDelete, route("v0", "inboxes", inboxID), nil, nil, nil)
}
