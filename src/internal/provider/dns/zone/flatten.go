package zone

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

func canonicalChoice(remote *string, choices []string, current types.String) types.String {
	if remote == nil {
		return keepString(current, nil)
	}
	member := (*remote)[strings.LastIndex(*remote, ".")+1:]
	for _, choice := range choices {
		if strings.EqualFold(choice, *remote) || strings.EqualFold(choice, member) {
			return types.StringValue(choice)
		}
	}
	return types.StringValue(*remote)
}

func sameCIDR(a, b string) bool {
	pa, errA := netip.ParsePrefix(a)
	pb, errB := netip.ParsePrefix(b)
	return errA == nil && errB == nil && pa.Masked() == pb.Masked()
}

func cidrValue(current types.String, remote *string) types.String {
	if remote == nil {
		return keepString(current, nil)
	}
	if known(current) && sameCIDR(current.ValueString(), *remote) {
		return current
	}
	return types.StringValue(*remote)
}

func protocolValue(current types.String, remote *string) types.String {
	if remote != nil && known(current) && strings.EqualFold(current.ValueString(), *remote) {
		return current
	}
	return canonicalChoice(remote, []string{"IPv4", "IPv6"}, current)
}

func sameAddress(a, b string) bool {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return pa.Unmap() == pb.Unmap()
}

func primaryServerList(ctx context.Context, remote []primaryServerRemote, syncKey *int64, prior types.List) (types.List, diag.Diagnostics) {
	if remote == nil {
		return types.ListNull(primaryServerType), nil
	}
	var previous []primaryServerModel
	var diags diag.Diagnostics
	if known(prior) {
		diags.Append(prior.ElementsAs(ctx, &previous, false)...)
	}
	items := make([]primaryServerModel, 0, len(remote))
	for _, server := range remote {
		item := primaryServerModel{IP: types.StringValue(server.IP), Port: types.Int64Value(server.Port), TsigKeyID: types.Int64Null()}
		for _, old := range previous {
			if sameAddress(old.IP.ValueString(), server.IP) && old.Port.ValueInt64() == server.Port {
				item.IP, item.TsigKeyID = old.IP, old.TsigKeyID
			}
		}
		if item.TsigKeyID.IsNull() && server.Secure && syncKey != nil {
			item.TsigKeyID = types.Int64Value(*syncKey)
		}
		items = append(items, item)
	}
	list, d := types.ListValueFrom(ctx, primaryServerType, items)
	return list, append(diags, d...)
}

func nameserverList(ctx context.Context, remote []string, prior types.List) (types.List, diag.Diagnostics) {
	if remote == nil {
		return types.ListNull(types.StringType), nil
	}
	if known(prior) {
		var previous []string
		if diags := prior.ElementsAs(ctx, &previous, false); !diags.HasError() &&
			slices.EqualFunc(previous, remote, sameAddress) {
			return prior, nil
		}
	}
	return types.ListValueFrom(ctx, types.StringType, remote)
}

func businessUnitList(ctx context.Context, remote []int64, prior types.List) (types.List, diag.Diagnostics) {
	if remote == nil {
		return types.ListNull(types.Int64Type), nil
	}
	if known(prior) {
		var previous []int64
		if diags := prior.ElementsAs(ctx, &previous, false); !diags.HasError() && len(previous) > 0 &&
			len(previous) == len(remote) && previous[0] == remote[0] {
			a, b := slices.Clone(previous), slices.Clone(remote)
			slices.Sort(a)
			slices.Sort(b)
			if slices.Equal(a, b) {
				return prior, nil
			}
		}
	}
	return types.ListValueFrom(ctx, types.Int64Type, remote)
}

