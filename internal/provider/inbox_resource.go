package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/dynamicplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

var (
	_ resource.ResourceWithConfigure   = (*inboxResource)(nil)
	_ resource.ResourceWithImportState = (*inboxResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*inboxResource)(nil)
)

func newInboxResource() resource.Resource { return &inboxResource{} }

type inboxResource struct {
	client *agentmail.Client
}

type inboxModel struct {
	InboxID     types.String  `tfsdk:"inbox_id"`
	Email       types.String  `tfsdk:"email"`
	Username    types.String  `tfsdk:"username"`
	Domain      types.String  `tfsdk:"domain"`
	PodID       types.String  `tfsdk:"pod_id"`
	ClientID    types.String  `tfsdk:"client_id"`
	DisplayName types.String  `tfsdk:"display_name"`
	Status      types.String  `tfsdk:"status"`
	Metadata    types.Dynamic `tfsdk:"metadata"`
	CreatedAt   types.String  `tfsdk:"created_at"`
}

const replacementDeletesMail = "AgentMail deletes an inbox's messages, threads and drafts with it, and cannot restore them."

func (r *inboxResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inbox"
}

func (r *inboxResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	createOnly := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional: true,
			Computed: true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
				stringplanmodifier.RequiresReplace(),
			},
			MarkdownDescription: description + " Changing it replaces the inbox, which deletes its mail.",
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An AgentMail inbox.\n\n" +
			"~> **Replacing or destroying an inbox deletes its messages, threads and drafts.** " +
			"`username`, `domain`, `pod_id` and `client_id` can't change in place, so changing any of them replaces the inbox. " +
			"Plans that replace or destroy an inbox carry a warning; consider `lifecycle { prevent_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			"inbox_id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The inbox's ID, which is its email address. Import an inbox by it.",
			},
			"email": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The inbox's email address.",
			},
			"username": createOnly("The part of the address before the `@`. AgentMail generates one when unset."),
			"domain":   createOnly("The part of the address after the `@`: `agentmail.to`, a verified domain, or a subdomain of a verified domain that has subdomains enabled. Defaults to `agentmail.to`."),
			"pod_id":   createOnly("The pod the inbox belongs to. Defaults to the organization's default pod."),
			"client_id": createOnly("A client-chosen ID that makes creation idempotent: creating again with the same `client_id` returns the existing inbox. " +
				"AgentMail allows no `@` in it."),
			"display_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The name shown with the address, as in `Display Name <username@domain>`. Removing it from the configuration leaves AgentMail's value in place.",
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.OneOf(agentmail.InboxStatusActive, agentmail.InboxStatusPaused)},
				MarkdownDescription: "`active` or `paused`. A paused inbox neither sends nor receives: mail that arrives while it is paused is not delivered, then or on resume. Removing it from the configuration leaves AgentMail's value in place.",
			},
			"metadata": schema.DynamicAttribute{
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Dynamic{dynamicplanmodifier.UseStateForUnknown()},
				Validators:    []validator.Dynamic{metadataValidator{}},
				MarkdownDescription: "Custom metadata: an object of up to 256 strings, numbers and booleans, which keep their types. " +
					"Keys and strings are at most 256 characters. The configuration is authoritative: an apply removes keys set elsewhere. Updates send only the keys that changed. " +
					"`{}` removes all metadata; removing the attribute leaves AgentMail's value in place.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "When the inbox was created, in RFC 3339.",
			},
		},
	}
}

func (r *inboxResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*agentmail.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *agentmail.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *inboxResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("inbox_id"), &id)...)
	if req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddWarning("Destroying an inbox deletes its mail",
			fmt.Sprintf("This plan destroys %s. %s", id.ValueString(), replacementDeletesMail))
		return
	}
	if len(resp.RequiresReplace) > 0 {
		changed := make([]string, len(resp.RequiresReplace))
		for i, p := range resp.RequiresReplace {
			changed[i] = p.String()
		}
		resp.Diagnostics.AddWarning("Replacing an inbox deletes its mail",
			fmt.Sprintf("This plan replaces %s because %s changed. %s", id.ValueString(), strings.Join(changed, ", "), replacementDeletesMail))
	}
}

