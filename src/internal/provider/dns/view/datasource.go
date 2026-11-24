package view

import (
	"context"

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

type viewItem struct {
	ID                     types.Int64  `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	Priority               types.Int64  `tfsdk:"priority"`
	ServerGroupIDs         types.Set    `tfsdk:"server_group_ids"`
	Recursion              types.Bool   `tfsdk:"recursion"`
	ForwardOnly            types.Bool   `tfsdk:"forward_only"`
	MatchRecursiveOnly     types.Bool   `tfsdk:"match_recursive_only"`
	Forwarders             types.List   `tfsdk:"forwarders"`
	AllowRecursionACLID    types.Int64  `tfsdk:"allow_recursion_acl_id"`
	MatchDestinationsACLID types.Int64  `tfsdk:"match_destinations_acl_id"`
	AllowQueryACLID        types.Int64  `tfsdk:"allow_query_acl_id"`
	AllowUpdateACLID       types.Int64  `tfsdk:"allow_update_acl_id"`
	ViewACLID              types.Int64  `tfsdk:"view_acl_id"`
	MaxCacheTTL            types.Int64  `tfsdk:"max_cache_ttl"`
	MaxNcacheTTL           types.Int64  `tfsdk:"max_ncache_ttl"`
	ZoneCount              types.Int64  `tfsdk:"zone_count"`
	CreatedOn              types.String `tfsdk:"created_on"`
	UpdatedOn              types.String `tfsdk:"updated_on"`
	Tags                   types.Map    `tfsdk:"tags"`
	TagsList               types.Map    `tfsdk:"tags_list"`
}

var viewItemAttrTypes = map[string]attr.Type{
	"id": types.Int64Type, "name": types.StringType, "description": types.StringType, "priority": types.Int64Type,
	"server_group_ids": types.SetType{ElemType: types.Int64Type}, "recursion": types.BoolType,
	"forward_only": types.BoolType, "match_recursive_only": types.BoolType,
	"forwarders":             types.ListType{ElemType: forwarderType},
	"allow_recursion_acl_id": types.Int64Type, "match_destinations_acl_id": types.Int64Type,
	"allow_query_acl_id": types.Int64Type, "allow_update_acl_id": types.Int64Type, "view_acl_id": types.Int64Type,
	"max_cache_ttl": types.Int64Type, "max_ncache_ttl": types.Int64Type, "zone_count": types.Int64Type,
	"created_on": types.StringType, "updated_on": types.StringType,
	"tags":      types.MapType{ElemType: types.StringType},
	"tags_list": types.MapType{ElemType: types.ListType{ElemType: types.StringType}},
}

type viewsModel struct {
	Filters types.List `tfsdk:"filters"`
	Views   types.List `tfsdk:"views"`
}

func viewItemAttributes() map[string]schema.Attribute {
	int64Computed := func(description string) schema.Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: description}
	}
	boolComputed := func(description string) schema.Attribute {
		return schema.BoolAttribute{Computed: true, MarkdownDescription: description}
	}
	stringComputed := func(description string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: description}
	}
	return map[string]schema.Attribute{
		"id":                        int64Computed("The OmniDDI view id."),
		"name":                      stringComputed("View name."),
		"description":               stringComputed("View description."),
		"priority":                  int64Computed("Match priority; a lower number is matched first."),
		"server_group_ids":          schema.SetAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Ids of the server groups that publish the view."},
		"recursion":                 boolComputed("Per-view recursion setting; null when the view inherits the server setting."),
		"forward_only":              boolComputed("Whether the view only uses its forwarders."),
		"match_recursive_only":      boolComputed("Whether only recursive queries select the view."),
		"forwarders":                schema.ListNestedAttribute{Computed: true, MarkdownDescription: "View forwarders; null when the view inherits the server forwarders.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{"ip": stringComputed("Forwarder address."), "port": int64Computed("Forwarder port.")}}},
		"allow_recursion_acl_id":    int64Computed("Id of the allow-recursion ACL."),
		"match_destinations_acl_id": int64Computed("Id of the match-destinations ACL."),
		"allow_query_acl_id":        int64Computed("Id of the allow-query ACL."),
		"allow_update_acl_id":       int64Computed("Id of the allow-update ACL."),
		"view_acl_id":               int64Computed("Id of the ACL that selects clients for the view."),
		"max_cache_ttl":             int64Computed("`max-cache-ttl` in seconds."),
		"max_ncache_ttl":            int64Computed("`max-ncache-ttl` in seconds."),
		"zone_count":                int64Computed("Number of zones in the view."),
		"created_on":                stringComputed("Creation timestamp."),
		"updated_on":                stringComputed("Last update timestamp."),
		"tags":                      schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Single-valued tags on the view. Not populated by the plural data source."},
		"tags_list":                 schema.MapAttribute{Computed: true, ElementType: types.ListType{ElemType: types.StringType}, MarkdownDescription: "Multi-valued tags on the view. Not populated by the plural data source."},
	}
}

func newViewItem(ctx context.Context, dto *viewDTO) (viewItem, diag.Diagnostics) {
	var diags diag.Diagnostics
	groups, d := serverGroupSet(ctx, dto.ServerGroupIDs)
	diags.Append(d...)
	forwarders, d := forwarderList(ctx, dto.Forwarders, types.ListNull(forwarderType))
	diags.Append(d...)
	return viewItem{
		ID:                     types.Int64Value(dto.ID),
		Name:                   types.StringValue(dto.Name),
		Description:            descriptionValue(dto.Description, types.StringNull()),
		Priority:               dnskit.Int64Value(dto.Priority),
		ServerGroupIDs:         groups,
		Recursion:              dnskit.BoolValue(dto.Recursion),
		ForwardOnly:            dnskit.BoolValue(dto.ForwardOnly),
		MatchRecursiveOnly:     dnskit.BoolValue(dto.MatchRecursiveOnly),
		Forwarders:             forwarders,
		AllowRecursionACLID:    dnskit.Int64Value(dto.AllowRecursionACLID),
		MatchDestinationsACLID: dnskit.Int64Value(dto.MatchDestinationsACLID),
		AllowQueryACLID:        dnskit.Int64Value(dto.AllowQueryACLID),
		AllowUpdateACLID:       dnskit.Int64Value(dto.AllowUpdateACLID),
		ViewACLID:              dnskit.Int64Value(dto.viewACLID()),
		MaxCacheTTL:            dnskit.Int64Value(dto.MaxCacheTTL),
		MaxNcacheTTL:           dnskit.Int64Value(dto.MaxNcacheTTL),
		ZoneCount:              dnskit.Int64Value(dto.ZoneCount),
		CreatedOn:              dnskit.StringPointer(dto.CreatedOn),
		UpdatedOn:              dnskit.StringPointer(dto.UpdatedOn),
		Tags:                   types.MapNull(types.StringType),
		TagsList:               types.MapNull(types.ListType{ElemType: types.StringType}),
	}, diags
}

type viewDataSource struct {
	pd *framework.ProviderData
}

var (
	_ datasource.DataSourceWithConfigure        = (*viewDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*viewDataSource)(nil)
)

func NewDataSource() datasource.DataSource { return &viewDataSource{} }

func (d *viewDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_view"
}

func (d *viewDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *viewDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

func (d *viewDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := viewItemAttributes()
	attrs["id"] = schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "The OmniDDI view id. Set exactly one of `id` and `name`."}
	attrs["name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "View name. Set exactly one of `id` and `name`."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one DNS view by id or by name.",
		Attributes:          attrs,
	}
}

func (d *viewDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config viewItem
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		dto *viewDTO
		err error
	)
	if !config.ID.IsNull() {
		dto, err = fetchView(ctx, d.pd.Client, config.ID.ValueInt64())
	} else {
		dto, err = findViewByName(ctx, d.pd.Client, config.Name.ValueString())
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the DNS view", err) {
		return
	}
	item, diags := newViewItem(ctx, dto)
	resp.Diagnostics.Append(diags...)
	remote, err := d.pd.Client.GetTagValues(ctx, client.TagObjectDNSView, dto.ID)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the view tags", err) {
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

type viewsDataSource struct {
	pd *framework.ProviderData
}

var _ datasource.DataSourceWithConfigure = (*viewsDataSource)(nil)

func NewListDataSource() datasource.DataSource { return &viewsDataSource{} }

func (d *viewsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_views"
}

func (d *viewsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *viewsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists DNS views, optionally narrowed by server-side filters. Every filter must match.",
		Attributes: map[string]schema.Attribute{
			"filters": dnskit.FiltersAttribute("viewsPaginated", "view", "`name` or `priority`"),
			"views": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching views, ordered by id.",
				NestedObject:        schema.NestedAttributeObject{Attributes: viewItemAttributes()},
			},
		},
	}
}

func (d *viewsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config viewsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filters, diags := dnskit.BuildFilters(ctx, config.Filters)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	dtos, err := listViews(ctx, d.pd.Client, filters)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list DNS views", err) {
		return
	}
	items := make([]viewItem, 0, len(dtos))
	for i := range dtos {
		item, itemDiags := newViewItem(ctx, &dtos[i])
		resp.Diagnostics.Append(itemDiags...)
		items = append(items, item)
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: viewItemAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Views = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
