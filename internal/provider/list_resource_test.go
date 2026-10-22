package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

const listAddress = "agentmail_list.support"

func fakeListIs(s *fakeagentmail.Server, podID, inboxID, direction, listType string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if want == nil {
			want = []string{}
		}
		got := s.ListEntries(podID, inboxID, direction, listType)
		if got == nil {
			got = []string{}
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("the %s %s list holds %v, want %v", direction, listType, got, want)
		}
		return nil
	}
}

func listCallsSince(s *fakeagentmail.Server, from int) []string {
	var calls []string
	for _, e := range s.Exchanges()[from:] {
		if e.Method == http.MethodPost || e.Method == http.MethodDelete {
			calls = append(calls, e.Method+" "+string(e.Request))
		}
	}
	return calls
}

func inboxList(s *fakeagentmail.Server, inbox, entries string) string {
	return providerBlock(s) + `
resource "agentmail_list" "support" {
  inbox_id  = "` + inbox + `"
  direction = "receive"
  list_type = "allow"
  entries   = ` + entries + `
}
`
}

func TestAccList_AddsBeforeItRemoves(t *testing.T) {
	s := fakeagentmail.New(t)
	inbox := s.SeedInbox(t, fakeagentmail.InboxSeed{Username: "support"})
	var mark int

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy:             fakeListIs(s, "", inbox, "receive", "allow"),
		Steps: []resource.TestStep{
			{
				Config: inboxList(s, inbox, `[{ entry = "example.com", reason = "partner" }, { entry = "a@example.org" }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(listAddress, "id", "inbox/"+inbox+"/receive/allow"),
					resource.TestCheckResourceAttr(listAddress, "read_only_entries.#", "0"),
					fakeListIs(s, "", inbox, "receive", "allow", "a@example.org", "example.com"),
				),
			},
			{
				PreConfig: func() { mark = len(s.Exchanges()) },
				Config:    inboxList(s, inbox, `[{ entry = "example.net" }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					fakeListIs(s, "", inbox, "receive", "allow", "example.net"),
					func(*terraform.State) error {
						calls := listCallsSince(s, mark)
						want := []string{`POST {"entry":"example.net"}`, "DELETE ", "DELETE "}
						if !slices.Equal(calls, want) {
							return fmt.Errorf("the apply made %q, want %q", calls, want)
						}
						return nil
					},
				),
			},
			{
				PreConfig: func() { mark = len(s.Exchanges()) },
				Config:    inboxList(s, inbox, `[{ entry = "example.net", reason = "now with a reason" }]`),
				Check: func(*terraform.State) error {
					calls := listCallsSince(s, mark)
					want := []string{"DELETE ", `POST {"entry":"example.net","reason":"now with a reason"}`}
					if !slices.Equal(calls, want) {
						return fmt.Errorf("the apply made %q, want %q", calls, want)
					}
					return nil
				},
			},
			{
				ResourceName:      listAddress,
				ImportState:       true,
				ImportStateId:     "inbox/" + inbox + "/receive/allow",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccList_RemovesEntriesAddedElsewhere(t *testing.T) {
	s := fakeagentmail.New(t)
	config := providerBlock(s) + `
resource "agentmail_list" "support" {
  direction = "send"
  list_type = "block"
  entries   = [{ entry = "competitor.example" }]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.TestCheckResourceAttr(listAddress, "id", "org/send/block")},
			{
				PreConfig: func() {
					s.SeedListEntry(t, fakeagentmail.ListEntrySeed{Direction: "send", ListType: "block", Entry: "added-in-the-console.example"})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(listAddress, plancheck.ResourceActionUpdate)},
				},
				Check: fakeListIs(s, "", "", "send", "block", "competitor.example"),
			},
		},
	})
}

func TestAccList_RefusesToCreateOverEntriesItDoesNotKnow(t *testing.T) {
	s := fakeagentmail.New(t)
	pod := s.AddPod()
	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{PodID: pod, Direction: "reply", ListType: "allow", Entry: "existing.example"})
	config := providerBlock(s) + `
resource "agentmail_list" "support" {
  pod_id    = "` + pod + `"
  direction = "reply"
  list_type = "allow"
  entries   = [{ entry = "new.example" }]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`holds existing.example, which the`),
			},
		},
	})
	if got := s.ListEntries(pod, "", "reply", "allow"); !slices.Equal(got, []string{"existing.example"}) {
		t.Errorf("the refused create left %v", got)
	}
}

func TestAccList_ImportsAListWithEntriesAndPlansClean(t *testing.T) {
	s := fakeagentmail.New(t)
	pod := s.AddPod()
	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{PodID: pod, Direction: "reply", ListType: "allow", Entry: "existing.example", Reason: "a customer"})
	config := providerBlock(s) + `
resource "agentmail_list" "support" {
  pod_id    = "` + pod + `"
  direction = "reply"
  list_type = "allow"
  entries   = [{ entry = "existing.example", reason = "a customer" }]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       listAddress,
				ImportState:        true,
				ImportStateId:      "pod/" + pod + "/reply/allow",
				ImportStatePersist: true,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccList_LeavesReadOnlyEntriesAlone(t *testing.T) {
	s := fakeagentmail.New(t)
	s.SeedListEntry(t, fakeagentmail.ListEntrySeed{Direction: "receive", ListType: "block", Entry: "abuse.example", ReadOnly: true})
	config := func(entries string) string {
		return providerBlock(s) + `
resource "agentmail_list" "support" {
  direction = "receive"
  list_type = "block"
  entries   = ` + entries + `
}
`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy:             fakeListIs(s, "", "", "receive", "block", "abuse.example"),
		Steps: []resource.TestStep{
			{
				Config: config(`[{ entry = "spam.example" }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(listAddress, "read_only_entries.#", "1"),
					resource.TestCheckTypeSetElemAttr(listAddress, "read_only_entries.*", "abuse.example"),
					fakeListIs(s, "", "", "receive", "block", "abuse.example", "spam.example"),
				),
			},
			{
				Config:      config(`[{ entry = "spam.example" }, { entry = "abuse.example" }]`),
				ExpectError: regexp.MustCompile(`AgentMail marks abuse.example read-only`),
			},
		},
	})
}

func TestAccList_RejectsAnEntryListedTwice(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
resource "agentmail_list" "support" {
  direction = "receive"
  list_type = "allow"
  entries   = [{ entry = "example.com" }, { entry = "example.com", reason = "twice" }]
}
`,
				ExpectError: regexp.MustCompile(`"example.com" appears more than once`),
			},
		},
	})
}

func TestAccList_PlansEntriesThatAreUnknownUntilApply(t *testing.T) {
	s := fakeagentmail.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(s) + `
resource "agentmail_inbox" "support" {
  username = "support"
}

resource "agentmail_list" "support" {
  direction = "send"
  list_type = "allow"
  entries   = agentmail_inbox.support.status == "paused" ? [] : [{ entry = agentmail_inbox.support.email }]
}
`,
				Check: fakeListIs(s, "", "", "send", "allow", "support@agentmail.to"),
			},
		},
	})
}
