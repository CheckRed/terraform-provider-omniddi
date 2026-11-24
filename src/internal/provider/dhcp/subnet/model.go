package subnet

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

type model struct {
	ID                 types.Int64    `tfsdk:"id"`
	SubnetID           types.Int64    `tfsdk:"subnet_id"`
	ServerGroupID      types.Int64    `tfsdk:"server_group_id"`
	CIDR               types.String   `tfsdk:"cidr"`
	Pools              types.List     `tfsdk:"pools"`
	OptionData         types.List     `tfsdk:"option_data"`
	LeaseTime          types.Int64    `tfsdk:"lease_time"`
	LeaseTimeUnit      types.String   `tfsdk:"lease_time_unit"`
	EnableDHCPService  types.Bool     `tfsdk:"enable_dhcp_service"`
	EnableDDNS         types.Bool     `tfsdk:"enable_ddns"`
	DDNSConfigJSON     types.String   `tfsdk:"ddns_config_json"`
	VLANID             types.Int64    `tfsdk:"vlan_id"`
	ViewID             types.Int64    `tfsdk:"view_id"`
	ClientClassIDs     types.Set      `tfsdk:"client_class_ids"`
	KeaSubnetID        types.Int64    `tfsdk:"kea_subnet_id"`
	SharedNetworkID    types.Int64    `tfsdk:"shared_network_id"`
	CreatedOn          types.String   `tfsdk:"created_on"`
	UpdatedOn          types.String   `tfsdk:"updated_on"`
	ApprovalMode       types.String   `tfsdk:"approval_mode"`
	DeletionProtection types.Bool     `tfsdk:"deletion_protection"`
	ForceDestroy       types.Bool     `tfsdk:"force_destroy"`
	Timeouts           timeouts.Value `tfsdk:"timeouts"`
}

type poolModel struct {
	Pool          types.String `tfsdk:"pool"`
	Name          types.String `tfsdk:"name"`
	ClientClassID types.Int64  `tfsdk:"client_class_id"`
	OptionData    types.List   `tfsdk:"option_data"`
}

func poolAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"pool":            types.StringType,
		"name":            types.StringType,
		"client_class_id": types.Int64Type,
		"option_data":     types.ListType{ElemType: dhcpkit.OptionObjectType()},
	}
}

func poolObjectType() types.ObjectType { return types.ObjectType{AttrTypes: poolAttrTypes()} }

func stripSpaces(s string) string { return strings.Join(strings.Fields(s), "") }

func nullableString(v any) types.String {
	if s, ok := v.(string); ok && s != "" {
		return types.StringValue(s)
	}
	return types.StringNull()
}

func nullableInt(v any) types.Int64 {
	if n, ok := v.(float64); ok {
		return types.Int64Value(int64(n))
	}
	return types.Int64Null()
}

