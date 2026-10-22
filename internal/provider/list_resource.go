package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

var (
	_ resource.ResourceWithConfigure      = (*listResource)(nil)
	_ resource.ResourceWithImportState    = (*listResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*listResource)(nil)
	_ resource.ResourceWithValidateConfig = (*listResource)(nil)
)

func newListResource() resource.Resource { return &listResource{} }

type listResource struct {
	client *agentmail.Client
}

type listModel struct {
	ID              types.String `tfsdk:"id"`
	PodID           types.String `tfsdk:"pod_id"`
	InboxID         types.String `tfsdk:"inbox_id"`
	Direction       types.String `tfsdk:"direction"`
	ListType        types.String `tfsdk:"list_type"`
	Entries         types.Set    `tfsdk:"entries"`
	ReadOnlyEntries types.Set    `tfsdk:"read_only_entries"`
}

type listEntryModel struct {
	Entry  types.String `tfsdk:"entry"`
	Reason types.String `tfsdk:"reason"`
}

var listEntryType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"entry":  types.StringType,
	"reason": types.StringType,
}}

func (m *listModel) scope() agentmail.ListScope {
	return agentmail.ListScope{PodID: m.PodID.ValueString(), InboxID: m.InboxID.ValueString()}
}

func listID(scope agentmail.ListScope, direction, listType string) string {
	switch {
	case scope.InboxID != "":
		return strings.Join([]string{"inbox", scope.InboxID, direction, listType}, "/")
	case scope.PodID != "":
		return strings.Join([]string{"pod", scope.PodID, direction, listType}, "/")
	}
	return strings.Join([]string{"org", direction, listType}, "/")
}

func (r *listResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_list"
}

func (r *listResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaces := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "One AgentMail allow or block list, for one direction of mail, owned whole by Terraform.\n\n" +
			"The resource is authoritative: an apply removes entries added outside Terraform. " +
			"It adds entries before it removes any, so swapping entries never leaves a closed allow list empty, and so open.\n\n" +
			"~> **An allow list blocks every address it does not match from its first entry on.** " +
			"Destroying an allow list, or emptying `entries`, removes its entries and so allows every address again. " +
			"Changing an entry's `reason` deletes and re-adds the entry, because entries can't be updated; for an allow list whose only entry that is, the list is briefly open.\n\n" +
			"Reply lists are checked instead of receive lists for replies, and the most specific scope with a match wins: inbox, then pod, then organization.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "`inbox/<inbox_id>/<direction>/<list_type>`, `pod/<pod_id>/<direction>/<list_type>` or `org/<direction>/<list_type>`. Import a list by it.",
			},
			"pod_id": schema.StringAttribute{
				Optional:            true,
				PlanModifiers:       replaces,
				Validators:          []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("inbox_id"))},
				MarkdownDescription: "Manage this pod's list. Leave both `pod_id` and `inbox_id` unset for the organization's list.",
			},
			"inbox_id": schema.StringAttribute{
				Optional:            true,
				PlanModifiers:       replaces,
				MarkdownDescription: "Manage this inbox's list.",
			},
			"direction": schema.StringAttribute{
				Required:            true,
				PlanModifiers:       replaces,
				Validators:          []validator.String{stringvalidator.OneOf(agentmail.Directions...)},
				MarkdownDescription: "`send`, `receive` or `reply`.",
			},
			"list_type": schema.StringAttribute{
				Required:            true,
				PlanModifiers:       replaces,
				Validators:          []validator.String{stringvalidator.OneOf(agentmail.ListTypes...)},
				MarkdownDescription: "`allow` or `block`.",
			},
			"entries": schema.SetNestedAttribute{
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"entry": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "An email address, or a domain, which matches only itself and not its subdomains.",
						},
						"reason": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Why the entry is there.",
						},
					},
				},
				MarkdownDescription: "Every entry the list holds, apart from read-only ones. An empty set removes them all.",
			},
			"read_only_entries": schema.SetAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Entries AgentMail marks read-only. They can't be deleted through the API, so the resource leaves them alone.",
			},
		},
	}
}

