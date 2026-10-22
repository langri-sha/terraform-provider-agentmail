package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func inboxSchemaAndState(t *testing.T, inboxID string) (resource.SchemaResponse, tftypes.Value) {
	t.Helper()
	var s resource.SchemaResponse
	newInboxResource().Schema(context.Background(), resource.SchemaRequest{}, &s)
	objectType := s.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, typ := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	values["inbox_id"] = tftypes.NewValue(tftypes.String, inboxID)
	return s, tftypes.NewValue(objectType, values)
}

func TestDestroyingAnInboxWarnsThatItsMailGoes(t *testing.T) {
	s, state := inboxSchemaAndState(t, "support@agentmail.to")
	req := resource.ModifyPlanRequest{
		State: tfsdk.State{Schema: s.Schema, Raw: state},
		Plan:  tfsdk.Plan{Schema: s.Schema, Raw: tftypes.NewValue(state.Type(), nil)},
	}
	var resp resource.ModifyPlanResponse
	newInboxResource().(resource.ResourceWithModifyPlan).ModifyPlan(context.Background(), req, &resp)
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics[0].Detail(), "destroys support@agentmail.to") {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}

func TestReplacingAnInboxWarnsWhichAttributeForcedIt(t *testing.T) {
	s, state := inboxSchemaAndState(t, "support@agentmail.to")
	req := resource.ModifyPlanRequest{
		State: tfsdk.State{Schema: s.Schema, Raw: state},
		Plan:  tfsdk.Plan{Schema: s.Schema, Raw: state},
	}
	resp := resource.ModifyPlanResponse{RequiresReplace: path.Paths{path.Root("username")}}
	newInboxResource().(resource.ResourceWithModifyPlan).ModifyPlan(context.Background(), req, &resp)
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics[0].Detail(), "because username changed") {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}

func TestUpdatingAnInboxInPlaceDoesNotWarn(t *testing.T) {
	s, state := inboxSchemaAndState(t, "support@agentmail.to")
	req := resource.ModifyPlanRequest{
		State: tfsdk.State{Schema: s.Schema, Raw: state},
		Plan:  tfsdk.Plan{Schema: s.Schema, Raw: state},
	}
	var resp resource.ModifyPlanResponse
	newInboxResource().(resource.ResourceWithModifyPlan).ModifyPlan(context.Background(), req, &resp)
	if len(resp.Diagnostics) != 0 {
		t.Errorf("diagnostics are %v", resp.Diagnostics)
	}
}
