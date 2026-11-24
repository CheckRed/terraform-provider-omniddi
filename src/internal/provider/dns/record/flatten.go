package record

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func flattenRecord(ctx context.Context, dto *recordDTO, prior recordModel) (recordModel, diag.Diagnostics) {
	next := prior
	next.ID = types.Int64Value(dto.ID)
	next.ZoneID = types.Int64Value(dto.ZoneID)
	next.Name = flattenName(dto.Name, prior.Name)
	next.Type = types.StringValue(strings.ToUpper(dto.Type))
	next.TTL = flattenTTL(dto)
	next.EffectiveTTL = types.Int64PointerValue(dto.EffectiveTTL)
	next.ViewID = types.Int64PointerValue(dto.ViewID)
	next.ZoneName = types.StringPointerValue(dto.ZoneName)
	next.IsAutoCreated = types.BoolValue(dto.IsAutoCreated != nil && *dto.IsAutoCreated)
	next.CreatedOn = types.StringPointerValue(dto.CreatedOn)
	next.UpdatedOn = types.StringPointerValue(dto.UpdatedOn)
	next.RecordSetOrdering = flattenOrdering(dto.RecordSetOrdering, prior.RecordSetOrdering)
	diags := flattenValueForms(ctx, &next, dto, prior)
	return next, diags
}

func flattenName(remote *string, prior framework.Hostname) framework.Hostname {
	name := normalizeName(derefString(remote))
	if known(prior) && normalizeName(prior.ValueString()) == name {
		return prior
	}
	return framework.NewHostnameValue(name)
}

func known(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func flattenTTL(dto *recordDTO) types.Int64 {
	if dto.TTL != nil {
		return types.Int64Value(*dto.TTL)
	}
	return types.Int64PointerValue(dto.EffectiveTTL)
}

func flattenOrdering(remote *string, prior types.String) types.String {
	switch {
	case remote != nil && *remote != "":
		return types.StringValue(*remote)
	case !prior.IsNull() && !prior.IsUnknown():
		return prior
	}
	return types.StringNull()
}

func resetValueForms(m *recordModel) {
	m.Values = types.ListNull(types.StringType)
	m.DisabledValues = types.SetNull(types.StringType)
	m.MX = []mxModel{}
	m.SRV = []srvModel{}
	m.CAA = []caaModel{}
	m.DataJSON = newNormalizedJSONNull()
}

func flattenValueForms(ctx context.Context, next *recordModel, dto *recordDTO, prior recordModel) diag.Diagnostics {
	resetValueForms(next)
	recordType := next.Type.ValueString()
	tokens, enabled := remoteTokens(dto)
	if isStringType(recordType) {
		return flattenStringValues(ctx, next, recordType, tokens, enabled, prior)
	}
	priorUsedJSON := !prior.DataJSON.IsNull() && !prior.DataJSON.IsUnknown()
	if !priorUsedJSON && flattenTypedBlocks(next, recordType, tokens) {
		return nil
	}
	return flattenDataJSON(next, recordType, tokens, prior)
}

func remoteTokens(dto *recordDTO) ([]any, []bool) {
	if dto.Values != nil {
		tokens := make([]any, len(dto.Values))
		enabled := make([]bool, len(dto.Values))
		for i, state := range dto.Values {
			tokens[i], enabled[i] = state.Value, state.Enabled
		}
		return tokens, enabled
	}
	tokens := unwrapTokens(dto.Data)
	enabled := make([]bool, len(tokens))
	for i := range enabled {
		enabled[i] = true
	}
	return tokens, enabled
}

func unwrapTokens(data any) []any {
	switch typed := data.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case map[string]any:
		if values, ok := typed["values"].([]any); ok {
			return values
		}
		if inner, ok := typed["data"]; ok {
			return unwrapTokens(inner)
		}
		return []any{typed}
	}
	return []any{data}
}

func tokenString(token any) string {
	switch typed := token.(type) {
	case string:
		return typed
	case float64:
		return fmt.Sprintf("%v", typed)
	}
	return fmt.Sprint(token)
}

func flattenStringValues(ctx context.Context, next *recordModel, recordType string, tokens []any, enabled []bool, prior recordModel) diag.Diagnostics {
	remote := make([]string, len(tokens))
	for i, token := range tokens {
		remote[i] = tokenString(token)
	}
	values := reconcileStrings(recordType, priorStrings(ctx, prior.Values), remote)
	var diags diag.Diagnostics
	next.Values, diags = types.ListValueFrom(ctx, types.StringType, values)
	if !supportsDisabledValues(recordType) {
		return diags
	}
	var disabled []string
	for i, value := range values {
		if i < len(enabled) && !enabled[i] {
			disabled = append(disabled, value)
		}
	}
	if len(disabled) == 0 && prior.DisabledValues.IsNull() {
		return diags
	}
	if disabled == nil {
		disabled = []string{}
	}
	set, setDiags := types.SetValueFrom(ctx, types.StringType, disabled)
	diags.Append(setDiags...)
	next.DisabledValues = set
	return diags
}

