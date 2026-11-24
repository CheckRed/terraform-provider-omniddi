package zone

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

func ptr[T any](v T) *T { return &v }

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, zoneByIDQuery, zonesQuery, viewsByNameQuery, userRecordsQuery)
}

func TestDeriveReverseName(t *testing.T) {
	cases := []struct{ protocol, cidr, want string }{
		{"IPv4", "192.0.2.0/24", "2.0.192.in-addr.arpa"},
		{"ipv4", "198.51.0.0/16", "51.198.in-addr.arpa"},
		{"IPv4", "10.0.0.0/8", "10.in-addr.arpa"},
		{"IPv6", "2001:db8::/32", "8.b.d.0.1.0.0.2.ip6.arpa"},
	}
	for _, c := range cases {
		got, ok := deriveReverseName(c.protocol, c.cidr)
		if !ok || got != c.want {
			t.Errorf("deriveReverseName(%s, %s) = %q, %v; want %q", c.protocol, c.cidr, got, ok, c.want)
		}
	}
	for _, c := range []struct{ protocol, cidr string }{{"IPv4", "192.0.2.0/20"}, {"IPv4", "garbage"}, {"IPv6", "192.0.2.0/24"}, {"IPv4", "192.0.2.1/32"}} {
		if name, ok := deriveReverseName(c.protocol, c.cidr); ok {
			t.Errorf("deriveReverseName(%s, %s) = %q, want failure", c.protocol, c.cidr, name)
		}
	}
}

