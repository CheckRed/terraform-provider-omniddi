package zone

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

type zoneItem struct {
	ID                     types.Int64  `tfsdk:"id"`
	ViewID                 types.Int64  `tfsdk:"view_id"`
	ViewName               types.String `tfsdk:"view_name"`
	Name                   types.String `tfsdk:"name"`
	ZoneType               types.String `tfsdk:"zone_type"`
	NetworkScope           types.String `tfsdk:"network_scope"`
	Protocol               types.String `tfsdk:"protocol"`
	CIDR                   types.String `tfsdk:"cidr"`
	TTL                    types.Int64  `tfsdk:"ttl"`
	Refresh                types.Int64  `tfsdk:"refresh"`
	Retry                  types.Int64  `tfsdk:"retry"`
	Expiry                 types.Int64  `tfsdk:"expiry"`
	Minimum                types.Int64  `tfsdk:"minimum"`
	Email                  types.String `tfsdk:"email"`
	PrimaryNameServer      types.String `tfsdk:"primary_name_server"`
	PrimaryServers         types.List   `tfsdk:"primary_servers"`
	Nameservers            types.List   `tfsdk:"nameservers"`
	Policy                 types.String `tfsdk:"policy"`
	EnableDynamicDNS       types.Bool   `tfsdk:"enable_dynamic_dns"`
	EnableDNSSEC           types.Bool   `tfsdk:"enable_dnssec"`
	AutoCreatePTRRecords   types.Bool   `tfsdk:"auto_create_ptr_records"`
	EnableZoneTransfer     types.Bool   `tfsdk:"enable_zone_transfer"`
	SecureSync             types.Bool   `tfsdk:"secure_sync"`
	ZoneTransferTsigKeyID  types.Int64  `tfsdk:"zone_transfer_tsig_key_id"`
	DynamicDNSTsigKeyID    types.Int64  `tfsdk:"dynamic_dns_tsig_key_id"`
	SecondarySyncTsigKeyID types.Int64  `tfsdk:"secondary_sync_tsig_key_id"`
	BusinessUnitIDs        types.List   `tfsdk:"business_unit_ids"`
	SOASerial              types.Int64  `tfsdk:"soa_serial"`
	RecordsCount           types.Int64  `tfsdk:"records_count"`
	CreatedOn              types.String `tfsdk:"created_on"`
	UpdatedOn              types.String `tfsdk:"updated_on"`
	Tags                   types.Map    `tfsdk:"tags"`
	TagsList               types.Map    `tfsdk:"tags_list"`
}

var zoneItemAttrTypes = map[string]attr.Type{
	"id": types.Int64Type, "view_id": types.Int64Type, "view_name": types.StringType, "name": types.StringType,
	"zone_type": types.StringType, "network_scope": types.StringType, "protocol": types.StringType, "cidr": types.StringType,
	"ttl": types.Int64Type, "refresh": types.Int64Type, "retry": types.Int64Type, "expiry": types.Int64Type, "minimum": types.Int64Type,
	"email": types.StringType, "primary_name_server": types.StringType,
	"primary_servers": types.ListType{ElemType: primaryServerType}, "nameservers": types.ListType{ElemType: types.StringType},
	"policy": types.StringType, "enable_dynamic_dns": types.BoolType, "enable_dnssec": types.BoolType,
	"auto_create_ptr_records": types.BoolType, "enable_zone_transfer": types.BoolType, "secure_sync": types.BoolType,
	"zone_transfer_tsig_key_id": types.Int64Type, "dynamic_dns_tsig_key_id": types.Int64Type, "secondary_sync_tsig_key_id": types.Int64Type,
	"business_unit_ids": types.ListType{ElemType: types.Int64Type}, "soa_serial": types.Int64Type, "records_count": types.Int64Type,
	"created_on": types.StringType, "updated_on": types.StringType,
	"tags":      types.MapType{ElemType: types.StringType},
	"tags_list": types.MapType{ElemType: types.ListType{ElemType: types.StringType}},
}

type zonesModel struct {
	ViewID  types.Int64 `tfsdk:"view_id"`
	Filters types.List  `tfsdk:"filters"`
	Zones   types.List  `tfsdk:"zones"`
}

