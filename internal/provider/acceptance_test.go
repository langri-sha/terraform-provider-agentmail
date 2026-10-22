package provider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/langri-sha/terraform-provider-agentmail/internal/fakeagentmail"
)

var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"agentmail": providerserver.NewProtocol6WithError(New("test", "none")()),
}

func providerBlock(s *fakeagentmail.Server) string {
	return fmt.Sprintf("provider \"agentmail\" {\n  base_url = %q\n  api_key  = %q\n}\n", s.URL(), s.RootKey())
}

func decodeJSON(raw string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

func fakeInboxContains(s *fakeagentmail.Server, id string, parts ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := s.InboxJSON(id)
		if got == "" {
			return fmt.Errorf("the fake has no inbox %s", id)
		}
		for _, p := range parts {
			if !strings.Contains(got, p) {
				return fmt.Errorf("inbox %s is %s, want it to contain %s", id, got, p)
			}
		}
		return nil
	}
}

func fakeInboxLacks(s *fakeagentmail.Server, id string, parts ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := s.InboxJSON(id)
		for _, p := range parts {
			if strings.Contains(got, p) {
				return fmt.Errorf("inbox %s is %s, want it without %s", id, got, p)
			}
		}
		return nil
	}
}

func lastRequest(s *fakeagentmail.Server, method, route string) string {
	exchanges := s.Exchanges()
	for i := len(exchanges) - 1; i >= 0; i-- {
		if e := exchanges[i]; e.Method == method && e.Route == route {
			return string(e.Request)
		}
	}
	return ""
}

func lastRequestIs(s *fakeagentmail.Server, method, route, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := lastRequest(s, method, route)
		gotJSON, err := decodeJSON(got)
		if err != nil {
			return fmt.Errorf("the last %s %s sent %q: %w", method, route, got, err)
		}
		wantJSON, err := decodeJSON(want)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(gotJSON, wantJSON) {
			return fmt.Errorf("the last %s %s sent %s, want %s", method, route, got, want)
		}
		return nil
	}
}
