package record

import (
	"context"
	"reflect"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
)

func expandData(ctx context.Context, m recordModel) (any, diag.Diagnostics) {
	var diags diag.Diagnostics
	switch {
	case len(m.MX) > 0:
		return expandMX(m.MX), nil
	case len(m.SRV) > 0:
		return expandSRV(m.SRV), nil
	case len(m.CAA) > 0:
		return expandCAA(m.CAA), nil
	case !m.DataJSON.IsNull() && !m.DataJSON.IsUnknown():
		parsed, err := decodeJSONForRequest(m.DataJSON.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("data_json"), "Invalid data_json", err.Error())
			return nil, diags
		}
		return parsed, nil
	case !m.Values.IsNull() && !m.Values.IsUnknown():
		var values []string
		diags.Append(m.Values.ElementsAs(ctx, &values, false)...)
		return expandValues(m.Type.ValueString(), values), diags
	}
	diags.AddError("Missing record value", "The record has no value form set.")
	return nil, diags
}

func expandValues(recordType string, values []string) any {
	if len(values) == 1 || isSingleValue(recordType) {
		return values[0]
	}
	return values
}

func expandMX(blocks []mxModel) []map[string]any {
	out := make([]map[string]any, len(blocks))
	for i, block := range blocks {
		out[i] = map[string]any{"preference": block.Preference.ValueInt64(), "exchange": block.Exchange.ValueString()}
	}
	return out
}

func expandSRV(blocks []srvModel) []map[string]any {
	out := make([]map[string]any, len(blocks))
	for i, block := range blocks {
		out[i] = map[string]any{
			"priority": block.Priority.ValueInt64(), "weight": block.Weight.ValueInt64(),
			"port": block.Port.ValueInt64(), "host": block.Host.ValueString(),
		}
	}
	return out
}

func expandCAA(blocks []caaModel) []map[string]any {
	out := make([]map[string]any, len(blocks))
	for i, block := range blocks {
		out[i] = map[string]any{"flag": block.Flag.ValueInt64(), "tag": block.Tag.ValueString(), "value": block.Value.ValueString()}
	}
	return out
}

func expandEnabledFlags(ctx context.Context, m recordModel, data any) ([]bool, diag.Diagnostics) {
	recordType := m.Type.ValueString()
	if !supportsDisabledValues(recordType) {
		return nil, nil
	}
	count := valueCount(data)
	flags := make([]bool, count)
	for i := range flags {
		flags[i] = true
	}
	if m.DisabledValues.IsNull() || m.DisabledValues.IsUnknown() || m.Values.IsNull() || m.Values.IsUnknown() {
		return flags, nil
	}
	var values, disabled []string
	var diags diag.Diagnostics
	diags.Append(m.Values.ElementsAs(ctx, &values, false)...)
	diags.Append(m.DisabledValues.ElementsAs(ctx, &disabled, false)...)
	for i, value := range values {
		if i < len(flags) && slices.Contains(disabled, value) {
			flags[i] = false
		}
	}
	return flags, diags
}

func valueCount(data any) int {
	switch typed := data.(type) {
	case []string:
		return len(typed)
	case []map[string]any:
		return len(typed)
	case []any:
		return len(typed)
	}
	return 1
}

func hasDisabled(flags []bool) bool { return slices.Contains(flags, false) }

func dataChanged(ctx context.Context, plan, state recordModel) (bool, diag.Diagnostics) {
	planData, diags := expandData(ctx, plan)
	stateData, stateDiags := expandData(ctx, state)
	diags.Append(stateDiags...)
	if diags.HasError() {
		return true, diags
	}
	planFlags, flagDiags := expandEnabledFlags(ctx, plan, planData)
	diags.Append(flagDiags...)
	stateFlags, flagDiags := expandEnabledFlags(ctx, state, stateData)
	diags.Append(flagDiags...)
	return !sameJSONShape(planData, stateData) || !slices.Equal(planFlags, stateFlags), diags
}

func sameJSONShape(a, b any) bool {
	left, errLeft := encodeJSON(a)
	right, errRight := encodeJSON(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	leftValue, _ := decodeJSON(left)
	rightValue, _ := decodeJSON(right)
	return reflect.DeepEqual(leftValue, rightValue)
}

func createBody(ctx context.Context, plan recordModel) (map[string]any, diag.Diagnostics) {
	data, diags := expandData(ctx, plan)
	if diags.HasError() {
		return nil, diags
	}
	body := map[string]any{
		"zone_id": plan.ZoneID.ValueInt64(),
		"name":    normalizeName(plan.Name.ValueString()),
		"type":    plan.Type.ValueString(),
		"data":    data,
	}
	if !plan.TTL.IsNull() && !plan.TTL.IsUnknown() {
		body["ttl"] = plan.TTL.ValueInt64()
	}
	if configured(plan.RecordSetOrdering.IsNull(), plan.RecordSetOrdering.IsUnknown()) {
		body["record_set_ordering"] = plan.RecordSetOrdering.ValueString()
	}
	flags, flagDiags := expandEnabledFlags(ctx, plan, data)
	diags.Append(flagDiags...)
	if hasDisabled(flags) {
		body["enabled_flags"] = flags
	}
	return body, diags
}

func updateBody(ctx context.Context, plan, state recordModel) (map[string]any, diag.Diagnostics) {
	body := map[string]any{}
	if normalizeName(plan.Name.ValueString()) != normalizeName(state.Name.ValueString()) {
		body["name"] = normalizeName(plan.Name.ValueString())
	}
	if !plan.TTL.IsUnknown() && !plan.TTL.IsNull() && !plan.TTL.Equal(state.TTL) {
		body["ttl"] = plan.TTL.ValueInt64()
	}
	if configured(plan.RecordSetOrdering.IsNull(), plan.RecordSetOrdering.IsUnknown()) && !plan.RecordSetOrdering.Equal(state.RecordSetOrdering) {
		body["record_set_ordering"] = plan.RecordSetOrdering.ValueString()
	}
	changed, diags := dataChanged(ctx, plan, state)
	if diags.HasError() || !changed {
		return body, diags
	}
	data, dataDiags := expandData(ctx, plan)
	diags.Append(dataDiags...)
	flags, flagDiags := expandEnabledFlags(ctx, plan, data)
	diags.Append(flagDiags...)
	body["data"] = data
	if flags != nil {
		body["enabled_flags"] = flags
	}
	return body, diags
}

func configured(isNull, isUnknown bool) bool { return !isNull && !isUnknown }