func zoneItemAttributes() map[string]schema.Attribute {
	i64 := func(d string) schema.Attribute { return schema.Int64Attribute{Computed: true, MarkdownDescription: d} }
	boolean := func(d string) schema.Attribute { return schema.BoolAttribute{Computed: true, MarkdownDescription: d} }
	str := func(d string) schema.Attribute { return schema.StringAttribute{Computed: true, MarkdownDescription: d} }
	return map[string]schema.Attribute{
		"id":                         i64("The OmniDDI zone id."),
		"view_id":                    i64("Id of the view that holds the zone."),
		"view_name":                  str("Name of the view that holds the zone."),
		"name":                       str("Zone name."),
		"zone_type":                  str("Zone type: `Primary`, `Secondary`, `Forward` or `Reverse`."),
		"network_scope":              str("Network scope: `Internal` or `External`."),
		"protocol":                   str("IP protocol of a `Reverse` zone."),
		"cidr":                       str("Network of a `Reverse` zone."),
		"ttl":                        i64("Default TTL in seconds."),
		"refresh":                    i64("SOA refresh interval in seconds."),
		"retry":                      i64("SOA retry interval in seconds."),
		"expiry":                     i64("SOA expire time in seconds."),
		"minimum":                    i64("SOA minimum TTL in seconds."),
		"email":                      str("SOA contact address."),
		"primary_name_server":        str("SOA primary name server."),
		"primary_servers":            schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Primary servers of a `Secondary` zone.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{"ip": str("Server address."), "port": i64("Server port."), "tsig_key_id": i64("TSIG key id securing the transfer.")}}},
		"nameservers":                schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Forward targets of a `Forward` zone."},
		"policy":                     str("Forward policy of a `Forward` zone."),
		"enable_dynamic_dns":         boolean("Whether dynamic DNS is enabled."),
		"enable_dnssec":              boolean("Whether DNSSEC is enabled."),
		"auto_create_ptr_records":    boolean("Whether PTR records are created automatically."),
		"enable_zone_transfer":       boolean("Whether zone transfers are allowed."),
		"secure_sync":                boolean("Whether a `Secondary` zone uses TSIG-secured transfers."),
		"zone_transfer_tsig_key_id":  i64("TSIG key id used for zone transfers."),
		"dynamic_dns_tsig_key_id":    i64("TSIG key id used for dynamic DNS."),
		"secondary_sync_tsig_key_id": i64("TSIG key id used by a `Secondary` zone to sync."),
		"business_unit_ids":          schema.ListAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Business unit ids; the first is the primary one."},
		"soa_serial":                 i64("Current SOA serial."),
		"records_count":              i64("Number of records in the zone."),
		"created_on":                 str("Creation timestamp."),
		"updated_on":                 str("Last update timestamp."),
		"tags":                       schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Single-valued tags on the zone. Not populated by the plural data source."},
		"tags_list":                  schema.MapAttribute{Computed: true, ElementType: types.ListType{ElemType: types.StringType}, MarkdownDescription: "Multi-valued tags on the zone. Not populated by the plural data source."},
	}
}

func newZoneItem(ctx context.Context, dto *zoneDTO) (zoneItem, diag.Diagnostics) {
	var diags diag.Diagnostics
	r := parseZone(dto)
	servers, d := primaryServerList(ctx, r.PrimaryServers, dto.SecondarySyncTsigKeyID, types.ListNull(primaryServerType))
	diags.Append(d...)
	nameservers, d := nameserverList(ctx, r.Nameservers, types.ListNull(types.StringType))
	diags.Append(d...)
	units, d := businessUnitList(ctx, r.BusinessUnitIDs, types.ListNull(types.Int64Type))
	diags.Append(d...)
	return zoneItem{
		ID: types.Int64Value(dto.ID), ViewID: dnskit.Int64Value(dto.ViewID), ViewName: dnskit.StringPointer(dto.ViewName),
		Name: types.StringValue(dto.Name), ZoneType: canonicalChoice(dto.ZoneType, zoneTypes, types.StringNull()), NetworkScope: canonicalChoice(dto.NetworkScope, networkScopes, types.StringNull()),
		Protocol: dnskit.StringPointer(r.Protocol), CIDR: dnskit.StringPointer(r.CIDR),
		TTL: dnskit.Int64Value(r.TTL), Refresh: dnskit.Int64Value(r.Refresh), Retry: dnskit.Int64Value(r.Retry), Expiry: dnskit.Int64Value(r.Expiry), Minimum: dnskit.Int64Value(r.Minimum),
		Email: dnskit.StringPointer(r.Email), PrimaryNameServer: dnskit.StringPointer(r.PrimaryNameServer),
		PrimaryServers: servers, Nameservers: nameservers, Policy: dnskit.StringPointer(r.Policy),
		EnableDynamicDNS: dnskit.BoolValue(r.EnableDynamicDNS), EnableDNSSEC: dnskit.BoolValue(r.EnableDNSSEC),
		AutoCreatePTRRecords: dnskit.BoolValue(r.AutoCreatePTRRecords), EnableZoneTransfer: dnskit.BoolValue(r.EnableZoneTransfer),
		SecureSync:            dnskit.BoolValue(r.SecureSync),
		ZoneTransferTsigKeyID: dnskit.Int64Value(dto.ZoneTransferTsigKeyID), DynamicDNSTsigKeyID: dnskit.Int64Value(dto.DynamicDNSTsigKeyID),
		SecondarySyncTsigKeyID: dnskit.Int64Value(dto.SecondarySyncTsigKeyID),
		BusinessUnitIDs:        units, SOASerial: dnskit.Int64Value(r.SOASerial), RecordsCount: dnskit.Int64Value(dto.RecordsCount),
		CreatedOn: dnskit.StringPointer(dto.CreatedOn), UpdatedOn: dnskit.StringPointer(dto.UpdatedOn),
		Tags:     types.MapNull(types.StringType),
		TagsList: types.MapNull(types.ListType{ElemType: types.StringType}),
	}, diags
}

