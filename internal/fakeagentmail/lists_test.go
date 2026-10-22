package fakeagentmail_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

func entryNames(entries []agentmail.ListEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Entry
	}
	return names
}

func exerciseLists(t *testing.T, s *fakeagentmail.Server) {
	ctx := context.Background()
	c := client(t, s)
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "lists"})
	pod := s.AddPod()

	scopes := map[string]agentmail.ListScope{
		"org":   {},
		"pod":   {PodID: pod},
		"inbox": {InboxID: inbox},
	}
	for name, scope := range scopes {
		e, err := c.CreateListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, agentmail.CreateListEntryRequest{
			Entry: "partner@example.com", Reason: ptr("a partner"),
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if e.EntryType != "email" || *e.Reason != "a partner" {
			t.Errorf("%s: created %+v", name, e)
		}
		if _, err := c.CreateListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, agentmail.CreateListEntryRequest{Entry: "example.org"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := c.GetListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, "example.org")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.EntryType != "domain" {
			t.Errorf("%s: read %+v", name, got)
		}
		entries, err := c.ListEntries(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := []string{"example.org", "partner@example.com"}; !reflect.DeepEqual(entryNames(entries), want) {
			t.Errorf("%s: entries are %v, want %v", name, entryNames(entries), want)
		}
		if err := c.DeleteListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, "example.org"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = c.GetListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, "example.org")
		wantStatus(t, err, http.StatusNotFound)
		err = c.DeleteListEntry(ctx, scope, agentmail.DirectionReceive, agentmail.ListTypeAllow, "example.org")
		wantStatus(t, err, http.StatusNotFound)
	}

	inboxEntry, err := c.GetListEntry(ctx, scopes["inbox"], agentmail.DirectionReceive, agentmail.ListTypeAllow, "partner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if inboxEntry.InboxID == nil || *inboxEntry.InboxID != inbox || inboxEntry.PodID != s.DefaultPod() {
		t.Errorf("an inbox entry reads as %+v", inboxEntry)
	}

	_, err = c.CreateListEntry(ctx, agentmail.ListScope{}, agentmail.DirectionReply, agentmail.ListTypeBlock, agentmail.CreateListEntryRequest{Entry: ""})
	wantStatus(t, err, http.StatusBadRequest)
	_, err = c.ListEntries(ctx, agentmail.ListScope{PodID: "pod_missing"}, agentmail.DirectionSend, agentmail.ListTypeBlock)
	wantStatus(t, err, http.StatusNotFound)

	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{Direction: "receive", ListType: "block", Entry: "spam.example", ReadOnly: true})
	err = c.DeleteListEntry(ctx, agentmail.ListScope{}, agentmail.DirectionReceive, agentmail.ListTypeBlock, "spam.example")
	wantStatus(t, err, http.StatusForbidden)
}

func TestListsBehaveAsDocumented(t *testing.T) {
	exerciseLists(t, fakeagentmail.New(t))
}

func TestListingPagesUntilTheTokenRunsOut(t *testing.T) {
	s := fakeagentmail.New(t)
	s.SetPageSize(2)
	s.ServeEmptyPages()
	want := []string{"a.example", "b.example", "c.example", "d.example", "e.example"}
	for _, e := range want {
		s.SeedListEntry(t, fakeagentmail.ListEntrySeed{Direction: "send", ListType: "block", Entry: e})
	}
	entries, err := client(t, s).ListEntries(context.Background(), agentmail.ListScope{}, agentmail.DirectionSend, agentmail.ListTypeBlock)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entryNames(entries), want) {
		t.Errorf("entries are %v, want %v", entryNames(entries), want)
	}
	var emptyPages int
	for _, e := range s.Exchanges() {
		if e.Route == "/v0/lists/{direction}/{type}" && string(e.Response) == "{\"count\":0,\"entries\":[],\"next_page_token\":\"empty-at-0\"}\n" {
			emptyPages++
		}
	}
	if emptyPages != 1 {
		t.Errorf("the client saw %d empty first pages, want 1", emptyPages)
	}
}

func TestReadOnlyEntriesCannotBeDeleted(t *testing.T) {
	s := fakeagentmail.New(t)
	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{Direction: "receive", ListType: "block", Entry: "spam.example", ReadOnly: true})
	c := client(t, s)
	entries, err := c.ListEntries(context.Background(), agentmail.ListScope{}, agentmail.DirectionReceive, agentmail.ListTypeBlock)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsReadOnly() {
		t.Fatalf("entries are %+v", entries)
	}
	err = c.DeleteListEntry(context.Background(), agentmail.ListScope{}, agentmail.DirectionReceive, agentmail.ListTypeBlock, "spam.example")
	wantStatus(t, err, http.StatusForbidden)
}

func TestDeletingAnInboxDropsItsLists(t *testing.T) {
	s := fakeagentmail.New(t)
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "gone"})
	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{InboxID: inbox, Direction: "receive", ListType: "allow", Entry: "a.example"})
	if err := client(t, s).DeleteInbox(context.Background(), inbox); err != nil {
		t.Fatal(err)
	}
	s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "gone"})
	if got := s.ListEntries("", inbox, "receive", "allow"); len(got) != 0 {
		t.Errorf("a recreated inbox inherited %v", got)
	}
}
