package servergroup

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

var listItemType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":             types.Int64Type,
	"name":           types.StringType,
	"type":           types.StringType,
	"description":    types.StringType,
	"dns_vendor_id":  types.Int64Type,
	"dhcp_vendor_id": types.Int64Type,
	"scope_id":       types.Int64Type,
	"protocol":       types.StringType,
}}

type listModel struct {
	Name         types.String `tfsdk:"name"`
	Type         types.String `tfsdk:"type"`
	ServerGroups types.List   `tfsdk:"server_groups"`
}

type listDataSource struct {
	pd *framework.ProviderData
}

var _ datasource.DataSourceWithConfigure = (*listDataSource)(nil)

func NewListDataSource() datasource.DataSource { return &listDataSource{} }

func (d *listDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_groups"
}

func (d *listDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *listDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists server groups, optionally narrowed by exact `name` and by `type`. Matching none is not an error.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Optional: true, MarkdownDescription: "Return only the groups with exactly this name."},
			"type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only groups of this type: `dns`, `dhcp` or `analytics`.",
				Validators:          []validator.String{stringvalidator.OneOf(typeDNS, typeDHCP, typeAnalytics)},
			},
			"server_groups": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching server groups.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             schema.Int64Attribute{Computed: true, MarkdownDescription: "Server group id."},
					"name":           schema.StringAttribute{Computed: true, MarkdownDescription: "Server group name."},
					"type":           schema.StringAttribute{Computed: true, MarkdownDescription: "`dns`, `dhcp` or `analytics`."},
					"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description of the group."},
					"dns_vendor_id":  schema.Int64Attribute{Computed: true, MarkdownDescription: "DNS vendor id of a `dns` group."},
					"dhcp_vendor_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "DHCP vendor id of a `dhcp` group."},
					"scope_id":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Protocol scope id of a `dhcp` group."},
					"protocol":       schema.StringAttribute{Computed: true, MarkdownDescription: "Protocol served by a `dhcp` group."},
				}},
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
	groups, err := listGroups(ctx, d.pd.Client, config.Type.ValueString(), config.Name.ValueString())
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list server groups", err) {
		return
	}
	items := make([]attr.Value, 0, len(groups))
	for _, g := range groups {
		items = append(items, types.ObjectValueMust(listItemType.AttrTypes, map[string]attr.Value{
			"id":             types.Int64Value(g.ID),
			"name":           types.StringValue(g.Name),
			"type":           types.StringValue(g.Type),
			"description":    types.StringPointerValue(g.Description),
			"dns_vendor_id":  types.Int64PointerValue(g.DNSVendorID),
			"dhcp_vendor_id": types.Int64PointerValue(g.DHCPVendorID),
			"scope_id":       types.Int64PointerValue(g.scopeID()),
			"protocol":       types.StringPointerValue(g.Protocol),
		}))
	}
	config.ServerGroups = types.ListValueMust(listItemType, items)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