type zoneDataSource struct {
	pd *framework.ProviderData
}

var (
	_ datasource.DataSourceWithConfigure        = (*zoneDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*zoneDataSource)(nil)
)

func NewDataSource() datasource.DataSource { return &zoneDataSource{} }

func (d *zoneDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (d *zoneDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *zoneDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
		datasourcevalidator.Conflicting(path.MatchRoot("id"), path.MatchRoot("view_name")),
	}
}

func (d *zoneDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := zoneItemAttributes()
	attrs["id"] = schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "The OmniDDI zone id. Set exactly one of `id` and `name`."}
	attrs["name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Zone name, compared case-insensitively and without a trailing dot. Set exactly one of `id` and `name`; with `name`, also set `view_id` or `view_name`."}
	attrs["view_id"] = schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Id of the view that holds the zone. With `name`, set this or `view_name`."}
	attrs["view_name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Name of the view that holds the zone. With `name`, set this or `view_id`."}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one DNS zone by id, or by name within a view.", Attributes: attrs}
}

func (d *zoneDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config zoneItem
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dto, err := d.lookup(ctx, config)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the DNS zone", err) {
		return
	}
	item, diags := newZoneItem(ctx, dto)
	resp.Diagnostics.Append(diags...)
	remote, err := d.pd.Client.GetTagValues(ctx, client.TagObjectDNSZone, dto.ID)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the zone tags", err) {
		return
	}
	var noDefaults *framework.ProviderData
	tags, diags := noDefaults.TagsFromRemote(ctx, remote, types.MapNull(types.StringType), types.MapNull(types.ListType{ElemType: types.StringType}))
	resp.Diagnostics.Append(diags...)
	item.Tags, item.TagsList = tags.Tags, tags.TagsList
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &item)...)
}

func (d *zoneDataSource) lookup(ctx context.Context, config zoneItem) (*zoneDTO, error) {
	if !config.ID.IsNull() {
		return fetchZone(ctx, d.pd.Client, config.ID.ValueInt64())
	}
	switch {
	case !config.ViewID.IsNull():
		return findZone(ctx, d.pd.Client, config.ViewID.ValueInt64(), config.Name.ValueString())
	case !config.ViewName.IsNull():
		viewID, err := resolveViewID(ctx, d.pd.Client, config.ViewName.ValueString())
		if err != nil {
			return nil, err
		}
		return findZone(ctx, d.pd.Client, viewID, config.Name.ValueString())
	}
	return nil, errors.New("looking a zone up by name needs view_id or view_name")
}

type zonesDataSource struct {
	pd *framework.ProviderData
}

var _ datasource.DataSourceWithConfigure = (*zonesDataSource)(nil)

func NewListDataSource() datasource.DataSource { return &zonesDataSource{} }

func (d *zonesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zones"
}

func (d *zonesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *zonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists DNS zones, optionally of one view and narrowed by server-side filters. Every filter must match.",
		Attributes: map[string]schema.Attribute{
			"view_id": schema.Int64Attribute{Optional: true, MarkdownDescription: "Only list the zones of this view."},
			"filters": dnskit.FiltersAttribute("zonesPaginated", "zone", "`name` or `zoneType`"),
			"zones": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching zones, ordered by id.",
				NestedObject:        schema.NestedAttributeObject{Attributes: zoneItemAttributes()},
			},
		},
	}
}

func (d *zonesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config zonesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filters, diags := dnskit.BuildFilters(ctx, config.Filters)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var viewID *int64
	if !config.ViewID.IsNull() {
		id := config.ViewID.ValueInt64()
		viewID = &id
	}
	dtos, err := listZones(ctx, d.pd.Client, viewID, filters)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list DNS zones", err) {
		return
	}
	items := make([]zoneItem, 0, len(dtos))
	for i := range dtos {
		item, itemDiags := newZoneItem(ctx, &dtos[i])
		resp.Diagnostics.Append(itemDiags...)
		items = append(items, item)
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: zoneItemAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Zones = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
