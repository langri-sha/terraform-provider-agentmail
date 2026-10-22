package fakeagentmail_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

func ptr[T any](v T) *T { return &v }

func client(t *testing.T, s *fakeagentmail.Server) *agentmail.Client {
	t.Helper()
	c, err := agentmail.New(agentmail.Config{BaseURL: s.URL(), APIKey: s.RootKey()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func raw(t *testing.T, s *fakeagentmail.Server, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.RootKey())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(out)
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	apiErr, ok := err.(*agentmail.APIError)
	if !ok || apiErr.StatusCode != status {
		t.Fatalf("error is %v, want HTTP %d", err, status)
	}
}

func exerciseInboxes(t *testing.T, s *fakeagentmail.Server) {
	ctx := context.Background()
	c := client(t, s)

	created, err := c.CreateInbox(ctx, "", agentmail.CreateInboxRequest{
		Username:    ptr("support"),
		Domain:      ptr("agentmail.to"),
		DisplayName: ptr("Support"),
		ClientID:    ptr("support-inbox"),
		Status:      ptr("paused"),
		Metadata:    agentmail.Metadata{"team": "ops", "tier": json.Number("2"), "pager": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.InboxID != "support@agentmail.to" || created.EffectiveStatus() != agentmail.InboxStatusPaused {
		t.Errorf("created %+v", created)
	}

	replayed, err := c.CreateInbox(ctx, "", agentmail.CreateInboxRequest{ClientID: ptr("support-inbox"), Username: ptr("other")})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.InboxID != created.InboxID {
		t.Errorf("a replayed client_id created %s", replayed.InboxID)
	}

	pod := s.AddPod()
	inPod, err := c.CreateInbox(ctx, pod, agentmail.CreateInboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if inPod.PodID != pod || !strings.HasSuffix(inPod.InboxID, "@agentmail.to") {
		t.Errorf("created in pod %+v", inPod)
	}

	_, err = c.CreateInbox(ctx, "", agentmail.CreateInboxRequest{Username: ptr("x"), Domain: ptr("unverified.example")})
	wantStatus(t, err, http.StatusUnprocessableEntity)
	_, err = c.CreateInbox(ctx, "pod_missing", agentmail.CreateInboxRequest{})
	wantStatus(t, err, http.StatusNotFound)

	got, err := c.GetInbox(ctx, created.InboxID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["tier"] != json.Number("2") || got.Metadata["pager"] != true {
		t.Errorf("metadata read back as %#v", got.Metadata)
	}
	_, err = c.GetInbox(ctx, "missing@agentmail.to")
	wantStatus(t, err, http.StatusNotFound)

	updated, err := c.UpdateInbox(ctx, created.InboxID, agentmail.UpdateInboxRequest{
		DisplayName: ptr("Support desk"),
		Status:      ptr("active"),
		Metadata:    &agentmail.MetadataPatch{Set: map[string]any{"team": "support"}, Remove: []string{"pager"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != nil || *updated.DisplayName != "Support desk" {
		t.Errorf("updated %+v", updated)
	}
	if len(updated.Metadata) != 2 || updated.Metadata["team"] != "support" || updated.Metadata["tier"] != json.Number("2") {
		t.Errorf("merge-patched metadata is %#v", updated.Metadata)
	}

	cleared, err := c.UpdateInbox(ctx, created.InboxID, agentmail.UpdateInboxRequest{Metadata: &agentmail.MetadataPatch{Clear: true}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Metadata != nil {
		t.Errorf("cleared metadata is %#v", cleared.Metadata)
	}

	if status, body := raw(t, s, http.MethodPatch, "/v0/inboxes/"+created.InboxID, `{}`); status != http.StatusBadRequest {
		t.Errorf("an empty update answered %d %s", status, body)
	}
	if status, body := raw(t, s, http.MethodPatch, "/v0/inboxes/"+created.InboxID, `{"metadata":{}}`); status != http.StatusBadRequest {
		t.Errorf("an empty metadata object answered %d %s", status, body)
	}
	_, err = c.UpdateInbox(ctx, "missing@agentmail.to", agentmail.UpdateInboxRequest{DisplayName: ptr("x")})
	wantStatus(t, err, http.StatusNotFound)

	if err := c.DeleteInbox(ctx, inPod.InboxID); err != nil {
		t.Fatal(err)
	}
	err = c.DeleteInbox(ctx, inPod.InboxID)
	wantStatus(t, err, http.StatusNotFound)

	unauthenticated, err := agentmail.New(agentmail.Config{BaseURL: s.URL(), APIKey: "am_unknown"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = unauthenticated.GetInbox(ctx, created.InboxID)
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestInboxesBehaveAsDocumented(t *testing.T) {
	exerciseInboxes(t, fakeagentmail.New(t))
}

func TestSeededInboxesKeepTheirMetadataTypes(t *testing.T) {
	s := fakeagentmail.New(t)
	id := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "seeded", Metadata: `{"n": 1, "b": true, "s": "x"}`})
	if got, want := s.InboxJSON(id), `"metadata":{"b":true,"n":1,"s":"x"}`; !strings.Contains(got, want) {
		t.Errorf("inbox is %s, want it to contain %s", got, want)
	}
}
