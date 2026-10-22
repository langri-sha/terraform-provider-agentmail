// Package provider implements the terraform-provider-agentmail plugin.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

// APIKeyEnv names the environment variable the API key falls back to.
const APIKeyEnv = "AGENTMAIL_API_KEY"

// New returns a providerserver factory.
func New(version, commit string) func() provider.Provider {
	return func() provider.Provider {
		return &agentmailProvider{version: version, commit: commit}
	}
}

type agentmailProvider struct {
	version string
	commit  string
}

type providerModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *agentmailProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "agentmail"
	resp.Version = p.version
}

func (p *agentmailProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Terraform provider for [AgentMail](https://agentmail.to). It authenticates with an AgentMail API key, which may be ephemeral, such as one read from a secret store with an ephemeral resource.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "AgentMail API key. Defaults to the `" + APIKeyEnv + "` environment variable. The key's scope and permissions bound what the provider can manage.",
			},
			"base_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Base URL of the AgentMail API. Defaults to `" + agentmail.DefaultBaseURL + "`; use `https://api.agentmail.eu` for the EU.",
			},
		},
	}
}

func (p *agentmailProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Unknown API key",
			"The provider needs a known api_key to configure. Pass a value that is known at plan time, such as an ephemeral resource's attribute.")
	}
	if data.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown base URL", "The provider needs a known base_url to configure.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := data.APIKey.ValueString()
	if data.APIKey.IsNull() {
		apiKey = os.Getenv(APIKeyEnv)
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing API key",
			"Set the provider's api_key or the "+APIKeyEnv+" environment variable.")
		return
	}

	client, err := agentmail.New(agentmail.Config{
		BaseURL:   data.BaseURL.ValueString(),
		APIKey:    apiKey,
		UserAgent: "terraform-provider-agentmail/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Invalid provider configuration", err.Error())
		return
	}
	resp.DataSourceData = client
	resp.ResourceData = client
	resp.EphemeralResourceData = client
}

func (p *agentmailProvider) Resources(context.Context) []func() resource.Resource {
	return nil
}

func (p *agentmailProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
