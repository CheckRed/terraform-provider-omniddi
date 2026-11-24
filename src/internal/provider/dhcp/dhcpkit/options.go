package dhcpkit

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type OptionModel struct {
	Code      types.Int64  `tfsdk:"code"`
	Name      types.String `tfsdk:"name"`
	Space     types.String `tfsdk:"space"`
	CSVFormat types.Bool   `tfsdk:"csv_format"`
	Data      types.String `tfsdk:"data"`
	DataType  types.String `tfsdk:"data_type"`
}

var optionAttrTypes = map[string]attr.Type{
	"code":       types.Int64Type,
	"name":       types.StringType,
	"space":      types.StringType,
	"csv_format": types.BoolType,
	"data":       types.StringType,
	"data_type":  types.StringType,
}

func OptionObjectType() types.ObjectType { return types.ObjectType{AttrTypes: optionAttrTypes} }

func OptionsAttribute(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"code":       schema.Int64Attribute{Required: true, MarkdownDescription: "DHCP option code."},
			"data":       schema.StringAttribute{Required: true, MarkdownDescription: "Option value, in the option's data format."},
			"name":       schema.StringAttribute{Optional: true, MarkdownDescription: "Option name, for example `routers`."},
			"space":      schema.StringAttribute{Optional: true, MarkdownDescription: "Option space, for example `dhcp4` or `dhcp6`."},
			"csv_format": schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether `data` is comma-separated values."},
			"data_type":  schema.StringAttribute{Optional: true, MarkdownDescription: "Option data type, for example `ipv4-address`."},
		}},
	}
}

func OptionsToWire(ctx context.Context, list types.List) ([]map[string]any, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var models []OptionModel
	diags := list.ElementsAs(ctx, &models, false)
	if diags.HasError() {
		return nil, diags
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entry := map[string]any{"code": m.Code.ValueInt64(), "data": m.Data.ValueString()}
		putString(entry, "name", m.Name)
		putString(entry, "space", m.Space)
		putString(entry, "data_type", m.DataType)
		if !m.CSVFormat.IsNull() && !m.CSVFormat.IsUnknown() {
			entry["csv_format"] = m.CSVFormat.ValueBool()
		}
		out = append(out, entry)
	}
	return out, nil
}

func putString(entry map[string]any, key string, v types.String) {
	if !v.IsNull() && !v.IsUnknown() {
		entry[key] = v.ValueString()
	}
}

func OptionsFromWire(raw []map[string]any, prior types.List) (types.List, diag.Diagnostics) {
	if len(raw) == 0 && prior.IsNull() {
		return types.ListNull(OptionObjectType()), nil
	}
	elems := make([]attr.Value, 0, len(raw))
	for _, item := range raw {
		obj, diags := types.ObjectValue(optionAttrTypes, map[string]attr.Value{
			"code":       intValue(item["code"]),
			"name":       stringValue(item["name"]),
			"space":      stringValue(item["space"]),
			"csv_format": boolValue(csvFlag(item)),
			"data":       stringValue(item["data"]),
			"data_type":  stringValue(item["data_type"]),
		})
		if diags.HasError() {
			return types.ListNull(OptionObjectType()), diags
		}
		elems = append(elems, obj)
	}
	return types.ListValue(OptionObjectType(), elems)
}

func csvFlag(item map[string]any) any {
	if v, ok := item["csv_format"]; ok {
		return v
	}
	return item["csv-format"]
}

func intValue(v any) types.Int64 {
	switch n := v.(type) {
	case float64:
		return types.Int64Value(int64(n))
	case int64:
		return types.Int64Value(n)
	case int:
		return types.Int64Value(int64(n))
	}
	return types.Int64Null()
}

func stringValue(v any) types.String {
	switch s := v.(type) {
	case nil:
		return types.StringNull()
	case string:
		return types.StringValue(s)
	default:
		return types.StringValue(fmt.Sprint(s))
	}
}

func boolValue(v any) types.Bool {
	if b, ok := v.(bool); ok {
		return types.BoolValue(b)
	}
	return types.BoolNull()
}
