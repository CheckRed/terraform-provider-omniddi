package nextfree

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

var (
	_ datasource.DataSourceWithConfigure        = (*ipDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*ipDataSource)(nil)
)

type ipModel struct {
	SubnetID   types.Int64        `tfsdk:"subnet_id"`
	SubnetCIDR cidrtypes.IPPrefix `tfsdk:"subnet_cidr"`
	Quantity   types.Int64        `tfsdk:"quantity"`
	Exclude    types.Set          `tfsdk:"exclude"`
	Addresses  types.List         `tfsdk:"addresses"`
	IPAddress  types.String       `tfsdk:"ip_address"`
}

type ipDataSource struct{ base }

func NewIPDataSource() datasource.DataSource { return &ipDataSource{} }

func (d *ipDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_next_free_ip"
}

func (d *ipDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Shows the next free addresses in a subnet without allocating them. " + advisoryNote +
			" To allocate an address, use the `omniddi_ipam_address` resource.",
		Attributes: map[string]schema.Attribute{
			"subnet_id": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Id of the subnet. Set this, `subnet_cidr`, or both.",
			},
			"subnet_cidr": schema.StringAttribute{
				Optional: true, Computed: true, CustomType: cidrtypes.IPPrefixType{},
				MarkdownDescription: "CIDR of the subnet. Set this, `subnet_id`, or both.",
			},
			"quantity": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "How many addresses to look for, 1 to 100. Defaults to 1. All or nothing: when fewer are free, `addresses` is empty.",
				Validators:          []validator.Int64{int64validator.Between(1, MaxCount)},
			},
			"exclude": schema.SetAttribute{
				Optional: true, ElementType: iptypes.IPAddressType{},
				MarkdownDescription: "Addresses never to return, at most 100.",
				Validators:          []validator.Set{setvalidator.SizeAtMost(MaxExclude)},
			},
			"addresses": schema.ListAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "The lowest free addresses, in order. Empty when fewer than `quantity` are free.",
			},
			"ip_address": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The first free address, or null when none is free.",
			},
		},
	}
}

func (d *ipDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.AtLeastOneOf(path.MatchRoot("subnet_id"), path.MatchRoot("subnet_cidr"))}
}

func (d *ipDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m ipModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	count := int64(1)
	if !m.Quantity.IsNull() {
		count = m.Quantity.ValueInt64()
	}
	target := Target{ID: m.SubnetID.ValueInt64(), CIDR: m.SubnetCIDR.ValueString()}
	found, err := FindIPs(ctx, d.pd.Client, target, count, Strings(m.Exclude))
	switch {
	case IsExhausted(err):
		found = &IPs{Subnet: target}
	case framework.AppendIfErr(&resp.Diagnostics, "Unable to find free addresses", err):
		return
	}
	addresses, diags := types.ListValueFrom(ctx, types.StringType, found.Addresses)
	resp.Diagnostics.Append(diags...)
	m.Addresses = addresses
	m.IPAddress = types.StringNull()
	if len(found.Addresses) > 0 {
		m.IPAddress = types.StringValue(found.Addresses[0])
	}
	m.SubnetID = int64OrNull(found.Subnet.ID)
	m.SubnetCIDR = cidrtypes.NewIPPrefixNull()
	if found.Subnet.CIDR != "" {
		m.SubnetCIDR = cidrtypes.NewIPPrefixValue(found.Subnet.CIDR)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
