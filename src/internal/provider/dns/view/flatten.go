package view

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

func serverGroupSet(ctx context.Context, ids []int64) (types.Set, diag.Diagnostics) {
	if ids == nil {
		ids = []int64{}
	}
	return types.SetValueFrom(ctx, types.Int64Type, ids)
}

func forwarderList(ctx context.Context, dto []forwarderDTO, prior types.List) (types.List, diag.Diagnostics) {
	if dto == nil {
		return types.ListNull(forwarderType), nil
	}
	items := make([]forwarderModel, 0, len(dto))
	for _, f := range dto {
		items = append(items, forwarderModel{IP: types.StringValue(f.IP), Port: types.Int64Value(f.Port)})
	}
	fresh, diags := types.ListValueFrom(ctx, forwarderType, items)
	if diags.HasError() || prior.IsNull() || prior.IsUnknown() {
		return fresh, diags
	}
	var previous []forwarderModel
	diags.Append(prior.ElementsAs(ctx, &previous, false)...)
	if sameForwarders(previous, items) {
		return prior, diags
	}
	return fresh, diags
}

func sameForwarders(a, b []forwarderModel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if dnskit.CanonicalIP(a[i].IP.ValueString()) != dnskit.CanonicalIP(b[i].IP.ValueString()) || a[i].Port.ValueInt64() != b[i].Port.ValueInt64() {
			return false
		}
	}
	return true
}
