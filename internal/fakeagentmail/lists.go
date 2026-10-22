package fakeagentmail

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

type listEntry struct {
	Entry     string
	Reason    *string
	ReadOnly  bool
	CreatedAt time.Time
}

type listKey struct {
	scope     string
	direction string
	listType  string
}

func orgScope() string            { return "org" }
func podScope(id string) string   { return "pod:" + id }
func inboxScope(id string) string { return "inbox:" + id }
func isDirection(d string) bool   { return d == "send" || d == "receive" || d == "reply" }
func isListType(t string) bool    { return t == "allow" || t == "block" }
func entryType(entry string) string {
	if strings.Contains(entry, "@") {
		return "email"
	}
	return "domain"
}

// ListEntrySeed describes a list entry made outside Terraform. Leave PodID
// and InboxID empty for the organization's lists.
type ListEntrySeed struct {
	PodID     string
	InboxID   string
	Direction string
	ListType  string
	Entry     string
	Reason    string
	ReadOnly  bool
}

// SeedListEntry adds an entry directly to the fake's state.
func (s *Server) SeedListEntry(t testing.TB, seed ListEntrySeed) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, ok := s.seedScope(seed.PodID, seed.InboxID)
	if !ok || !isDirection(seed.Direction) || !isListType(seed.ListType) {
		t.Fatalf("fakeagentmail: invalid list entry seed %+v", seed)
	}
	e := &listEntry{Entry: seed.Entry, ReadOnly: seed.ReadOnly, CreatedAt: s.now()}
	if seed.Reason != "" {
		e.Reason = &seed.Reason
	}
	s.list(listKey{scope, seed.Direction, seed.ListType})[seed.Entry] = e
}

// ListEntries returns the entries of one list, sorted. Leave podID and
// inboxID empty for the organization's lists.
func (s *Server) ListEntries(podID, inboxID, direction, listType string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, ok := s.seedScope(podID, inboxID)
	if !ok {
		return nil
	}
	var out []string
	for entry := range s.lists[listKey{scope, direction, listType}] {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

func (s *Server) seedScope(podID, inboxID string) (string, bool) {
	switch {
	case inboxID != "":
		_, ok := s.inboxes[inboxID]
		return inboxScope(inboxID), ok
	case podID != "":
		return podScope(podID), s.pods[podID]
	}
	return orgScope(), true
}

func (s *Server) list(k listKey) map[string]*listEntry {
	if s.lists == nil {
		s.lists = map[listKey]map[string]*listEntry{}
	}
	if s.lists[k] == nil {
		s.lists[k] = map[string]*listEntry{}
	}
	return s.lists[k]
}

func (s *Server) listRoutes(mux *http.ServeMux) {
	for _, prefix := range []string{"/v0", "/v0/pods/{pod_id}", "/v0/inboxes/{inbox_id}"} {
		mux.HandleFunc("GET "+prefix+"/lists/{direction}/{type}", s.listEntries)
		mux.HandleFunc("POST "+prefix+"/lists/{direction}/{type}", s.createListEntry)
		mux.HandleFunc("GET "+prefix+"/lists/{direction}/{type}/{entry}", s.getListEntry)
		mux.HandleFunc("DELETE "+prefix+"/lists/{direction}/{type}/{entry}", s.deleteListEntry)
	}
}

// listScope resolves the list a request addresses. The caller holds s.mu.
func (s *Server) listScope(w http.ResponseWriter, r *http.Request) (listKey, bool) {
	direction, listType := r.PathValue("direction"), r.PathValue("type")
	if !isDirection(direction) || !isListType(listType) {
		writeValidationError(w, "path", fmt.Sprintf("no %s %s list", direction, listType))
		return listKey{}, false
	}
	switch {
	case r.PathValue("inbox_id") != "":
		id := r.PathValue("inbox_id")
		if _, ok := s.inboxes[id]; !ok {
			writeNotFound(w, "Inbox")
			return listKey{}, false
		}
		return listKey{inboxScope(id), direction, listType}, true
	case r.PathValue("pod_id") != "":
		id := r.PathValue("pod_id")
		if !s.pods[id] {
			writeNotFound(w, "Pod")
			return listKey{}, false
		}
		return listKey{podScope(id), direction, listType}, true
	}
	return listKey{orgScope(), direction, listType}, true
}

func (s *Server) wireListEntry(k listKey, e *listEntry) map[string]any {
	out := map[string]any{
		"entry":           e.Entry,
		"organization_id": s.org,
		"direction":       k.direction,
		"list_type":       k.listType,
		"entry_type":      entryType(e.Entry),
		"created_at":      timestamp(e.CreatedAt),
	}
	if e.Reason != nil {
		out["reason"] = *e.Reason
	}
	if e.ReadOnly {
		out["read_only"] = true
	}
	switch scope, id, _ := strings.Cut(k.scope, ":"); scope {
	case "pod":
		out["pod_id"] = id
	case "inbox":
		out["pod_id"] = s.inboxes[id].PodID
		out["inbox_id"] = id
	}
	return out
}

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	k, ok := s.listScope(w, r)
	if !ok {
		s.mu.Unlock()
		return
	}
	entries := s.list(k)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	wired := make([]map[string]any, len(names))
	for i, name := range names {
		wired[i] = s.wireListEntry(k, entries[name])
	}
	s.mu.Unlock()

	items, next, ok := page(s, r, wired)
	if !ok {
		writeValidationError(w, "page_token", "invalid page token")
		return
	}
	body := map[string]any{"count": len(items), "entries": items}
	if next != nil {
		body["next_page_token"] = *next
	}
	writeJSON(w, http.StatusOK, body)
}

type createListEntryBody struct {
	Entry  *string `json:"entry"`
	Reason *string `json:"reason"`
}

// createListEntry answers a repeated entry with the stored one; whether
// AgentMail does the same is not documented.
func (s *Server) createListEntry(w http.ResponseWriter, r *http.Request) {
	var body createListEntryBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Entry == nil || *body.Entry == "" || strings.ContainsAny(*body.Entry, " /") {
		writeValidationError(w, "entry", "entry must be an email address or a domain")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.listScope(w, r)
	if !ok {
		return
	}
	entries := s.list(k)
	e, exists := entries[*body.Entry]
	if !exists {
		e = &listEntry{Entry: *body.Entry, Reason: body.Reason, CreatedAt: s.now()}
		entries[e.Entry] = e
	}
	writeJSON(w, http.StatusOK, s.wireListEntry(k, e))
}

func (s *Server) getListEntry(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.listScope(w, r)
	if !ok {
		return
	}
	e, ok := s.list(k)[r.PathValue("entry")]
	if !ok {
		writeNotFound(w, "List entry")
		return
	}
	writeJSON(w, http.StatusOK, s.wireListEntry(k, e))
}

func (s *Server) deleteListEntry(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.listScope(w, r)
	if !ok {
		return
	}
	entries := s.list(k)
	e, ok := entries[r.PathValue("entry")]
	switch {
	case !ok:
		writeNotFound(w, "List entry")
	case e.ReadOnly:
		writeError(w, http.StatusForbidden, "ForbiddenError", "forbidden", "The entry is read-only and cannot be deleted via the API.")
	default:
		delete(entries, e.Entry)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) dropInboxLists(id string) {
	for k := range s.lists {
		if k.scope == inboxScope(id) {
			delete(s.lists, k)
		}
	}
}