func (m *zoneModel) readAll(ctx context.Context, dto *zoneDTO) diag.Diagnostics {
	var diags diag.Diagnostics
	r := parseZone(dto)
	m.ID = types.Int64Value(dto.ID)
	if dto.ViewID != nil {
		m.ViewID = types.Int64Value(*dto.ViewID)
	}
	if dto.Name != "" {
		m.Name = framework.NewFQDNValue(dto.Name)
	}
	m.ZoneType = canonicalChoice(dto.ZoneType, zoneTypes, m.ZoneType)
	m.NetworkScope = canonicalChoice(dto.NetworkScope, networkScopes, m.NetworkScope)
	m.Protocol = protocolValue(m.Protocol, r.Protocol)
	m.CIDR = cidrValue(m.CIDR, r.CIDR)
	m.TTL, m.Refresh, m.Retry = keepInt64(m.TTL, r.TTL), keepInt64(m.Refresh, r.Refresh), keepInt64(m.Retry, r.Retry)
	m.Expiry, m.Minimum = keepInt64(m.Expiry, r.Expiry), keepInt64(m.Minimum, r.Minimum)
	m.Email = keepString(m.Email, r.Email)
	m.PrimaryNameServer = keepString(m.PrimaryNameServer, r.PrimaryNameServer)
	m.Policy = keepString(m.Policy, r.Policy)
	m.EnableDynamicDNS = keepBool(m.EnableDynamicDNS, r.EnableDynamicDNS)
	m.EnableDNSSEC = keepBool(m.EnableDNSSEC, r.EnableDNSSEC)
	m.AutoCreatePTRRecords = keepBool(m.AutoCreatePTRRecords, r.AutoCreatePTRRecords)
	m.EnableZoneTransfer = keepBool(m.EnableZoneTransfer, r.EnableZoneTransfer)
	m.SecureSync = keepBool(m.SecureSync, r.SecureSync)
	m.ZoneTransferTsigKeyID = dnskit.Int64Value(dto.ZoneTransferTsigKeyID)
	m.DynamicDNSTsigKeyID = dnskit.Int64Value(dto.DynamicDNSTsigKeyID)
	m.SecondarySyncTsigKeyID = dnskit.Int64Value(dto.SecondarySyncTsigKeyID)

	var d diag.Diagnostics
	m.PrimaryServers, d = primaryServerList(ctx, r.PrimaryServers, dto.SecondarySyncTsigKeyID, m.PrimaryServers)
	diags.Append(d...)
	m.Nameservers, d = nameserverList(ctx, r.Nameservers, m.Nameservers)
	diags.Append(d...)
	m.BusinessUnitIDs, d = businessUnitList(ctx, r.BusinessUnitIDs, m.BusinessUnitIDs)
	diags.Append(d...)
	m.fillVolatile(dto, r)
	return diags
}

func (m *zoneModel) fillVolatile(dto *zoneDTO, r zoneRemote) {
	m.SOASerial = dnskit.Int64Value(r.SOASerial)
	m.RecordsCount = dnskit.Int64Value(dto.RecordsCount)
	m.ViewName = dnskit.StringPointer(dto.ViewName)
	m.CreatedOn = dnskit.StringPointer(dto.CreatedOn)
	m.UpdatedOn = dnskit.StringPointer(dto.UpdatedOn)
}

func (m *zoneModel) fillComputed(ctx context.Context, dto *zoneDTO) diag.Diagnostics {
	fresh := *m
	diags := fresh.readAll(ctx, dto)
	if m.Name.IsNull() || m.Name.IsUnknown() {
		m.Name = fresh.Name
	}
	for _, pair := range []struct{ dst, src *types.Int64 }{
		{&m.TTL, &fresh.TTL}, {&m.Refresh, &fresh.Refresh}, {&m.Retry, &fresh.Retry},
		{&m.Expiry, &fresh.Expiry}, {&m.Minimum, &fresh.Minimum},
	} {
		if pair.dst.IsNull() {
			*pair.dst = *pair.src
		}
	}
	for _, pair := range []struct{ dst, src *types.String }{
		{&m.Email, &fresh.Email}, {&m.PrimaryNameServer, &fresh.PrimaryNameServer}, {&m.Policy, &fresh.Policy},
	} {
		if pair.dst.IsNull() {
			*pair.dst = *pair.src
		}
	}
	for _, pair := range []struct{ dst, src *types.Bool }{
		{&m.EnableDynamicDNS, &fresh.EnableDynamicDNS}, {&m.EnableDNSSEC, &fresh.EnableDNSSEC},
		{&m.AutoCreatePTRRecords, &fresh.AutoCreatePTRRecords}, {&m.EnableZoneTransfer, &fresh.EnableZoneTransfer},
		{&m.SecureSync, &fresh.SecureSync},
	} {
		if pair.dst.IsNull() {
			*pair.dst = *pair.src
		}
	}
	if m.SecondarySyncTsigKeyID.IsNull() {
		m.SecondarySyncTsigKeyID = fresh.SecondarySyncTsigKeyID
	}
	if m.BusinessUnitIDs.IsNull() {
		m.BusinessUnitIDs = fresh.BusinessUnitIDs
	}
	m.SOASerial, m.RecordsCount, m.ViewName = fresh.SOASerial, fresh.RecordsCount, fresh.ViewName
	m.CreatedOn, m.UpdatedOn = fresh.CreatedOn, fresh.UpdatedOn
	return diags
}