func (r *listResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *listResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var entries types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("entries"), &entries)...)
	if resp.Diagnostics.HasError() || entries.IsNull() || entries.IsUnknown() {
		return
	}
	seen := map[string]bool{}
	for _, element := range entries.Elements() {
		obj, ok := element.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		entry, ok := obj.Attributes()["entry"].(types.String)
		if !ok || entry.IsNull() || entry.IsUnknown() {
			continue
		}
		if seen[entry.ValueString()] {
			resp.Diagnostics.AddAttributeError(path.Root("entries"), "Duplicate list entry",
				fmt.Sprintf("%q appears more than once. A list holds an entry once, with one reason.", entry.ValueString()))
		}
		seen[entry.ValueString()] = true
	}
}

func (r *listResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || !req.Plan.Raw.IsNull() {
		return
	}
	var state listModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if state.ListType.ValueString() == agentmail.ListTypeAllow && len(state.Entries.Elements()) > 0 && len(state.ReadOnlyEntries.Elements()) == 0 {
		resp.Diagnostics.AddWarning("Destroying an allow list opens it",
			fmt.Sprintf("This plan removes every entry from %s, so it allows every address again.", state.ID.ValueString()))
	}
}

func (r *listResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan listModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want, diags := wantedEntries(ctx, plan.Entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope, direction, listType := plan.scope(), plan.Direction.ValueString(), plan.ListType.ValueString()
	current, err := r.client.ListEntries(ctx, scope, direction, listType)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the list", err.Error())
		return
	}
	var unmanaged []string
	for _, e := range current {
		if _, ok := want[e.Entry]; !ok && !e.IsReadOnly() {
			unmanaged = append(unmanaged, e.Entry)
		}
	}
	id := listID(scope, direction, listType)
	if len(unmanaged) > 0 {
		resp.Diagnostics.AddError("The list already holds entries",
			fmt.Sprintf("%s holds %s, which the configuration doesn't list. Import the list with its ID, %q, or add the entries to the configuration.",
				id, strings.Join(unmanaged, ", "), id))
		return
	}
	plan.ID = types.StringValue(id)
	r.reconcile(ctx, &plan, current, want, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *listResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state listModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.ListEntries(ctx, state.scope(), state.Direction.ValueString(), state.ListType.ValueString())
	if agentmail.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read the list", err.Error())
		return
	}
	resp.Diagnostics.Append(state.setEntries(current)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *listResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan listModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want, diags := wantedEntries(ctx, plan.Entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.ListEntries(ctx, plan.scope(), plan.Direction.ValueString(), plan.ListType.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Could not read the list", err.Error())
		return
	}
	r.reconcile(ctx, &plan, current, want, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *listResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state listModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope, direction, listType := state.scope(), state.Direction.ValueString(), state.ListType.ValueString()
	current, err := r.client.ListEntries(ctx, scope, direction, listType)
	if agentmail.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read the list", err.Error())
		return
	}
	for _, e := range current {
		if e.IsReadOnly() {
			continue
		}
		if err := r.client.DeleteListEntry(ctx, scope, direction, listType, e.Entry); err != nil && !agentmail.IsNotFound(err) {
			resp.Diagnostics.AddError("Could not remove a list entry", fmt.Sprintf("Removing %s: %s", e.Entry, err))
		}
	}
}

func (r *listResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	var podID, inboxID, direction, listType string
	switch {
	case len(parts) == 4 && parts[0] == "inbox":
		inboxID, direction, listType = parts[1], parts[2], parts[3]
	case len(parts) == 4 && parts[0] == "pod":
		podID, direction, listType = parts[1], parts[2], parts[3]
	case len(parts) == 3 && parts[0] == "org":
		direction, listType = parts[1], parts[2]
	default:
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("%q is not inbox/<inbox_id>/<direction>/<list_type>, pod/<pod_id>/<direction>/<list_type> or org/<direction>/<list_type>.", req.ID))
		return
	}
	scope := agentmail.ListScope{PodID: podID, InboxID: inboxID}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), listID(scope, direction, listType))...)
	if podID != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("pod_id"), podID)...)
	}
	if inboxID != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("inbox_id"), inboxID)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("direction"), direction)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("list_type"), listType)...)
}

