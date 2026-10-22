package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

var withEcho = map[string]func() (tfprotov6.ProviderServer, error){
	"agentmail": providerserver.NewProtocol6WithError(New("test", "none")()),
	"echo":      echoprovider.NewProviderServer(),
}

var ephemeralResourcesSupported = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)}

func providerBlockWithKey(s *fakeagentmail.Server, apiKey string) string {
	return fmt.Sprintf("provider \"agentmail\" {\n  base_url = %q\n  api_key  = %q\n}\n", s.URL(), apiKey)
}

func noMintedSecretInState(s *fakeagentmail.Server) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		minted := s.MintedSecrets()
		if len(minted) == 0 {
			return fmt.Errorf("the fake minted no keys")
		}
		for _, secret := range minted {
			if strings.Contains(string(raw), secret) {
				return fmt.Errorf("state holds the minted secret %s", secret)
			}
		}
		return nil
	}
}

func onlyTheRootKeyIsLive(s *fakeagentmail.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if live := s.LiveKeyIDs(); !slices.Equal(live, []string{"key_root"}) {
			return fmt.Errorf("live keys are %v, want only the root key", live)
		}
		return nil
	}
}

func TestAccAPIKey_HandsTheSecretToAConsumer(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: withEcho,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
ephemeral "agentmail_api_key" "ci" {
  name        = "ci"
  permissions = ["inbox_read", "list_entry_read"]
  ttl         = "1h"
}

provider "echo" {
  data = ephemeral.agentmail_api_key.ci
}

resource "echo" "key" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("echo.key", "data.api_key", func(v string) error {
						if !slices.Contains(s.MintedSecrets(), v) {
							return fmt.Errorf("%q is not a key the fake minted", v)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("echo.key", "data.name", "ci"),
					resource.TestCheckResourceAttr("echo.key", "data.permissions.#", "2"),
					resource.TestMatchResourceAttr("echo.key", "data.api_key_id", regexp.MustCompile(`^key_`)),
					resource.TestCheckResourceAttrWith("echo.key", "data.expires_at", func(v string) error {
						at, err := time.Parse(time.RFC3339, v)
						if err != nil {
							return err
						}
						if d := time.Until(at); d < 50*time.Minute || d > time.Hour {
							return fmt.Errorf("the key expires in %s, want about an hour", d)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccAPIKey_ConfiguresAnotherProviderAndIsRevoked(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
ephemeral "agentmail_api_key" "inboxes" {
  permissions = ["inbox_read", "inbox_create", "inbox_update", "inbox_delete"]
  ttl         = "1h"
}

provider "agentmail" {
  alias    = "inboxes"
  base_url = "` + s.URL() + `"
  api_key  = ephemeral.agentmail_api_key.inboxes.api_key
}

resource "agentmail_inbox" "support" {
  provider = agentmail.inboxes
  username = "support"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					fakeInboxContains(s, "support@agentmail.to", `"inbox_id":"support@agentmail.to"`),
					noMintedSecretInState(s),
					onlyTheRootKeyIsLive(s),
				),
			},
		},
	})
}

func TestAccAPIKey_KeepsAKeyWhenAskedTo(t *testing.T) {
	s := fakeagentmail.New(t)
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "scoped"})
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: withEcho,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
ephemeral "agentmail_api_key" "kept" {
  inbox_id        = "` + inbox + `"
  permissions     = ["message_send"]
  expires_at      = "` + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) + `"
  revoke_on_close = false
}

provider "echo" {
  data = ephemeral.agentmail_api_key.kept
}

resource "echo" "key" {}
`,
				Check: resource.TestCheckResourceAttrWith("echo.key", "data.api_key_id", func(id string) error {
					got := s.APIKeyJSON(id)
					if !strings.Contains(got, `"inbox_id":"`+inbox+`"`) {
						return fmt.Errorf("key %s is %q, want it live and scoped to %s", id, got, inbox)
					}
					return nil
				}),
			},
		},
	})
}

func TestAccAPIKey_RefusesToKeepAKeyThatNeverExpires(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: withEcho,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
ephemeral "agentmail_api_key" "kept" {
  revoke_on_close = false
}

provider "echo" {
  data = ephemeral.agentmail_api_key.kept
}

resource "echo" "key" {}
`,
				ExpectError: regexp.MustCompile(`A kept key needs an expiry`),
			},
		},
	})
}

