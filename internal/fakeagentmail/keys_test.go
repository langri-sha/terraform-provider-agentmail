package fakeagentmail_test

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

func grant(names ...string) *map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return &m
}

func clientWithKey(t *testing.T, s *fakeagentmail.Server, secret string) *agentmail.Client {
	t.Helper()
	c, err := agentmail.New(agentmail.Config{BaseURL: s.URL(), APIKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func exerciseAPIKeys(t *testing.T, s *fakeagentmail.Server) {
	ctx := context.Background()
	c := client(t, s)
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "keys"})
	pod := s.AddPod()
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	scopes := map[string]agentmail.APIKeyScope{"org": {}, "pod": {PodID: pod}, "inbox": {InboxID: inbox}}
	for name, scope := range scopes {
		created, err := c.CreateAPIKey(ctx, scope, agentmail.CreateAPIKeyRequest{
			Name:        ptr("ci " + name),
			Permissions: grant("inbox_read", "list_entry_read"),
			ExpiresAt:   &expiry,
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if created.APIKey == "" || !created.ExpiresAt.Equal(expiry) || len(created.Permissions) != 2 {
			t.Errorf("%s: created %+v", name, created)
		}
		read, err := c.GetAPIKey(ctx, created.APIKeyID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if read.Type != "bearer" || read.Name != "ci "+name {
			t.Errorf("%s: read %+v", name, read)
		}
		if err := c.DeleteAPIKey(ctx, scope, created.APIKeyID); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = c.GetAPIKey(ctx, created.APIKeyID)
		wantStatus(t, err, http.StatusNotFound)
		err = c.DeleteAPIKey(ctx, scope, created.APIKeyID)
		wantStatus(t, err, http.StatusNotFound)
	}

	unrestricted, err := c.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if unrestricted.Permissions != nil || unrestricted.ExpiresAt != nil {
		t.Errorf("an unrestricted key from the root key is %+v", unrestricted)
	}

	past := time.Now().Add(-time.Hour)
	_, err = c.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{ExpiresAt: &past})
	wantStatus(t, err, http.StatusBadRequest)
	_, err = c.CreateAPIKey(ctx, agentmail.APIKeyScope{PodID: "pod_missing"}, agentmail.CreateAPIKeyRequest{})
	wantStatus(t, err, http.StatusNotFound)

	parentExpiry := time.Now().Add(time.Hour)
	parent := clientWithKey(t, s, s.SeedKey(t, fakeagentmail.KeySeed{
		Permissions: []string{"api_key_create", "inbox_read"},
		ExpiresAt:   &parentExpiry,
	}))
	tooLate := parentExpiry.Add(time.Minute)
	_, err = parent.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_read"), ExpiresAt: &tooLate})
	wantStatus(t, err, http.StatusForbidden)
	_, err = parent.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_delete")})
	wantStatus(t, err, http.StatusForbidden)
	inherited, err := parent.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_read")})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ExpiresAt == nil || !inherited.ExpiresAt.Equal(parentExpiry) {
		t.Errorf("a key without expires_at expires at %v, want its parent's %v", inherited.ExpiresAt, parentExpiry)
	}

	reader := clientWithKey(t, s, s.SeedKey(t, fakeagentmail.KeySeed{Permissions: []string{"inbox_read"}}))
	_, err = reader.CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_read")})
	wantStatus(t, err, http.StatusForbidden)
}

func TestAPIKeysBehaveAsDocumented(t *testing.T) {
	exerciseAPIKeys(t, fakeagentmail.New(t))
}

func TestAnUnknownPermissionIsRejected(t *testing.T) {
	s := fakeagentmail.New(t)
	_, err := client(t, s).CreateAPIKey(context.Background(), agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_teleport")})
	wantStatus(t, err, http.StatusBadRequest)
}

func TestAMintedKeyAuthenticatesUntilItIsRevoked(t *testing.T) {
	s := fakeagentmail.New(t)
	ctx := context.Background()
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "minted"})
	created, err := client(t, s).CreateAPIKey(ctx, agentmail.APIKeyScope{}, agentmail.CreateAPIKeyRequest{Permissions: grant("inbox_read")})
	if err != nil {
		t.Fatal(err)
	}
	minted := clientWithKey(t, s, created.APIKey)
	if _, err := minted.GetInbox(ctx, inbox); err != nil {
		t.Fatal(err)
	}
	if got := s.MintedSecrets(); !slices.Equal(got, []string{created.APIKey}) {
		t.Errorf("minted secrets are %v", got)
	}
	if err := client(t, s).DeleteAPIKey(ctx, agentmail.APIKeyScope{}, created.APIKeyID); err != nil {
		t.Fatal(err)
	}
	_, err = minted.GetInbox(ctx, inbox)
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestTheClientKnowsEveryPermissionTheDocumentDefines(t *testing.T) {
	spec := loadOpenAPI(t)
	var documented []string
	for name := range asMap(spec.schema("ApiKeyPermissions")["properties"]) {
		documented = append(documented, name)
	}
	sort.Strings(documented)
	known := slices.Sorted(slices.Values(agentmail.Permissions))
	if !slices.Equal(known, documented) {
		t.Errorf("agentmail.Permissions is %v, the document defines %v", known, documented)
	}
}