func priorStrings(ctx context.Context, list types.List) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var out []string
	if diags := list.ElementsAs(ctx, &out, false); diags.HasError() {
		return nil
	}
	return out
}

func reconcileStrings(recordType string, prior, remote []string) []string {
	if len(prior) != len(remote) {
		return remote
	}
	out := make([]string, len(remote))
	for i := range remote {
		out[i] = remote[i]
		if sameStringValue(recordType, prior[i], remote[i]) {
			out[i] = prior[i]
		}
	}
	return out
}

func sameStringValue(recordType, a, b string) bool {
	if a == b {
		return true
	}
	if _, hostname := hostnameValueTypes[recordType]; hostname {
		return framework.NormalizeFQDN(a) == framework.NormalizeFQDN(b)
	}
	if recordType == "AAAA" {
		left, errLeft := netip.ParseAddr(strings.TrimSpace(a))
		right, errRight := netip.ParseAddr(strings.TrimSpace(b))
		return errLeft == nil && errRight == nil && left == right
	}
	return false
}

func flattenTypedBlocks(next *recordModel, recordType string, tokens []any) bool {
	switch recordType {
	case typeMX:
		blocks, ok := mxFromTokens(tokens)
		next.MX = blocks
		return ok
	case typeSRV:
		blocks, ok := srvFromTokens(tokens)
		next.SRV = blocks
		return ok
	case typeCAA:
		blocks, ok := caaFromTokens(tokens)
		next.CAA = blocks
		return ok
	}
	return false
}

func intField(item map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		switch typed := item[key].(type) {
		case float64:
			if typed == math.Trunc(typed) {
				return int64(typed), true
			}
		case int64:
			return typed, true
		}
	}
	return 0, false
}

func stringField(item map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if text, ok := item[key].(string); ok {
			return text, true
		}
	}
	return "", false
}

func mxFromTokens(tokens []any) ([]mxModel, bool) {
	out := make([]mxModel, 0, len(tokens))
	for _, token := range tokens {
		item, ok := token.(map[string]any)
		preference, okPref := intField(item, "preference")
		exchange, okExch := stringField(item, "exchange")
		if !ok || !okPref || !okExch {
			return []mxModel{}, false
		}
		out = append(out, mxModel{Preference: types.Int64Value(preference), Exchange: framework.NewFQDNValue(exchange)})
	}
	return out, len(out) > 0
}

func srvFromTokens(tokens []any) ([]srvModel, bool) {
	out := make([]srvModel, 0, len(tokens))
	for _, token := range tokens {
		item, ok := token.(map[string]any)
		priority, okPrio := intField(item, "priority")
		weight, okWeight := intField(item, "weight")
		port, okPort := intField(item, "port")
		host, okHost := stringField(item, "host", "target")
		if !ok || !okPrio || !okWeight || !okPort || !okHost {
			return []srvModel{}, false
		}
		out = append(out, srvModel{
			Priority: types.Int64Value(priority), Weight: types.Int64Value(weight),
			Port: types.Int64Value(port), Host: framework.NewFQDNValue(host),
		})
	}
	return out, len(out) > 0
}

func caaFromTokens(tokens []any) ([]caaModel, bool) {
	out := make([]caaModel, 0, len(tokens))
	for _, token := range tokens {
		item, ok := token.(map[string]any)
		flag, okFlag := intField(item, "flag", "flags")
		tag, okTag := stringField(item, "tag")
		value, okValue := stringField(item, "value", "val")
		if !ok || !okFlag || !okTag || !okValue {
			return []caaModel{}, false
		}
		out = append(out, caaModel{Flag: types.Int64Value(flag), Tag: types.StringValue(tag), Value: types.StringValue(value)})
	}
	return out, len(out) > 0
}

func flattenDataJSON(next *recordModel, recordType string, tokens []any, prior recordModel) diag.Diagnostics {
	var diags diag.Diagnostics
	var payload any = tokens
	if tokens == nil {
		payload = []any{}
	}
	if singleObjectPayload(recordType, tokens, prior) {
		payload = tokens[0]
	}
	encoded, err := encodeJSON(payload)
	if err != nil {
		diags.AddError("Unable to encode record data", err.Error())
		return diags
	}
	next.DataJSON = reuseEquivalentJSON(prior.DataJSON, encoded)
	return diags
}

func singleObjectPayload(recordType string, tokens []any, prior recordModel) bool {
	if len(tokens) != 1 {
		return false
	}
	if !prior.DataJSON.IsNull() && !prior.DataJSON.IsUnknown() {
		priorValue, err := decodeJSON(prior.DataJSON.ValueString())
		_, isList := priorValue.([]any)
		return err == nil && !isList
	}
	_, objectShaped := objectShapedTypes[recordType]
	return objectShaped
}

func reuseEquivalentJSON(prior normalizedJSON, encoded string) normalizedJSON {
	fresh := newNormalizedJSON(encoded)
	if prior.IsNull() || prior.IsUnknown() {
		return fresh
	}
	if equal, _ := prior.StringSemanticEquals(context.Background(), fresh); equal {
		return prior
	}
	return fresh
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