func optionMaps(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func optionsFromRemote(ctx context.Context, raw []map[string]any, prior types.List, mirror bool) (types.List, diag.Diagnostics) {
	if mirror {
		return dhcpkit.OptionsFromWire(raw, prior)
	}
	return dhcpkit.OptionsFromWireKeeping(ctx, raw, prior)
}

func poolsFromRemote(ctx context.Context, raw []map[string]any, prior types.List, mirror bool) (types.List, diag.Diagnostics) {
	known := !prior.IsNull() && !prior.IsUnknown()
	if len(raw) == 0 && !known {
		return types.ListNull(poolObjectType()), nil
	}
	var previous []poolModel
	var diags diag.Diagnostics
	if known {
		diags.Append(prior.ElementsAs(ctx, &previous, false)...)
		if diags.HasError() {
			return types.ListNull(poolObjectType()), diags
		}
	}
	elems := make([]attr.Value, 0, len(raw))
	for i, item := range raw {
		var before poolModel
		if i < len(previous) {
			before = previous[i]
		}
		pool := nullableString(item["pool"])
		if !before.Pool.IsNull() && !pool.IsNull() && stripSpaces(before.Pool.ValueString()) == stripSpaces(pool.ValueString()) {
			pool = before.Pool
		}
		options, optionDiags := optionsFromRemote(ctx, optionMaps(item["option_data"]), before.OptionData, mirror)
		diags.Append(optionDiags...)
		obj, objDiags := types.ObjectValue(poolAttrTypes(), map[string]attr.Value{
			"pool":            pool,
			"name":            nullableString(item["name"]),
			"client_class_id": nullableInt(item["client_class_id"]),
			"option_data":     options,
		})
		diags.Append(objDiags...)
		if diags.HasError() {
			return types.ListNull(poolObjectType()), diags
		}
		elems = append(elems, obj)
	}
	list, listDiags := types.ListValue(poolObjectType(), elems)
	diags.Append(listDiags...)
	return list, diags
}

func poolsToWire(ctx context.Context, list types.List) ([]map[string]any, diag.Diagnostics) {
	out := []map[string]any{}
	if list.IsNull() || list.IsUnknown() {
		return out, nil
	}
	var pools []poolModel
	diags := list.ElementsAs(ctx, &pools, false)
	if diags.HasError() {
		return nil, diags
	}
	for _, p := range pools {
		entry := map[string]any{"pool": strings.TrimSpace(p.Pool.ValueString())}
		if !p.Name.IsNull() && !p.Name.IsUnknown() {
			entry["name"] = p.Name.ValueString()
		}
		if !p.ClientClassID.IsNull() && !p.ClientClassID.IsUnknown() {
			entry["client_class_id"] = p.ClientClassID.ValueInt64()
		}
		options, optionDiags := dhcpkit.OptionsToWire(ctx, p.OptionData)
		diags.Append(optionDiags...)
		if len(options) > 0 {
			entry["option_data"] = options
		}
		out = append(out, entry)
	}
	return out, diags
}

func optionsToWire(ctx context.Context, list types.List) ([]map[string]any, diag.Diagnostics) {
	options, diags := dhcpkit.OptionsToWire(ctx, list)
	if options == nil {
		options = []map[string]any{}
	}
	return options, diags
}

func idSet(ids []idRef, prior types.Set) types.Set {
	if len(ids) == 0 && prior.IsNull() {
		return types.SetNull(types.Int64Type)
	}
	elems := make([]attr.Value, 0, len(ids))
	for _, ref := range ids {
		elems = append(elems, types.Int64Value(ref.ID))
	}
	return types.SetValueMust(types.Int64Type, elems)
}

func idsFromSet(ctx context.Context, set types.Set) ([]int64, diag.Diagnostics) {
	ids := []int64{}
	if set.IsNull() || set.IsUnknown() {
		return ids, nil
	}
	diags := set.ElementsAs(ctx, &ids, false)
	return ids, diags
}

func canonicalJSON(raw string) (any, bool) {
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, false
	}
	return out, true
}

