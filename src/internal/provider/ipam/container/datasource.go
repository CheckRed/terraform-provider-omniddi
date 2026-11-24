package container

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

var (
	_ datasource.DataSourceWithConfigure        = (*singleDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*singleDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*listDataSource)(nil)
)

type singleDataSource struct {
	pd *framework.ProviderData
}

func NewDataSource() datasource.DataSource { return &singleDataSource{} }

func (d *singleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_container"
}

func (d *singleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *singleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an IPAM container by id or by CIDR. Exactly one of `id` and `cidr` is required.",
		Attributes:          nodeapi.DataAttributes(true, true, false),
	}
}

func (d *singleDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("cidr")),
	}
}

func (d *singleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config nodeapi.DataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		node *nodeapi.Node
		err  error
	)
	if config.ID.IsNull() {
		node, err = nodeapi.ResolveCIDR(ctx, d.pd.Client, config.CIDR.ValueString(), nodeapi.TypeContainer)
	} else {
		node, err = nodeapi.FetchOfType(ctx, d.pd.Client, config.ID.ValueInt64(), nodeapi.TypeContainer)
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the IPAM container", err) {
		return
	}
	state, diags := nodeapi.Flatten(ctx, *node)
	resp.Diagnostics.Append(diags...)
	state.CIDR = nodeapi.KeepConfiguredCIDR(config.CIDR, state.CIDR)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

type listModel struct {
	ParentID   types.Int64  `tfsdk:"parent_id"`
	Recursive  types.Bool   `tfsdk:"recursive"`
	Search     types.String `tfsdk:"search"`
	TagFilters types.Map    `tfsdk:"tag_filters"`
	Containers types.List   `tfsdk:"containers"`
}

type listDataSource struct {
	pd *framework.ProviderData
}

func NewListDataSource() datasource.DataSource { return &listDataSource{} }

func (d *listDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_containers"
}

func (d *listDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *listDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists IPAM containers. The tree is walked from `parent_id` (or from the address-family roots when it is not set); " +
			"each level is one API call, so scope the walk with `parent_id` on large trees.",
		Attributes: map[string]schema.Attribute{
			"parent_id": schema.Int64Attribute{Optional: true, MarkdownDescription: "Only list containers below this node."},
			"recursive": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Also descend into containers found. Defaults to `true`.",
			},
			"search": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Case-insensitive substring matched against the name, CIDR, owner and description.",
			},
			"tag_filters": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Only list containers carrying all of these tag values (own or inherited).",
			},
			"containers": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching containers, ordered by id.",
				NestedObject:        schema.NestedAttributeObject{Attributes: nodeapi.DataAttributes(false, false, false)},
			},
		},
	}
}

func (d *listDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config listModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := nodeapi.WalkOptions{Recursive: config.Recursive.IsNull() || config.Recursive.ValueBool(), Search: config.Search.ValueString()}
	if !config.ParentID.IsNull() {
		parent := config.ParentID.ValueInt64()
		opts.ParentID = &parent
	}
	if !config.TagFilters.IsNull() {
		resp.Diagnostics.Append(config.TagFilters.ElementsAs(ctx, &opts.Tags, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	nodes, err := nodeapi.WalkContainers(ctx, d.pd.Client, opts)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list IPAM containers", err) {
		return
	}
	items, diags := nodeapi.FlattenList(ctx, nodes)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	config.Containers = items
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
