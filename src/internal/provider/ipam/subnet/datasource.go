package subnet

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const tagFieldPrefix = "tag."

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
	resp.TypeName = req.ProviderTypeName + "_ipam_subnet"
}

func (d *singleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *singleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an IPAM subnet by id or by CIDR. Exactly one of `id` and `cidr` is required.",
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
		node, err = nodeapi.FindSubnetByCIDR(ctx, d.pd.Client, config.CIDR.ValueString())
	} else {
		node, err = nodeapi.FetchOfType(ctx, d.pd.Client, config.ID.ValueInt64(), nodeapi.TypeSubnet)
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the IPAM subnet", err) {
		return
	}
	state, diags := nodeapi.Flatten(ctx, *node)
	resp.Diagnostics.Append(diags...)
	state.CIDR = nodeapi.KeepConfiguredCIDR(config.CIDR, state.CIDR)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

type filterModel struct {
	Field    types.String `tfsdk:"field"`
	Operator types.String `tfsdk:"operator"`
	Value    types.String `tfsdk:"value"`
}

type listModel struct {
	Search      types.String `tfsdk:"search"`
	ScopeNodeID types.Int64  `tfsdk:"scope_node_id"`
	Filters     types.List   `tfsdk:"filters"`
	TagFilters  types.Map    `tfsdk:"tag_filters"`
	Subnets     types.List   `tfsdk:"subnets"`
}

type listDataSource struct {
	pd *framework.ProviderData
}

func NewListDataSource() datasource.DataSource { return &listDataSource{} }

func (d *listDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_subnets"
}

func (d *listDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *listDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists IPAM subnets, optionally narrowed by a quick search, typed filters, tag filters and a scope node.",
		Attributes: map[string]schema.Attribute{
			"search": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Case-insensitive substring matched against the name, network address, CIDR, protocol, VLAN, owner and description.",
			},
			"scope_node_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Only list subnets at or below this container or family node.",
			},
			"filters": schema.ListNestedAttribute{
				Optional: true,
				MarkdownDescription: "Typed filters, all of which must match. `field` is a subnet column such as `name`, `networkAddress`, " +
					"`prefixDisplay`, `status`, `vlan`, `owner` or `protocol`.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"field": schema.StringAttribute{Required: true, MarkdownDescription: "The field to filter on."},
					"operator": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "One of `EQ`, `NEQ`, `CONTAINS`, `NOT_CONTAINS`, `STARTS_WITH`, `ENDS_WITH`, `IS_NULL`, `NOT_NULL`.",
						Validators: []validator.String{stringvalidator.OneOf(
							"EQ", "NEQ", "CONTAINS", "NOT_CONTAINS", "STARTS_WITH", "ENDS_WITH", "IS_NULL", "NOT_NULL")},
					},
					"value": schema.StringAttribute{Optional: true, MarkdownDescription: "The value to compare with."},
				}},
			},
			"tag_filters": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Only list subnets whose tags equal all of these values.",
			},
			"subnets": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching subnets, ordered by name.",
				NestedObject:        schema.NestedAttributeObject{Attributes: nodeapi.DataAttributes(false, false, false)},
			},
		},
	}
}

func (d *listDataSource) query(ctx context.Context, config listModel) (nodeapi.SubnetQuery, error) {
	query := nodeapi.SubnetQuery{Search: config.Search.ValueString()}
	if !config.ScopeNodeID.IsNull() {
		scope := config.ScopeNodeID.ValueInt64()
		query.ScopeNodeID = &scope
	}
	if !config.Filters.IsNull() {
		var filters []filterModel
		if diags := config.Filters.ElementsAs(ctx, &filters, false); diags.HasError() {
			return query, fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
		for _, f := range filters {
			query.Filters = append(query.Filters, nodeapi.SubnetFilter{Field: f.Field.ValueString(), Operator: f.Operator.ValueString(), Value: f.Value.ValueStringPointer()})
		}
	}
	if !config.TagFilters.IsNull() {
		tags := map[string]string{}
		if diags := config.TagFilters.ElementsAs(ctx, &tags, false); diags.HasError() {
			return query, fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
		names := make([]string, 0, len(tags))
		for name := range tags {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			query.Filters = append(query.Filters, nodeapi.SubnetFilter{Field: tagFieldPrefix + name, Operator: "EQ", Value: tags[name]})
		}
	}
	return query, nil
}

func (d *listDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config listModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query, err := d.query(ctx, config)
	if framework.AppendIfErr(&resp.Diagnostics, "Invalid subnet filters", err) {
		return
	}
	nodes, err := nodeapi.ListSubnets(ctx, d.pd.Client, query)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list IPAM subnets", err) {
		return
	}
	items, diags := nodeapi.FlattenList(ctx, nodes)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	config.Subnets = items
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
