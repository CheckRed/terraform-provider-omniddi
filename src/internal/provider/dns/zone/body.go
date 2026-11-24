package zone

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

func buildBody(ctx context.Context, plan zoneModel, prior *zoneModel) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	update := prior != nil
	if !update {
		prior = &zoneModel{}
	}
	b := dnskit.NewBodyBuilder(update)
	str := func(v types.String) func() any { return func() any { return v.ValueString() } }
	num := func(v types.Int64) func() any { return func() any { return v.ValueInt64() } }
	flag := func(v types.Bool) func() any { return func() any { return v.ValueBool() } }

	if !update {
		b.Body["view_id"] = plan.ViewID.ValueInt64()
		if known(plan.Name) {
			b.Body["name"] = plan.Name.ValueString()
		}
		b.Body["zone_type"] = plan.ZoneType.ValueString()
		b.Body["network_scope"] = plan.NetworkScope.ValueString()
		b.PutIfSet("protocol", plan.Protocol, prior.Protocol, str(plan.Protocol))
		b.PutIfSet("cidr", plan.CIDR, prior.CIDR, str(plan.CIDR))
	}
	b.PutIfSet("ttl", plan.TTL, prior.TTL, num(plan.TTL))
	b.PutIfSet("refresh", plan.Refresh, prior.Refresh, num(plan.Refresh))
	b.PutIfSet("retry", plan.Retry, prior.Retry, num(plan.Retry))
	b.PutIfSet("expiry", plan.Expiry, prior.Expiry, num(plan.Expiry))
	b.PutIfSet("minimum", plan.Minimum, prior.Minimum, num(plan.Minimum))
	b.PutIfSet("email", plan.Email, prior.Email, str(plan.Email))
	b.PutIfSet("primary_name_server", plan.PrimaryNameServer, prior.PrimaryNameServer, str(plan.PrimaryNameServer))
	b.PutIfSet("policy", plan.Policy, prior.Policy, str(plan.Policy))
	b.PutIfSet("enable_dynamic_dns", plan.EnableDynamicDNS, prior.EnableDynamicDNS, flag(plan.EnableDynamicDNS))
	b.PutIfSet("enable_dnssec", plan.EnableDNSSEC, prior.EnableDNSSEC, flag(plan.EnableDNSSEC))
	b.PutIfSet("auto_create_ptr_records", plan.AutoCreatePTRRecords, prior.AutoCreatePTRRecords, flag(plan.AutoCreatePTRRecords))
	b.PutIfSet("enable_zone_transfer", plan.EnableZoneTransfer, prior.EnableZoneTransfer, flag(plan.EnableZoneTransfer))
	b.PutIfSet("secure_sync", plan.SecureSync, prior.SecureSync, flag(plan.SecureSync))
	b.Put("zone_acl_id", plan.ZoneACLID, prior.ZoneACLID, num(plan.ZoneACLID))
	b.Put("zone_transfer_tsig_key_id", plan.ZoneTransferTsigKeyID, prior.ZoneTransferTsigKeyID, num(plan.ZoneTransferTsigKeyID))
	b.Put("dynamic_dns_tsig_key_id", plan.DynamicDNSTsigKeyID, prior.DynamicDNSTsigKeyID, num(plan.DynamicDNSTsigKeyID))
	b.Put("secondary_sync_tsig_key_id", plan.SecondarySyncTsigKeyID, prior.SecondarySyncTsigKeyID, num(plan.SecondarySyncTsigKeyID))

	servers, d := primaryServersBody(ctx, plan.PrimaryServers)
	diags.Append(d...)
	b.PutIfSet("primary_servers", plan.PrimaryServers, prior.PrimaryServers, func() any { return servers })

	var nameservers []string
	if known(plan.Nameservers) {
		diags.Append(plan.Nameservers.ElementsAs(ctx, &nameservers, false)...)
	}
	b.PutIfSet("nameservers", plan.Nameservers, prior.Nameservers, func() any { return nameservers })

	var units []int64
	if known(plan.BusinessUnitIDs) {
		diags.Append(plan.BusinessUnitIDs.ElementsAs(ctx, &units, false)...)
	}
	b.PutIfSet("business_unit_ids", plan.BusinessUnitIDs, prior.BusinessUnitIDs, func() any { return units })
	return b.Body, diags
}

func primaryServersBody(ctx context.Context, list types.List) ([]map[string]any, diag.Diagnostics) {
	out := []map[string]any{}
	if !known(list) {
		return out, nil
	}
	var items []primaryServerModel
	diags := list.ElementsAs(ctx, &items, false)
	for _, item := range items {
		entry := map[string]any{"ip": item.IP.ValueString(), "port": item.Port.ValueInt64()}
		if known(item.TsigKeyID) {
			entry["secure_sync"] = true
			entry["tsig_key_id"] = item.TsigKeyID.ValueInt64()
		}
		out = append(out, entry)
	}
	return out, diags
}

func expectedName(m zoneModel) string {
	if known(m.Name) {
		return framework.NormalizeFQDN(m.Name.ValueString())
	}
	if known(m.Protocol) && known(m.CIDR) {
		name, _ := deriveReverseName(m.Protocol.ValueString(), m.CIDR.ValueString())
		return name
	}
	return ""
}