func jsonContains(remote, wanted any) bool {
	switch w := wanted.(type) {
	case map[string]any:
		r, ok := remote.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			if !jsonContains(r[key], value) {
				return false
			}
		}
		return true
	case []any:
		r, ok := remote.([]any)
		if !ok || len(r) != len(w) {
			return false
		}
		for i := range w {
			if !jsonContains(r[i], w[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(remote, wanted)
}

func ddnsFromRemote(remote map[string]any, prior types.String) types.String {
	if remote == nil {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		if wanted, ok := canonicalJSON(prior.ValueString()); ok && jsonContains(remote, wanted) {
			return prior
		}
	}
	encoded, err := json.Marshal(remote)
	if err != nil {
		return types.StringNull()
	}
	return types.StringValue(string(encoded))
}

func sameJSON(a, b types.String) bool {
	left, okLeft := canonicalJSON(a.ValueString())
	right, okRight := canonicalJSON(b.ValueString())
	return okLeft && okRight && reflect.DeepEqual(left, right)
}

func (m *model) applyRemote(ctx context.Context, d *dto, mirror bool) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.Int64Value(d.ID)
	m.SubnetID = types.Int64Value(d.ID)
	m.ServerGroupID = dhcpkit.OptionalInt64(d.ServerGroupID)
	m.CIDR = types.StringNull()
	if cidr, ok := d.cidr(); ok {
		m.CIDR = types.StringValue(cidr)
	}
	pools, poolDiags := poolsFromRemote(ctx, d.pools(), m.Pools, mirror)
	diags.Append(poolDiags...)
	m.Pools = pools
	options, optionDiags := optionsFromRemote(ctx, d.options(), m.OptionData, mirror)
	diags.Append(optionDiags...)
	m.OptionData = options
	m.LeaseTime = dhcpkit.OptionalInt64(d.configInt("lease_time"))
	m.LeaseTimeUnit = types.StringPointerValue(d.configString("lease_time_unit"))
	m.EnableDHCPService = types.BoolPointerValue(d.configBool("enable_dhcp_service"))
	m.EnableDDNS = types.BoolPointerValue(d.configBool("enable_ddns"))
	m.DDNSConfigJSON = ddnsFromRemote(d.ddnsConfig(), m.DDNSConfigJSON)
	m.VLANID = types.Int64Null()
	if len(d.VLANs) > 0 {
		m.VLANID = types.Int64Value(d.VLANs[0].ID)
	}
	m.ViewID = dhcpkit.OptionalInt64(d.ViewID)
	m.ClientClassIDs = idSet(d.ClientClasses, m.ClientClassIDs)
	m.KeaSubnetID = dhcpkit.OptionalInt64(d.KeaSubnetID)
	m.SharedNetworkID = dhcpkit.OptionalInt64(d.SharedNetworkID)
	m.CreatedOn = types.StringPointerValue(d.CreatedOn)
	m.UpdatedOn = types.StringPointerValue(d.UpdatedOn)
	return diags
}

func configured[T interface {
	IsNull() bool
	IsUnknown() bool
}](v T) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func settingsBody(ctx context.Context, plan, prior model) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := map[string]any{}
	if !plan.Pools.Equal(prior.Pools) {
		pools, d := poolsToWire(ctx, plan.Pools)
		diags.Append(d...)
		body["pools"] = pools
	}
	if !plan.OptionData.Equal(prior.OptionData) {
		options, d := optionsToWire(ctx, plan.OptionData)
		diags.Append(d...)
		body["option_data"] = options
	}
	if !plan.ClientClassIDs.Equal(prior.ClientClassIDs) {
		ids, d := idsFromSet(ctx, plan.ClientClassIDs)
		diags.Append(d...)
		body["client_class_ids"] = ids
	}
	addLeaseTime(body, plan, prior)
	if configured(plan.EnableDHCPService) && !plan.EnableDHCPService.Equal(prior.EnableDHCPService) {
		body["enable_dhcp_service"] = plan.EnableDHCPService.ValueBool()
	}
	if configured(plan.EnableDDNS) && !plan.EnableDDNS.Equal(prior.EnableDDNS) {
		body["enable_ddns"] = plan.EnableDDNS.ValueBool()
	}
	if configured(plan.DDNSConfigJSON) && (!configured(prior.DDNSConfigJSON) || !sameJSON(plan.DDNSConfigJSON, prior.DDNSConfigJSON)) {
		if decoded, ok := canonicalJSON(plan.DDNSConfigJSON.ValueString()); ok {
			body["ddns_config"] = decoded
		}
	}
	if configured(plan.VLANID) && !plan.VLANID.Equal(prior.VLANID) {
		body["vlan_id"] = plan.VLANID.ValueInt64()
	}
	return body, diags
}

func addLeaseTime(body map[string]any, plan, prior model) {
	if !configured(plan.LeaseTime) {
		return
	}
	unitChanged := configured(plan.LeaseTimeUnit) && !plan.LeaseTimeUnit.Equal(prior.LeaseTimeUnit)
	if plan.LeaseTime.Equal(prior.LeaseTime) && !unitChanged {
		return
	}
	body["lease_time"] = plan.LeaseTime.ValueInt64()
	if configured(plan.LeaseTimeUnit) {
		body["lease_time_unit"] = plan.LeaseTimeUnit.ValueString()
	}
}
