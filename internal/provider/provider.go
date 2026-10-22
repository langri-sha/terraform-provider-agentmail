// Package provider implements the terraform-provider-agentmail plugin.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

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

func (p *agentmailProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "agentmail"
	resp.Version = p.version
}

func (p *agentmailProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Terraform provider for AgentMail.",
	}
}

func (p *agentmailProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *agentmailProvider) Resources(context.Context) []func() resource.Resource {
	return nil
}

func (p *agentmailProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
