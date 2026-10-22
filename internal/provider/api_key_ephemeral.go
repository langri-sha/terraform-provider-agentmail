package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

var (
	_ ephemeral.EphemeralResourceWithConfigure      = (*apiKeyEphemeral)(nil)
	_ ephemeral.EphemeralResourceWithClose          = (*apiKeyEphemeral)(nil)
	_ ephemeral.EphemeralResourceWithValidateConfig = (*apiKeyEphemeral)(nil)
)

func newAPIKeyEphemeral() ephemeral.EphemeralResource { return &apiKeyEphemeral{} }

type apiKeyEphemeral struct {
	client *agentmail.Client
}

type apiKeyModel struct {
	Name          types.String `tfsdk:"name"`
	PodID         types.String `tfsdk:"pod_id"`
	InboxID       types.String `tfsdk:"inbox_id"`
	Permissions   types.Set    `tfsdk:"permissions"`
	TTL           types.String `tfsdk:"ttl"`
	ExpiresAt     types.String `tfsdk:"expires_at"`
	RevokeOnClose types.Bool   `tfsdk:"revoke_on_close"`
	APIKey        types.String `tfsdk:"api_key"`
	APIKeyID      types.String `tfsdk:"api_key_id"`
	Prefix        types.String `tfsdk:"prefix"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

// apiKeyPrivate is what Close needs to revoke the key.
type apiKeyPrivate struct {
	APIKeyID string `json:"api_key_id"`
	PodID    string `json:"pod_id,omitempty"`
	InboxID  string `json:"inbox_id,omitempty"`
}

const apiKeyPrivateKey = "revoke"

func (e *apiKeyEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (e *apiKeyEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Mints an AgentMail API key without writing its secret to plan or state.\n\n" +
			"Terraform opens ephemeral resources during `plan` as well as `apply`, so **every plan and every apply mints a new key**, " +
			"and the provider's own key needs the `api_key_create` permission at plan time. " +
			"By default the key is revoked when Terraform closes the resource at the end of the run, which suits handing it to another provider configuration. " +
			"To store the key somewhere that outlives the run, set `revoke_on_close = false` with `ttl` or `expires_at`, " +
			"and gate the resource with `count` so it mints only when you rotate.\n\n" +
			"A key can't outlive the key that mints it. Without `ttl` or `expires_at` it inherits the provider key's expiry, and never expires when that key never does.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The key's name.",
			},
			"pod_id": schema.StringAttribute{
				Optional:            true,
				Validators:          []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("inbox_id"))},
				MarkdownDescription: "Scope the key to this pod. Leave both `pod_id` and `inbox_id` unset for an organization key.",
			},
			"inbox_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Scope the key to this inbox.",
			},
			"permissions": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(agentmail.Permissions...)),
				},
				MarkdownDescription: "The permissions to grant, such as `inbox_read` or `message_send`. " +
					"**Unset grants every permission**, intersected with the key's scope. A key can't grant permissions its creator lacks.",
			},
			"ttl": schema.StringAttribute{
				Optional:            true,
				Validators:          []validator.String{durationValidator{}, stringvalidator.ConflictsWith(path.MatchRoot("expires_at"))},
				MarkdownDescription: "How long the key lives, as a Go duration such as `720h`.",
			},
			"expires_at": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{rfc3339Validator{}},
				MarkdownDescription: "When the key expires, in RFC 3339. Set it or `ttl`, or read the inherited expiry here. AgentMail can't change it afterwards.",
			},
			"revoke_on_close": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Revoke the key when Terraform closes the resource at the end of the run. Defaults to `true`. Setting `false` requires `ttl` or `expires_at`.",
			},
			"api_key": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The secret. AgentMail returns it only when the key is created.",
			},
			"api_key_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The key's ID.",
			},
			"prefix": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The secret's prefix, which AgentMail shows to identify the key.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the key was created, in RFC 3339.",
			},
		},
	}
}

func (e *apiKeyEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*agentmail.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *agentmail.Client, got %T.", req.ProviderData))
		return
	}
	e.client = client
}

func (e *apiKeyEphemeral) ValidateConfig(ctx context.Context, req ephemeral.ValidateConfigRequest, resp *ephemeral.ValidateConfigResponse) {
	var config apiKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keepsKey := !config.RevokeOnClose.IsNull() && !config.RevokeOnClose.IsUnknown() && !config.RevokeOnClose.ValueBool()
	if keepsKey && config.TTL.IsNull() && config.ExpiresAt.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("revoke_on_close"), "A kept key needs an expiry",
			"Every plan and apply mints a new key, so a key that isn't revoked on close must expire: set ttl or expires_at.")
	}
}

func (e *apiKeyEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var config apiKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	request := agentmail.CreateAPIKeyRequest{Name: knownString(config.Name)}
	if !config.Permissions.IsNull() {
		var names []string
		resp.Diagnostics.Append(config.Permissions.ElementsAs(ctx, &names, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		granted := make(map[string]bool, len(names))
		for _, n := range names {
			granted[n] = true
		}
		request.Permissions = &granted
	}
	switch {
	case !config.TTL.IsNull():
		ttl, err := time.ParseDuration(config.TTL.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid ttl", err.Error())
			return
		}
		at := time.Now().Add(ttl).UTC().Truncate(time.Second)
		request.ExpiresAt = &at
	case !config.ExpiresAt.IsNull():
		at, err := time.Parse(time.RFC3339, config.ExpiresAt.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Invalid expires_at", err.Error())
			return
		}
		request.ExpiresAt = &at
	}

	scope := agentmail.APIKeyScope{PodID: config.PodID.ValueString(), InboxID: config.InboxID.ValueString()}
	created, err := e.client.CreateAPIKey(ctx, scope, request)
	if err != nil {
		resp.Diagnostics.AddError("Could not mint the API key", err.Error()+explainKeyRefusal(err, request.ExpiresAt != nil))
		return
	}

	config.APIKey = types.StringValue(created.APIKey)
	config.APIKeyID = types.StringValue(created.APIKeyID)
	config.Prefix = types.StringValue(created.Prefix)
	config.CreatedAt = types.StringValue(created.CreatedAt.Format(time.RFC3339Nano))
	if config.ExpiresAt.IsNull() || config.ExpiresAt.IsUnknown() {
		config.ExpiresAt = types.StringNull()
		if created.ExpiresAt != nil {
			config.ExpiresAt = types.StringValue(created.ExpiresAt.Format(time.RFC3339))
		}
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, &config)...)

	if config.RevokeOnClose.IsNull() || config.RevokeOnClose.ValueBool() {
		private, err := json.Marshal(apiKeyPrivate{APIKeyID: created.APIKeyID, PodID: scope.PodID, InboxID: scope.InboxID})
		if err != nil {
			resp.Diagnostics.AddError("Could not remember the key to revoke", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, apiKeyPrivateKey, private)...)
	}
}

func (e *apiKeyEphemeral) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	raw, diags := req.Private.GetKey(ctx, apiKeyPrivateKey)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || raw == nil {
		return
	}
	var private apiKeyPrivate
	if err := json.Unmarshal(raw, &private); err != nil {
		resp.Diagnostics.AddError("Could not read the key to revoke", err.Error())
		return
	}
	scope := agentmail.APIKeyScope{PodID: private.PodID, InboxID: private.InboxID}
	if err := e.client.DeleteAPIKey(ctx, scope, private.APIKeyID); err != nil && !agentmail.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not revoke the API key",
			fmt.Sprintf("Revoking %s failed, so it stays valid until it expires: %s", private.APIKeyID, err))
	}
}

func explainKeyRefusal(err error, setsExpiry bool) string {
	var apiErr *agentmail.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		return ""
	}
	switch {
	case apiErr.Code == "missing_permission":
		return "\n\nThe provider's API key can't create keys. Terraform opens ephemeral resources during plan too, so plan with a key that holds api_key_create, or gate this resource with count."
	case apiErr.Code == "permission_escalation":
		return "\n\nA key can't grant permissions the provider's key lacks."
	case setsExpiry:
		return "\n\nA key can't outlive the key that mints it: the expiry must come before the provider's key expires."
	}
	return ""
}

type durationValidator struct{}

func (durationValidator) Description(context.Context) string {
	return "a positive Go duration, such as 720h"
}

func (v durationValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (durationValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	d, err := time.ParseDuration(req.ConfigValue.ValueString())
	if err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration",
			fmt.Sprintf("%q is not a positive Go duration, such as 720h.", req.ConfigValue.ValueString()))
	}
}

type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string { return "an RFC 3339 timestamp" }

func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (rfc3339Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp",
			fmt.Sprintf("%q is not an RFC 3339 timestamp, such as 2027-01-01T00:00:00Z.", req.ConfigValue.ValueString()))
	}
}
