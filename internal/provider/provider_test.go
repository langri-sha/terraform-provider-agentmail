package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

func TestProviderSchemaHasNoDiagnostics(t *testing.T) {
	server, err := providerserver.NewProtocol6WithError(New("test", "none")())()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s", d.Summary, d.Detail)
	}
}

func configure(t *testing.T, apiKey, baseURL tftypes.Value) *provider.ConfigureResponse {
	t.Helper()
	p := New("test", "none")()
	var schemaResp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schemaResp)
	raw := tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_key":  tftypes.String,
		"base_url": tftypes.String,
	}}, map[string]tftypes.Value{"api_key": apiKey, "base_url": baseURL})
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw},
	}, &resp)
	return &resp
}

func nullString() tftypes.Value { return tftypes.NewValue(tftypes.String, nil) }

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

func TestConfigureUsesTheAPIKeyAttribute(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	resp := configure(t, str("am_attribute"), nullString())
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if _, ok := resp.ResourceData.(*agentmail.Client); !ok {
		t.Errorf("resource data is %T", resp.ResourceData)
	}
}

func TestConfigureFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv(APIKeyEnv, "am_env")
	if resp := configure(t, nullString(), nullString()); resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
}

func TestConfigureFailsWithoutAnAPIKey(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	resp := configure(t, nullString(), nullString())
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Missing API key" {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}

func TestConfigureFailsOnAnUnknownAPIKey(t *testing.T) {
	resp := configure(t, tftypes.NewValue(tftypes.String, tftypes.UnknownValue), nullString())
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Unknown API key" {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}

func TestConfigureRejectsABaseURLThatIsNotAbsolute(t *testing.T) {
	resp := configure(t, str("am_attribute"), str("api.agentmail.eu"))
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Invalid provider configuration" {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}
