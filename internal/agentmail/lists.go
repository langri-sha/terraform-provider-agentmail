package agentmail

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Directions and list types of AgentMail's allow and block lists.
const (
	DirectionSend    = "send"
	DirectionReceive = "receive"
	DirectionReply   = "reply"

	ListTypeAllow = "allow"
	ListTypeBlock = "block"
)

var (
	Directions = []string{DirectionSend, DirectionReceive, DirectionReply}
	ListTypes  = []string{ListTypeAllow, ListTypeBlock}
)

// ListScope selects whose lists a call addresses: an inbox's, a pod's, or
// the organization's when both are empty.
type ListScope struct {
	PodID   string
	InboxID string
}

func (s ListScope) path(direction, listType string, rest ...string) string {
	var segments []string
	switch {
	case s.InboxID != "":
		segments = []string{"v0", "inboxes", s.InboxID, "lists"}
	case s.PodID != "":
		segments = []string{"v0", "pods", s.PodID, "lists"}
	default:
		segments = []string{"v0", "lists"}
	}
	return route(append(append(segments, direction, listType), rest...)...)
}

// ListEntry is ListEntry, or PodListEntry on the pod and inbox routes.
type ListEntry struct {
	Entry          string    `json:"entry"`
	OrganizationID string    `json:"organization_id"`
	Reason         *string   `json:"reason,omitempty"`
	Direction      string    `json:"direction"`
	ListType       string    `json:"list_type"`
	EntryType      string    `json:"entry_type"`
	CreatedAt      time.Time `json:"created_at"`
	ReadOnly       *bool     `json:"read_only,omitempty"`
	PodID          string    `json:"pod_id,omitempty"`
	InboxID        *string   `json:"inbox_id,omitempty"`
}

// IsReadOnly reports whether the API refuses to delete the entry.
func (e *ListEntry) IsReadOnly() bool { return e.ReadOnly != nil && *e.ReadOnly }

// CreateListEntryRequest is CreateListEntryRequest.
type CreateListEntryRequest struct {
	Entry  string  `json:"entry"`
	Reason *string `json:"reason,omitempty"`
}

type listEntriesPage struct {
	NextPageToken *string     `json:"next_page_token"`
	Entries       []ListEntry `json:"entries"`
}

// ListEntries returns every entry of one list, ordered by entry.
func (c *Client) ListEntries(ctx context.Context, scope ListScope, direction, listType string) ([]ListEntry, error) {
	var entries []ListEntry
	token := ""
	for {
		q := url.Values{}
		if token != "" {
			q.Set("page_token", token)
		}
		var p listEntriesPage
		if err := c.do(ctx, http.MethodGet, scope.path(direction, listType), q, nil, &p); err != nil {
			return nil, err
		}
		entries = append(entries, p.Entries...)
		if p.NextPageToken == nil || *p.NextPageToken == "" {
			return entries, nil
		}
		token = *p.NextPageToken
	}
}

// GetListEntry reads one entry.
func (c *Client) GetListEntry(ctx context.Context, scope ListScope, direction, listType, entry string) (*ListEntry, error) {
	var e ListEntry
	if err := c.do(ctx, http.MethodGet, scope.path(direction, listType, entry), nil, nil, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// CreateListEntry adds an entry. An allow list blocks every address it does
// not match from its first entry on.
func (c *Client) CreateListEntry(ctx context.Context, scope ListScope, direction, listType string, req CreateListEntryRequest) (*ListEntry, error) {
	var e ListEntry
	if err := c.do(ctx, http.MethodPost, scope.path(direction, listType), nil, req, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// DeleteListEntry removes an entry. Read-only entries can't be removed.
func (c *Client) DeleteListEntry(ctx context.Context, scope ListScope, direction, listType, entry string) error {
	return c.do(ctx, http.MethodDelete, scope.path(direction, listType, entry), nil, nil, nil)
}