func (r *inboxResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	metadata, err := metadataFromDynamic(plan.Metadata)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid inbox metadata", err.Error())
		return
	}
	created, err := r.client.CreateInbox(ctx, plan.PodID.ValueString(), agentmail.CreateInboxRequest{
		Username:    knownString(plan.Username),
		Domain:      knownString(plan.Domain),
		DisplayName: knownString(plan.DisplayName),
		ClientID:    knownString(plan.ClientID),
		Status:      knownString(plan.Status),
		Metadata:    metadata,
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create the inbox", err.Error())
		return
	}
	state := plan
	resp.Diagnostics.Append(state.fromAPI(created)...)
	if !plan.Metadata.IsUnknown() {
		state.Metadata = plan.Metadata
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inboxResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	inbox, err := r.client.GetInbox(ctx, state.InboxID.ValueString())
	if agentmail.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read the inbox", err.Error())
		return
	}
	prior := state.Metadata
	resp.Diagnostics.Append(state.fromAPI(inbox)...)
	if known, err := metadataFromDynamic(prior); err == nil && !prior.IsNull() && metadataEqual(known, inbox.Metadata) {
		state.Metadata = prior
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inboxResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var update agentmail.UpdateInboxRequest
	if !plan.DisplayName.IsUnknown() && !plan.DisplayName.Equal(state.DisplayName) {
		update.DisplayName = knownString(plan.DisplayName)
	}
	if !plan.Status.IsUnknown() && !plan.Status.Equal(state.Status) {
		update.Status = knownString(plan.Status)
	}
	if !plan.Metadata.IsUnknown() {
		from, err := metadataFromDynamic(state.Metadata)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid inbox metadata in state", err.Error())
			return
		}
		to, err := metadataFromDynamic(plan.Metadata)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid inbox metadata", err.Error())
			return
		}
		update.Metadata = metadataPatch(from, to)
	}

	result := plan
	if update.DisplayName != nil || update.Status != nil || update.Metadata != nil {
		updated, err := r.client.UpdateInbox(ctx, state.InboxID.ValueString(), update)
		if err != nil {
			resp.Diagnostics.AddError("Could not update the inbox", err.Error())
			return
		}
		resp.Diagnostics.Append(result.fromAPI(updated)...)
	}
	if !plan.Metadata.IsUnknown() {
		result.Metadata = plan.Metadata
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *inboxResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteInbox(ctx, state.InboxID.ValueString())
	if err != nil && !agentmail.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete the inbox", err.Error())
	}
}

func (r *inboxResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("inbox_id"), req, resp)
}

// fromAPI sets every attribute AgentMail returns. AgentMail never returns
// username and domain, so they come from the address.
func (m *inboxModel) fromAPI(in *agentmail.Inbox) diag.Diagnostics {
	m.InboxID = types.StringValue(in.InboxID)
	m.Email = types.StringValue(in.Email)
	at := strings.LastIndex(in.Email, "@")
	if at < 0 {
		return diag.Diagnostics{diag.NewErrorDiagnostic("Unreadable inbox", fmt.Sprintf("AgentMail returned %q as the inbox's address.", in.Email))}
	}
	m.Username = types.StringValue(in.Email[:at])
	m.Domain = types.StringValue(in.Email[at+1:])
	m.PodID = types.StringValue(in.PodID)
	m.ClientID = types.StringPointerValue(in.ClientID)
	m.DisplayName = types.StringPointerValue(in.DisplayName)
	m.Status = types.StringValue(in.EffectiveStatus())
	m.CreatedAt = types.StringValue(in.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	metadata, diags := metadataToDynamic(in.Metadata)
	m.Metadata = metadata
	return diags
}

// knownString is the value of a known, non-null string, or nil.
func knownString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueStringPointer()
}
