package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/langri-sha/terraform-provider-agentmail/internal/agentmail"
)

// metadataFromDynamic reads an inbox's metadata attribute, an object or map
// of strings, numbers and booleans. It returns nil for a null attribute.
func metadataFromDynamic(v types.Dynamic) (agentmail.Metadata, error) {
	if v.IsNull() || v.IsUnknown() || v.IsUnderlyingValueNull() {
		return nil, nil
	}
	var elements map[string]attr.Value
	switch u := v.UnderlyingValue().(type) {
	case basetypes.ObjectValue:
		elements = u.Attributes()
	case basetypes.MapValue:
		elements = u.Elements()
	default:
		return nil, fmt.Errorf("metadata must be an object of strings, numbers and booleans, not %s", u.Type(context.Background()))
	}
	md := make(agentmail.Metadata, len(elements))
	for k, e := range elements {
		if e.IsNull() {
			return nil, fmt.Errorf("metadata %q is null; remove the key instead", k)
		}
		if e.IsUnknown() {
			return nil, errUnknownMetadata
		}
		switch e := e.(type) {
		case basetypes.StringValue:
			md[k] = e.ValueString()
		case basetypes.BoolValue:
			md[k] = e.ValueBool()
		case basetypes.NumberValue:
			md[k] = json.Number(e.ValueBigFloat().Text('g', -1))
		default:
			return nil, fmt.Errorf("metadata %q must be a string, number or boolean, not %s", k, e.Type(context.Background()))
		}
	}
	return md, nil
}

var errUnknownMetadata = fmt.Errorf("metadata holds an unknown value")

// metadataToDynamic builds the metadata attribute from what AgentMail
// returns, as an object so each value keeps its type.
func metadataToDynamic(md agentmail.Metadata) (types.Dynamic, diag.Diagnostics) {
	if len(md) == 0 {
		return types.DynamicNull(), nil
	}
	attrTypes := make(map[string]attr.Type, len(md))
	values := make(map[string]attr.Value, len(md))
	for k, v := range md {
		switch v := v.(type) {
		case string:
			attrTypes[k], values[k] = types.StringType, types.StringValue(v)
		case bool:
			attrTypes[k], values[k] = types.BoolType, types.BoolValue(v)
		case json.Number:
			f, ok := new(big.Float).SetString(v.String())
			if !ok {
				return types.DynamicNull(), diag.Diagnostics{diag.NewErrorDiagnostic("Unreadable inbox metadata", fmt.Sprintf("AgentMail returned %q for metadata %q, which is not a number.", v, k))}
			}
			attrTypes[k], values[k] = types.NumberType, types.NumberValue(f)
		default:
			return types.DynamicNull(), diag.Diagnostics{diag.NewErrorDiagnostic("Unreadable inbox metadata", fmt.Sprintf("AgentMail returned a %T for metadata %q.", v, k))}
		}
	}
	obj, diags := types.ObjectValue(attrTypes, values)
	return types.DynamicValue(obj), diags
}

func metadataValueEqual(a, b any) bool {
	an, aNum := a.(json.Number)
	bn, bNum := b.(json.Number)
	if aNum && bNum {
		af, aok := new(big.Float).SetString(an.String())
		bf, bok := new(big.Float).SetString(bn.String())
		return aok && bok && af.Cmp(bf) == 0
	}
	if aNum || bNum {
		return false
	}
	return a == b
}

func metadataEqual(a, b agentmail.Metadata) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !metadataValueEqual(v, w) {
			return false
		}
	}
	return true
}

// metadataPatch is the merge patch that turns from into to, or nil when
// nothing changes. Emptying the metadata clears it with null, because
// AgentMail rejects an empty object.
func metadataPatch(from, to agentmail.Metadata) *agentmail.MetadataPatch {
	if len(to) == 0 {
		if len(from) == 0 {
			return nil
		}
		return &agentmail.MetadataPatch{Clear: true}
	}
	patch := &agentmail.MetadataPatch{Set: map[string]any{}}
	for k, v := range to {
		if old, ok := from[k]; !ok || !metadataValueEqual(old, v) {
			patch.Set[k] = v
		}
	}
	for k := range from {
		if _, ok := to[k]; !ok {
			patch.Remove = append(patch.Remove, k)
		}
	}
	if len(patch.Set) == 0 && len(patch.Remove) == 0 {
		return nil
	}
	return patch
}

type metadataValidator struct{}

func (metadataValidator) Description(context.Context) string {
	return "an object of up to 256 strings, numbers and booleans, with keys and strings of at most 256 characters"
}

func (v metadataValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (metadataValidator) ValidateDynamic(_ context.Context, req validator.DynamicRequest, resp *validator.DynamicResponse) {
	md, err := metadataFromDynamic(req.ConfigValue)
	if err == errUnknownMetadata {
		return
	}
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid inbox metadata", err.Error())
		return
	}
	if len(md) > 256 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid inbox metadata", fmt.Sprintf("AgentMail allows up to 256 metadata keys, not %d.", len(md)))
	}
	for k, v := range md {
		if utf8.RuneCountInString(k) > 256 {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid inbox metadata", fmt.Sprintf("Metadata key %q is longer than 256 characters.", k))
		}
		if s, ok := v.(string); ok && utf8.RuneCountInString(s) > 256 {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid inbox metadata", fmt.Sprintf("Metadata %q is longer than 256 characters.", k))
		}
	}
}
