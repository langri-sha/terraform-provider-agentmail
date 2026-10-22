package provider

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

const inboxAddress = "agentmail_inbox.support"

func TestAccInbox_CreatesUpdatesAndImports(t *testing.T) {
	s := fakeagentmail.New(t)
	const id = "support@agentmail.to"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support"
  status       = "paused"
  metadata     = { team = "ops", tier = 2, pager = true }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inboxAddress, "inbox_id", id),
					resource.TestCheckResourceAttr(inboxAddress, "email", id),
					resource.TestCheckResourceAttr(inboxAddress, "domain", "agentmail.to"),
					resource.TestCheckResourceAttr(inboxAddress, "pod_id", s.DefaultPod()),
					resource.TestCheckResourceAttr(inboxAddress, "status", "paused"),
					resource.TestCheckNoResourceAttr(inboxAddress, "client_id"),
					fakeInboxContains(s, id, `"display_name":"Support"`, `"status":"paused"`, `"metadata":{"pager":true,"team":"ops","tier":2}`),
				),
			},
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support desk"
  status       = "active"
  metadata     = { team = "support", tier = 2 }
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inboxAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inboxAddress, "status", "active"),
					fakeInboxContains(s, id, `"display_name":"Support desk"`, `"metadata":{"team":"support","tier":2}`),
					fakeInboxLacks(s, id, `"status"`),
					lastRequestIs(s, http.MethodPatch, "/v0/inboxes/{inbox_id}",
						`{"display_name":"Support desk","metadata":{"pager":null,"team":"support"},"status":"active"}`),
				),
			},
			{
				ResourceName:                         inboxAddress,
				ImportState:                          true,
				ImportStateId:                        id,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "inbox_id",
			},
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support desk"
  metadata     = {}
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					fakeInboxLacks(s, id, `"metadata"`),
					lastRequestIs(s, http.MethodPatch, "/v0/inboxes/{inbox_id}", `{"metadata":null}`),
				),
			},
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support desk"
  metadata     = {}
}
`,
				PlanOnly: true,
			},
		},
	})
}

func TestAccInbox_ImportingAnInboxMadeElsewherePlansClean(t *testing.T) {
	s := fakeagentmail.New(t)
	pod := s.AddPod()
	id := s.SeedInbox(t, fakeagentmail.InboxSeed{
		Username:    "seeded",
		PodID:       pod,
		DisplayName: "Seeded",
		ClientID:    "seeded-inbox",
		Paused:      true,
		Metadata:    `{"n": 1, "b": true, "s": "x"}`,
	})
	minimal := providerBlock(s) + `resource "agentmail_inbox" "support" {}`
	withMetadata := providerBlock(s) + `
resource "agentmail_inbox" "support" {
  metadata = { n = 1, b = true, s = "x" }
}
`
	changingOneKey := providerBlock(s) + `
resource "agentmail_inbox" "support" {
  metadata = { n = 1, b = true, s = "y" }
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             minimal,
				ResourceName:       inboxAddress,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
			},
			{
				Config: minimal,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inboxAddress, "pod_id", pod),
					resource.TestCheckResourceAttr(inboxAddress, "client_id", "seeded-inbox"),
					resource.TestCheckResourceAttr(inboxAddress, "username", "seeded"),
					resource.TestCheckResourceAttr(inboxAddress, "status", "paused"),
				),
			},
			{
				Config: withMetadata,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: changingOneKey,
				Check: resource.ComposeAggregateTestCheckFunc(
					fakeInboxContains(s, id, `"metadata":{"b":true,"n":1,"s":"y"}`),
					lastRequestIs(s, http.MethodPatch, "/v0/inboxes/{inbox_id}", `{"metadata":{"s":"y"}}`),
				),
			},
		},
	})
}

func TestAccInbox_ChangingACreateOnlyAttributeReplacesIt(t *testing.T) {
	s := fakeagentmail.New(t)
	pod := s.AddPod()
	config := func(username, podID string) string {
		return providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username = "` + username + `"
  pod_id   = "` + podID + `"
}
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("first", s.DefaultPod())},
			{
				Config: config("second", s.DefaultPod()),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inboxAddress, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inboxAddress, "inbox_id", "second@agentmail.to"),
					fakeInboxLacks(s, "first@agentmail.to", "first"),
				),
			},
			{
				Config: config("second", pod),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inboxAddress, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(inboxAddress, "pod_id", pod),
			},
		},
	})
}

func TestAccInbox_CorrectsChangesMadeElsewhere(t *testing.T) {
	s := fakeagentmail.New(t)
	const id = "support@agentmail.to"
	config := providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support"
  status       = "active"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					s.UpdateInboxOutOfBand(t, id, func(name *string, paused *bool, _ map[string]any) {
						*name = "Renamed"
						*paused = true
					})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inboxAddress, plancheck.ResourceActionUpdate)},
				},
				Check: fakeInboxContains(s, id, `"display_name":"Support"`),
			},
			{
				PreConfig: func() {
					c, err := agentmail.New(agentmail.Config{BaseURL: s.URL(), APIKey: s.RootKey()})
					if err != nil {
						t.Fatal(err)
					}
					if err := c.DeleteInbox(context.Background(), id); err != nil {
						t.Fatal(err)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inboxAddress, plancheck.ResourceActionCreate)},
				},
				Check: fakeInboxContains(s, id, `"display_name":"Support"`),
			},
		},
	})
}

func TestAccInbox_RejectsMetadataAgentMailWouldRefuse(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  metadata = { nested = { a = 1 } }
}
`,
				ExpectError: regexp.MustCompile(`must be a string, number or boolean`),
			},
		},
	})
}
