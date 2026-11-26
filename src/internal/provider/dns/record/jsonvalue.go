package record

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var (
	_ basetypes.StringTypable                    = normalizedJSONType{}
	_ basetypes.StringValuableWithSemanticEquals = normalizedJSON{}
	_ xattr.ValidateableAttribute                = normalizedJSON{}
)

type normalizedJSONType struct {
	basetypes.StringType
}

func (t normalizedJSONType) String() string { return "record.normalizedJSONType" }

func (t normalizedJSONType) ValueType(context.Context) attr.Value { return normalizedJSON{} }

func (t normalizedJSONType) Equal(o attr.Type) bool {
	other, ok := o.(normalizedJSONType)
	return ok && t.StringType.Equal(other.StringType)
}

func (t normalizedJSONType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return normalizedJSON{StringValue: in}, nil
}

func (t normalizedJSONType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	raw, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	str, ok := raw.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type of %T", raw)
	}
	return normalizedJSON{StringValue: str}, nil
}

type normalizedJSON struct {
	basetypes.StringValue
}

func newNormalizedJSON(value string) normalizedJSON {
	return normalizedJSON{StringValue: basetypes.NewStringValue(value)}
}

func newNormalizedJSONNull() normalizedJSON {
	return normalizedJSON{StringValue: basetypes.NewStringNull()}
}

func newNormalizedJSONUnknown() normalizedJSON {
	return normalizedJSON{StringValue: basetypes.NewStringUnknown()}
}

func (v normalizedJSON) Type(context.Context) attr.Type { return normalizedJSONType{} }

func (v normalizedJSON) Equal(o attr.Value) bool {
	other, ok := o.(normalizedJSON)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v normalizedJSON) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	o, ok := other.(normalizedJSON)
	if !ok {
		diags.AddError("Semantic equality check error", fmt.Sprintf("Expected normalized JSON value, got %T. Please report this to the provider developers.", other))
		return false, diags
	}
	left, err := decodeJSON(v.ValueString())
	if err != nil {
		return false, diags
	}
	right, err := decodeJSON(o.ValueString())
	if err != nil {
		return false, diags
	}
	return reflect.DeepEqual(left, right), diags
}

func (v normalizedJSON) ValidateAttribute(_ context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	if _, err := decodeJSON(v.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON", fmt.Sprintf("The value is not valid JSON: %s", err))
	}
}

func decodeJSON(raw string) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("unexpected data after the JSON document")
	}
	return normalizeNumbers(out), nil
}

func normalizeNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeNumbers(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeNumbers(item)
		}
		return out
	}
	return value
}

func decodeJSONForRequest(raw string) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type keepEquivalentJSON struct{}

func (keepEquivalentJSON) Description(context.Context) string {
	return "Keeps the prior value when the configured JSON differs only in key order or whitespace."
}

func (m keepEquivalentJSON) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (keepEquivalentJSON) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	equal, diags := newNormalizedJSON(req.PlanValue.ValueString()).StringSemanticEquals(ctx, newNormalizedJSON(req.StateValue.ValueString()))
	resp.Diagnostics.Append(diags...)
	if equal {
		resp.PlanValue = req.StateValue
	}
}