func TestValidateZoneName(t *testing.T) {
	for _, ok := range []string{"corp.example.com", "Corp.Example.com.", "a-b.example.com", "2.0.192.in-addr.arpa"} {
		if err := validateZoneName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "bad_name.example.com", "-a.example.com", "a..example.com", "a b.example.com"} {
		if validateZoneName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	long := ""
	for range 30 {
		long += "abcdefgh."
	}
	if validateZoneName(long) == nil {
		t.Error("an over-long name must be rejected")
	}
}

func TestReversePrefixRules(t *testing.T) {
	for _, ok := range []string{"10.0.0.0/8", "192.0.2.0/24", "192.0.2.1/32", "2001:db8::/32"} {
		if _, err := reversePrefix(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"10.0.0.0/4", "10.0.0.0/20", "2001:db8::/33", "x"} {
		if _, err := reversePrefix(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestParseZoneReadsConfigAndSOA(t *testing.T) {
	raw := `{
	  "id": 4, "name": "corp.example.com", "viewId": 1,
	  "configJson": {
	    "ttl": 100, "email": "h@example.com", "policy": "first", "protocol": "IPv4", "cidr": "192.0.2.0/24",
	    "dynamic_dns": {"enable": true}, "dnssec": {"enable": false}, "enable_zone_transfer": true, "auto_create_ptr_records": false,
	    "primary_servers": ["198.51.100.1", {"ip": "198.51.100.2", "port": 5353, "secure_sync": true}, {"serverName": "198.51.100.3"}, 7],
	    "nameservers": [{"ip": "198.51.100.9", "port": 53}, "198.51.100.8"]
	  },
	  "soa": {"ttl": 3600, "nttl": "300", "serial": 7, "refresh": 900, "retry": 60, "expire": "604800", "primaryNameServer": "198.51.100.7"},
	  "businessUnits": [{"id": 3}, {"id": 2}, {"id": 5}], "primaryBusinessUnit": {"id": 2}
	}`
	var dto zoneDTO
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal(err)
	}
	r := parseZone(&dto)
	if *r.TTL != 3600 || *r.Minimum != 300 || *r.Expiry != 604800 || *r.Refresh != 900 || *r.SOASerial != 7 {
		t.Errorf("SOA values must win over config: %+v", r)
	}
	if *r.Email != "h@example.com" || *r.PrimaryNameServer != "198.51.100.7" || *r.Policy != "first" || *r.CIDR != "192.0.2.0/24" {
		t.Errorf("strings: %+v", r)
	}
	if !*r.EnableDynamicDNS || *r.EnableDNSSEC || !*r.EnableZoneTransfer || *r.AutoCreatePTRRecords || !*r.SecureSync {
		t.Errorf("flags: dyn=%v dnssec=%v zt=%v ptr=%v sync=%v", *r.EnableDynamicDNS, *r.EnableDNSSEC, *r.EnableZoneTransfer, *r.AutoCreatePTRRecords, *r.SecureSync)
	}
	if len(r.PrimaryServers) != 3 || r.PrimaryServers[0].Port != 53 || r.PrimaryServers[1].Port != 5353 || !r.PrimaryServers[1].Secure || r.PrimaryServers[2].IP != "198.51.100.3" {
		t.Errorf("primary servers: %+v", r.PrimaryServers)
	}
	if len(r.Nameservers) != 2 || r.Nameservers[1] != "198.51.100.8" {
		t.Errorf("nameservers: %v", r.Nameservers)
	}
	if got := r.BusinessUnitIDs; len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 5 {
		t.Errorf("the primary business unit must come first: %v", got)
	}
}

func TestParseZoneFallsBackToConfigAndLegacyFlags(t *testing.T) {
	dto := &zoneDTO{ConfigJSON: map[string]any{"ttl": float64(60), "enable_dnssec": true, "enable_dynamic_dns": false, "dnssec_x": 1}}
	r := parseZone(dto)
	if *r.TTL != 60 || !*r.EnableDNSSEC || *r.EnableDynamicDNS || r.Refresh != nil || r.PrimaryServers != nil || r.Nameservers != nil || r.SecureSync != nil {
		t.Errorf("%+v", r)
	}
	if got := parseZone(&zoneDTO{}); got.TTL != nil || got.BusinessUnitIDs == nil && false {
		t.Errorf("an empty zone must parse to empty values: %+v", got)
	}
}

func TestFlexIntAcceptsNumbersAndStrings(t *testing.T) {
	var soa soaDTO
	if err := json.Unmarshal([]byte(`{"expire": "12", "ttl": 5, "serial": 3.0, "retry": null, "nttl": ""}`), &soa); err != nil {
		t.Fatal(err)
	}
	if *soa.Expire.int64() != 12 || *soa.TTL.int64() != 5 || *soa.Serial.int64() != 3 || soa.Retry.int64() != nil {
		t.Errorf("%+v", soa)
	}
	if err := json.Unmarshal([]byte(`{"expire": "abc"}`), &soa); err == nil {
		t.Error("a non-numeric value must fail")
	}
	var empty *flexInt
	if empty.int64() != nil {
		t.Error("nil must stay nil")
	}
}

func baseModel() zoneModel {
	ctx := context.Background()
	_ = ctx
	return zoneModel{
		ViewID: types.Int64Value(1), Name: framework.NewFQDNValue("corp.example.com"), ZoneType: types.StringValue("Primary"), NetworkScope: types.StringValue("Internal"),
		Protocol: types.StringNull(), CIDR: types.StringNull(),
		TTL: types.Int64Unknown(), Refresh: types.Int64Unknown(), Retry: types.Int64Unknown(), Expiry: types.Int64Unknown(), Minimum: types.Int64Unknown(),
		Email: types.StringUnknown(), PrimaryNameServer: types.StringUnknown(), Policy: types.StringUnknown(),
		PrimaryServers: types.ListNull(primaryServerType), Nameservers: types.ListNull(types.StringType),
		EnableDynamicDNS: types.BoolUnknown(), EnableDNSSEC: types.BoolUnknown(), AutoCreatePTRRecords: types.BoolUnknown(),
		EnableZoneTransfer: types.BoolUnknown(), SecureSync: types.BoolUnknown(),
		ZoneACLID: types.Int64Null(), ZoneTransferTsigKeyID: types.Int64Null(), DynamicDNSTsigKeyID: types.Int64Null(), SecondarySyncTsigKeyID: types.Int64Unknown(),
		BusinessUnitIDs: types.ListUnknown(types.Int64Type),
	}
}

func TestBuildBodyForCreateOmitsUnknownAndUnset(t *testing.T) {
	ctx := context.Background()
	plan := baseModel()
	plan.TTL = types.Int64Value(60)
	plan.PrimaryServers, _ = types.ListValueFrom(ctx, primaryServerType, []primaryServerModel{
		{IP: types.StringValue("198.51.100.1"), Port: types.Int64Value(53), TsigKeyID: types.Int64Value(4)},
		{IP: types.StringValue("198.51.100.2"), Port: types.Int64Value(5353), TsigKeyID: types.Int64Null()},
	})
	plan.Nameservers, _ = types.ListValueFrom(ctx, types.StringType, []string{"198.51.100.9"})
	body, diags := buildBody(ctx, plan, nil)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if got := sortedBody(body); got != "name,nameservers,network_scope,primary_servers,ttl,view_id,zone_type" {
		t.Errorf("create body keys = %s", got)
	}
	servers := body["primary_servers"].([]map[string]any)
	if servers[0]["secure_sync"] != true || servers[0]["tsig_key_id"] != int64(4) || servers[1]["secure_sync"] != nil {
		t.Errorf("primary_servers = %v", servers)
	}
}

func sortedBody(m map[string]any) string {
	out := ""
	for _, k := range []string{"business_unit_ids", "cidr", "dynamic_dns_tsig_key_id", "email", "enable_dnssec", "enable_dynamic_dns", "name", "nameservers", "network_scope", "policy", "primary_servers", "protocol", "refresh", "retry", "secondary_sync_tsig_key_id", "secure_sync", "ttl", "view_id", "zone_acl_id", "zone_transfer_tsig_key_id", "zone_type"} {
		if _, ok := m[k]; ok {
			if out != "" {
				out += ","
			}
			out += k
		}
	}
	return out
}

func TestBuildBodyForUpdateIsDiffBased(t *testing.T) {
	ctx := context.Background()
	state := baseModel()
	state.TTL, state.Refresh, state.Retry = types.Int64Value(1), types.Int64Value(300), types.Int64Value(60)
	state.Expiry, state.Minimum = types.Int64Value(1), types.Int64Value(1)
	state.Email, state.PrimaryNameServer, state.Policy = types.StringNull(), types.StringNull(), types.StringNull()
	state.EnableDynamicDNS, state.EnableDNSSEC, state.AutoCreatePTRRecords = types.BoolValue(true), types.BoolValue(true), types.BoolNull()
	state.EnableZoneTransfer, state.SecureSync = types.BoolNull(), types.BoolNull()
	state.SecondarySyncTsigKeyID = types.Int64Null()
	state.BusinessUnitIDs, _ = types.ListValueFrom(ctx, types.Int64Type, []int64{1})
	state.ZoneACLID = types.Int64Value(9)

	plan := state
	body, diags := buildBody(ctx, plan, &state)
	if diags.HasError() || len(body) != 0 {
		t.Fatalf("an unchanged plan must produce an empty body, got %v %v", body, diags)
	}
	plan.TTL = types.Int64Value(2)
	plan.ZoneACLID = types.Int64Null()
	plan.EnableDNSSEC = types.BoolValue(false)
	plan.BusinessUnitIDs, _ = types.ListValueFrom(ctx, types.Int64Type, []int64{2, 1})
	body, diags = buildBody(ctx, plan, &state)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if got := sortedBody(body); got != "business_unit_ids,enable_dnssec,ttl,zone_acl_id" {
		t.Errorf("update body keys = %s", got)
	}
	if v, present := body["zone_acl_id"]; !present || v != nil {
		t.Errorf("a removed ACL reference must be sent as null: %v", body)
	}
	if units := body["business_unit_ids"].([]int64); units[0] != 2 {
		t.Errorf("business units = %v", units)
	}
}

func TestExpectedName(t *testing.T) {
	m := baseModel()
	if expectedName(m) != "corp.example.com" {
		t.Error("a known name is normalized")
	}
	m.Name = framework.NewFQDNValue("Corp.Example.com.")
	if expectedName(m) != "corp.example.com" {
		t.Error("case and trailing dot must be dropped")
	}
	m.Name = framework.NewFQDNUnknown()
	m.Protocol, m.CIDR = types.StringValue("IPv4"), types.StringValue("192.0.2.0/24")
	if expectedName(m) != "2.0.192.in-addr.arpa" {
		t.Errorf("a reverse name is derived: %q", expectedName(m))
	}
	m.CIDR = types.StringNull()
	if expectedName(m) != "" {
		t.Error("without a name or cidr there is no expected name")
	}
}

func TestReadAllKeepsConfiguredSpellingsAndFillsRemote(t *testing.T) {
	ctx := context.Background()
	dto := &zoneDTO{
		ID: 8, Name: "2.0.192.in-addr.arpa", ZoneType: ptr("reverse"), NetworkScope: ptr("internal"), ViewID: ptr(int64(3)), ViewName: ptr("internal"),
		RecordsCount: ptr(int64(4)), CreatedOn: ptr("c"), UpdatedOn: ptr("u"),
		ConfigJSON:          map[string]any{"protocol": "ipv4", "cidr": "192.0.2.0/24", "nameservers": []any{"2001:db8::53"}},
		BusinessUnits:       []idRef{{ID: 1}, {ID: 2}},
		PrimaryBusinessUnit: &idRef{ID: 1},
	}
	m := baseModel()
	m.Protocol, m.CIDR = types.StringValue("IPv4"), types.StringValue("192.0.2.5/24")
	m.Nameservers, _ = types.ListValueFrom(ctx, types.StringType, []string{"2001:0db8::53"})
	m.BusinessUnitIDs, _ = types.ListValueFrom(ctx, types.Int64Type, []int64{1, 2})
	if diags := m.readAll(ctx, dto); diags.HasError() {
		t.Fatal(diags)
	}
	if m.ZoneType.ValueString() != "Reverse" || m.NetworkScope.ValueString() != "Internal" {
		t.Errorf("enum spellings must be canonical: %s %s", m.ZoneType, m.NetworkScope)
	}
	if m.Protocol.ValueString() != "IPv4" || m.CIDR.ValueString() != "192.0.2.5/24" {
		t.Errorf("equivalent protocol and cidr must keep the configured spelling: %s %s", m.Protocol, m.CIDR)
	}
	if elems := m.Nameservers.Elements(); len(elems) != 1 || elems[0].(types.String).ValueString() != "2001:0db8::53" {
		t.Errorf("equivalent nameserver spelling must be kept: %v", elems)
	}
	if m.RecordsCount.ValueInt64() != 4 || m.ViewName.ValueString() != "internal" || !m.TTL.IsNull() {
		t.Errorf("unexpected model: %+v", m)
	}
	if !m.ZoneACLID.IsNull() {
		t.Error("zone_acl_id is not readable and must stay untouched")
	}
}

func TestFillComputedKeepsConfiguredValues(t *testing.T) {
	ctx := context.Background()
	dto := &zoneDTO{ID: 1, Name: "a.example.com", ConfigJSON: map[string]any{"ttl": float64(10), "email": "x@example.com"}, RecordsCount: ptr(int64(9))}
	m := baseModel()
	m.TTL = types.Int64Value(77)
	framework.SettleUnknowns(ctx, &m)
	if diags := m.fillComputed(ctx, dto); diags.HasError() {
		t.Fatal(diags)
	}
	if m.TTL.ValueInt64() != 77 || m.Email.ValueString() != "x@example.com" || m.RecordsCount.ValueInt64() != 9 {
		t.Errorf("configured values must survive and computed ones be filled: %+v", m)
	}
}

func TestMatchersAndImportKeys(t *testing.T) {
	if !createMatcher(3, "a.example.com", "")(map[string]any{"view_id": float64(3), "name": "A.example.com."}) {
		t.Error("name match is case- and dot-insensitive")
	}
	if createMatcher(3, "a.example.com", "")(map[string]any{"view_id": float64(4), "name": "a.example.com"}) {
		t.Error("another view must not match")
	}
	if !createMatcher(3, "", "192.0.2.0/24")(map[string]any{"view_id": float64(3), "cidr": "192.0.2.0/24"}) || createMatcher(3, "", "")(map[string]any{"view_id": float64(3)}) {
		t.Error("cidr matching")
	}
	if !zoneIDMatcher(4)(map[string]any{"zone_id": float64(4)}) || zoneIDMatcher(4)(map[string]any{}) {
		t.Error("zoneIDMatcher")
	}
	if firstNonEmpty("", "b", "c") != "b" || firstNonEmpty() != "" {
		t.Error("firstNonEmpty")
	}
	if zonePath(7) != "/zones/7" {
		t.Error("zonePath")
	}
}

func TestKeepHelpers(t *testing.T) {
	if !keepInt64(types.Int64Unknown(), nil).IsNull() || keepInt64(types.Int64Value(1), nil).ValueInt64() != 1 || keepInt64(types.Int64Value(1), ptr(int64(2))).ValueInt64() != 2 {
		t.Error("keepInt64")
	}
	if !keepBool(types.BoolUnknown(), nil).IsNull() || !keepBool(types.BoolValue(true), nil).ValueBool() || keepBool(types.BoolValue(true), ptr(false)).ValueBool() {
		t.Error("keepBool")
	}
	if !keepString(types.StringUnknown(), nil).IsNull() || keepString(types.StringValue("a"), nil).ValueString() != "a" || keepString(types.StringValue("a"), ptr("b")).ValueString() != "b" {
		t.Error("keepString")
	}
}

func TestCanonicalChoiceAcceptsPythonEnumSpelling(t *testing.T) {
	for remote, want := range map[string]string{
		"NetworkScope.INTERNAL": "Internal",
		"NetworkScope.EXTERNAL": "External",
		"internal":              "Internal",
		"Unknown.Value":         "Unknown.Value",
	} {
		if got := canonicalChoice(ptr(remote), networkScopes, types.StringNull()).ValueString(); got != want {
			t.Errorf("canonicalChoice(%q) = %q, want %q", remote, got, want)
		}
	}
}