func TestAccAPIKey_ExplainsWhyAgentMailRefusedIt(t *testing.T) {
	s := fakeagentmail.New(t)
	parentExpiry := time.Now().Add(time.Hour)
	minter := s.SeedKey(t, fakeagentmail.KeySeed{Permissions: []string{"api_key_create", "inbox_read"}, ExpiresAt: &parentExpiry})
	reader := s.SeedKey(t, fakeagentmail.KeySeed{Permissions: []string{"inbox_read"}})
	config := func(apiKey, body string) string {
		return providerBlockWithKey(s, apiKey) + `
ephemeral "agentmail_api_key" "ci" {
` + body + `
}

provider "echo" {
  data = ephemeral.agentmail_api_key.ci
}

resource "echo" "key" {}
`
	}

	cases := map[string]struct {
		config string
		want   string
	}{
		"a key that outlives its parent": {
			config: config(minter, `  permissions = ["inbox_read"]
  ttl         = "48h"`),
			want: `can't outlive the key that mints it`,
		},
		"a parent that can't create keys": {
			config: config(reader, `  permissions = ["inbox_read"]`),
			want:   `can't create keys`,
		},
		"a permission the parent lacks": {
			config: config(minter, `  permissions = ["inbox_delete"]`),
			want:   `can't grant permissions`,
		},
		"a permission AgentMail doesn't define": {
			config: config(s.RootKey(), `  permissions = ["inbox_teleport"]`),
			want:   `value must be one of`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   ephemeralResourcesSupported,
				ProtoV6ProviderFactories: withEcho,
				Steps:                    []resource.TestStep{{Config: c.config, ExpectError: regexp.MustCompile(c.want)}},
			})
		})
	}
}

func keptKeyConfig(s *fakeagentmail.Server) string {
	return providerBlock(s) + `
ephemeral "agentmail_api_key" "kept" {
  permissions     = ["inbox_read"]
  ttl             = "24h"
  revoke_on_close = false
}

provider "echo" {
  data = ephemeral.agentmail_api_key.kept
}

resource "echo" "key" {}
`
}

func TestAccAPIKey_RevokesAKeyWhoseResponseItCannotRead(t *testing.T) {
	s := fakeagentmail.New(t)
	s.AnswerKeyCreationsWith(func(id string) string {
		return `{"api_key_id":"` + id + `","api_key":"am_x","prefix":"am_x","name":"","created_at":"yesterday"}`
	})
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: withEcho,
		Steps: []resource.TestStep{{
			Config:      keptKeyConfig(s),
			ExpectError: regexp.MustCompile(`Could not read the minted API key`),
		}},
	})
	if len(s.MintedSecrets()) == 0 {
		t.Fatal("the fake minted no keys")
	}
	if live := s.LiveKeyIDs(); !slices.Equal(live, []string{"key_root"}) {
		t.Errorf("live keys are %v, want only the root key", live)
	}
}

func TestAccAPIKey_SaysAKeyMayBeLiveWhenItCannotTellWhich(t *testing.T) {
	s := fakeagentmail.New(t)
	s.AnswerKeyCreationsWith(func(string) string { return `{"api_key_id":` })
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   ephemeralResourcesSupported,
		ProtoV6ProviderFactories: withEcho,
		Steps: []resource.TestStep{{
			Config:      keptKeyConfig(s),
			ExpectError: regexp.MustCompile(`The minted API key may still be live`),
		}},
	})
}

func TestAccAPIKey_RevokesAKeyThatDiffersFromTheRequest(t *testing.T) {
	later := time.Now().Add(48 * time.Hour)
	cases := map[string]struct {
		alter func(*fakeagentmail.KeyGrant)
		want  string
	}{
		"an extra permission": {
			alter: func(g *fakeagentmail.KeyGrant) { g.Permissions["inbox_delete"] = true },
			want:  `grants inbox_delete, inbox_read`,
		},
		"a missing permission": {
			alter: func(g *fakeagentmail.KeyGrant) { g.Permissions = map[string]bool{} },
			want:  `grants no permissions`,
		},
		"no restriction": {
			alter: func(g *fakeagentmail.KeyGrant) { g.Permissions = nil },
			want:  `is unrestricted`,
		},
		"no expiry": {
			alter: func(g *fakeagentmail.KeyGrant) { g.ExpiresAt = nil },
			want:  `never expires`,
		},
		"another expiry": {
			alter: func(g *fakeagentmail.KeyGrant) { g.ExpiresAt = &later },
			want:  `expires at`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := fakeagentmail.New(t)
			s.AlterKeyGrants(c.alter)
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   ephemeralResourcesSupported,
				ProtoV6ProviderFactories: withEcho,
				Steps: []resource.TestStep{{
					Config:      keptKeyConfig(s),
					ExpectError: regexp.MustCompile(`(?s)AgentMail minted a different key than requested.*` + c.want),
				}},
			})
			if len(s.MintedSecrets()) == 0 {
				t.Fatal("the fake minted no keys")
			}
			if live := s.LiveKeyIDs(); !slices.Equal(live, []string{"key_root"}) {
				t.Errorf("live keys are %v, want only the root key", live)
			}
		})
	}
}
