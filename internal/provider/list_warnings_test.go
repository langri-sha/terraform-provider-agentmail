package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func planListDestroy(t *testing.T, listType string, entries, readOnly int) *resource.ModifyPlanResponse {
	t.Helper()
	ctx := context.Background()
	var s resource.SchemaResponse
	newListResource().Schema(ctx, resource.SchemaRequest{}, &s)
	objectType := s.Schema.Type().TerraformType(ctx).(tftypes.Object)
	entryType := objectType.AttributeTypes["entries"].(tftypes.Set).ElementType
	var entryValues, readOnlyValues []tftypes.Value
	for range entries {
		entryValues = append(entryValues, tftypes.NewValue(entryType, map[string]tftypes.Value{
			"entry":  tftypes.NewValue(tftypes.String, "example.com"),
			"reason": tftypes.NewValue(tftypes.String, nil),
		}))
	}
	for range readOnly {
		readOnlyValues = append(readOnlyValues, tftypes.NewValue(tftypes.String, "abuse.example"))
	}
	state := tftypes.NewValue(objectType, map[string]tftypes.Value{
		"id":                tftypes.NewValue(tftypes.String, "org/receive/"+listType),
		"pod_id":            tftypes.NewValue(tftypes.String, nil),
		"inbox_id":          tftypes.NewValue(tftypes.String, nil),
		"direction":         tftypes.NewValue(tftypes.String, "receive"),
		"list_type":         tftypes.NewValue(tftypes.String, listType),
		"entries":           tftypes.NewValue(objectType.AttributeTypes["entries"], entryValues),
		"read_only_entries": tftypes.NewValue(objectType.AttributeTypes["read_only_entries"], readOnlyValues),
	})
	req := resource.ModifyPlanRequest{
		State: tfsdk.State{Schema: s.Schema, Raw: state},
		Plan:  tfsdk.Plan{Schema: s.Schema, Raw: tftypes.NewValue(objectType, nil)},
	}
	var resp resource.ModifyPlanResponse
	newListResource().(resource.ResourceWithModifyPlan).ModifyPlan(ctx, req, &resp)
	return &resp
}

func TestDestroyingANonEmptyAllowListWarnsThatItOpens(t *testing.T) {
	cases := map[string]struct {
		listType string
		entries  int
		readOnly int
		warns    bool
	}{
		"an allow list with entries":             {"allow", 1, 0, true},
		"an empty allow list":                    {"allow", 0, 0, false},
		"an allow list kept closed by read-only": {"allow", 1, 1, false},
		"a block list":                           {"block", 1, 0, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := planListDestroy(t, c.listType, c.entries, c.readOnly)
			if got := resp.Diagnostics.WarningsCount() == 1; got != c.warns {
				t.Errorf("diagnostics are %v", resp.Diagnostics)
			}
		})
	}
}