// reconcile turns current into want: it adds missing entries before it
// removes any, so a closed allow list stays closed. An entry whose reason
// changed is deleted and re-added, since entries can't be updated. The model
// ends up holding what the list holds afterwards, even after a failure.
func (r *listResource) reconcile(ctx context.Context, m *listModel, current []agentmail.ListEntry, want map[string]*string, diags *diag.Diagnostics) {
	scope, direction, listType := m.scope(), m.Direction.ValueString(), m.ListType.ValueString()
	have := map[string]agentmail.ListEntry{}
	for _, e := range current {
		have[e.Entry] = e
	}

	var add, readd, remove []string
	for entry, reason := range want {
		e, ok := have[entry]
		switch {
		case !ok:
			add = append(add, entry)
		case e.IsReadOnly():
			diags.AddAttributeError(path.Root("entries"), "Read-only list entry",
				fmt.Sprintf("AgentMail marks %s read-only, so the resource can't manage it. Remove it from entries; it shows in read_only_entries.", entry))
		case !equalReason(e.Reason, reason):
			readd = append(readd, entry)
		}
	}
	for entry, e := range have {
		if _, ok := want[entry]; !ok && !e.IsReadOnly() {
			remove = append(remove, entry)
		}
	}
	sort.Strings(add)
	sort.Strings(readd)
	sort.Strings(remove)

	create := func(entry string) bool {
		_, err := r.client.CreateListEntry(ctx, scope, direction, listType, agentmail.CreateListEntryRequest{Entry: entry, Reason: want[entry]})
		if err != nil {
			diags.AddError("Could not add a list entry", fmt.Sprintf("Adding %s: %s", entry, err))
			return false
		}
		return true
	}
	del := func(entry string) bool {
		if err := r.client.DeleteListEntry(ctx, scope, direction, listType, entry); err != nil && !agentmail.IsNotFound(err) {
			diags.AddError("Could not remove a list entry", fmt.Sprintf("Removing %s: %s", entry, err))
			return false
		}
		return true
	}
	if !diags.HasError() {
		for _, entry := range add {
			if !create(entry) {
				break
			}
		}
	}
	if !diags.HasError() {
		for _, entry := range readd {
			if !del(entry) || !create(entry) {
				break
			}
		}
	}
	if !diags.HasError() {
		for _, entry := range remove {
			if !del(entry) {
				break
			}
		}
	}

	after, err := r.client.ListEntries(ctx, scope, direction, listType)
	if err != nil {
		diags.AddError("Could not read the list back", err.Error())
		return
	}
	if diags.HasError() {
		diags.Append(m.setEntries(after)...)
		return
	}
	readOnly := []string{}
	for _, e := range after {
		if e.IsReadOnly() {
			readOnly = append(readOnly, e.Entry)
		}
	}
	set, d := types.SetValueFrom(ctx, types.StringType, readOnly)
	diags.Append(d...)
	m.ReadOnlyEntries = set
}

// setEntries sets entries and read_only_entries from what the list holds.
func (m *listModel) setEntries(current []agentmail.ListEntry) diag.Diagnostics {
	entries := []attr.Value{}
	readOnly := []attr.Value{}
	for _, e := range current {
		if e.IsReadOnly() {
			readOnly = append(readOnly, types.StringValue(e.Entry))
			continue
		}
		obj, diags := types.ObjectValue(listEntryType.AttrTypes, map[string]attr.Value{
			"entry":  types.StringValue(e.Entry),
			"reason": types.StringPointerValue(e.Reason),
		})
		if diags.HasError() {
			return diags
		}
		entries = append(entries, obj)
	}
	var diags diag.Diagnostics
	var d diag.Diagnostics
	m.Entries, d = types.SetValue(listEntryType, entries)
	diags.Append(d...)
	m.ReadOnlyEntries, d = types.SetValue(types.StringType, readOnly)
	diags.Append(d...)
	return diags
}

func wantedEntries(ctx context.Context, set types.Set) (map[string]*string, diag.Diagnostics) {
	var entries []listEntryModel
	diags := set.ElementsAs(ctx, &entries, false)
	want := make(map[string]*string, len(entries))
	for _, e := range entries {
		want[e.Entry.ValueString()] = knownString(e.Reason)
	}
	return want, diags
}

func equalReason(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
