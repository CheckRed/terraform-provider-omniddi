package dhcpkit

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func OptionsFromWireKeeping(ctx context.Context, raw []map[string]any, prior types.List) (types.List, diag.Diagnostics) {
	known := !prior.IsNull() && !prior.IsUnknown()
	if len(raw) == 0 && !known {
		return types.ListNull(OptionObjectType()), nil
	}
	var previous []OptionModel
	var diags diag.Diagnostics
	if known {
		diags.Append(prior.ElementsAs(ctx, &previous, false)...)
		if diags.HasError() {
			return types.ListNull(OptionObjectType()), diags
		}
	}
	elems := make([]attr.Value, 0, len(raw))
	for _, item := range raw {
		code := intValue(item["code"])
		kept := takeOption(&previous, code)
		obj, objDiags := types.ObjectValue(optionAttrTypes, map[string]attr.Value{
			"code":       code,
			"data":       stringValue(item["data"]),
			"name":       kept.Name,
			"space":      kept.Space,
			"csv_format": kept.CSVFormat,
			"data_type":  kept.DataType,
		})
		diags.Append(objDiags...)
		if objDiags.HasError() {
			return types.ListNull(OptionObjectType()), diags
		}
		elems = append(elems, obj)
	}
	list, listDiags := types.ListValue(OptionObjectType(), elems)
	diags.Append(listDiags...)
	return list, diags
}

func takeOption(pool *[]OptionModel, code types.Int64) OptionModel {
	for i, candidate := range *pool {
		if candidate.Code.Equal(code) {
			*pool = append((*pool)[:i:i], (*pool)[i+1:]...)
			return candidate
		}
	}
	return OptionModel{}
}
