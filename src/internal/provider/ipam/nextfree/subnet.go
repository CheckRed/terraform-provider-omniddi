package nextfree

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
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
	_ datasource.DataSourceWithConfigure        = (*subnetDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*subnetDataSource)(nil)
)

type subnetModel struct {
	ContainerID   types.Int64        `tfsdk:"container_id"`
	ContainerCIDR cidrtypes.IPPrefix `tfsdk:"container_cidr"`
	PrefixLength  types.Int64        `tfsdk:"prefix_length"`
	Exclude       types.Set          `tfsdk:"exclude"`
	CIDR          cidrtypes.IPPrefix `tfsdk:"cidr"`
	NetworkAddr   types.String       `tfsdk:"network_addr"`
}

type subnetDataSource struct{ base }

func NewSubnetDataSource() datasource.DataSource { return &subnetDataSource{} }

func (d *subnetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_next_free_subnet"
}

func (d *subnetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Shows the lowest free block of a given size in a container without creating it. " + advisoryNote +
			" To create a subnet in the next free block, leave `network_addr` unset on `omniddi_ipam_subnet`.",
		Attributes: map[string]schema.Attribute{
			"container_id": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Id of the container. Set this, `container_cidr`, or both.",
			},
			"container_cidr": schema.StringAttribute{
				Optional: true, Computed: true, CustomType: cidrtypes.IPPrefixType{},
				MarkdownDescription: "CIDR of the container. Set this, `container_id`, or both.",
			},
			"prefix_length": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Size of the block to look for. It must be longer than the container's prefix, at most 32 (IPv4) or 128 (IPv6).",
				Validators:          []validator.Int64{int64validator.Between(1, 128)},
			},
			"exclude": schema.SetAttribute{
				Optional: true, ElementType: cidrtypes.IPPrefixType{},
				MarkdownDescription: "CIDRs the block must not overlap, at most 100.",
				Validators:          []validator.Set{setvalidator.SizeAtMost(MaxExclude)},
			},
			"cidr": schema.StringAttribute{
				Computed: true, CustomType: cidrtypes.IPPrefixType{},
				MarkdownDescription: "The lowest free block, or null when no block of this size is free.",
			},
			"network_addr": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Network address of `cidr`, or null when no block of this size is free.",
			},
		},
	}
}

func (d *subnetDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.AtLeastOneOf(path.MatchRoot("container_id"), path.MatchRoot("container_cidr"))}
}

func (d *subnetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m subnetModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	target := Target{ID: m.ContainerID.ValueInt64(), CIDR: m.ContainerCIDR.ValueString()}
	block, err := FindSubnet(ctx, d.pd.Client, target, m.PrefixLength.ValueInt64(), Strings(m.Exclude))
	m.CIDR, m.NetworkAddr = cidrtypes.NewIPPrefixNull(), types.StringNull()
	switch {
	case IsExhausted(err):
		block = &Block{Container: target}
	case framework.AppendIfErr(&resp.Diagnostics, "Unable to find a free block", err):
		return
	default:
		m.CIDR = cidrtypes.NewIPPrefixValue(block.CIDR.String())
		m.NetworkAddr = types.StringValue(block.CIDR.Addr().String())
	}
	m.ContainerID = int64OrNull(block.Container.ID)
	if block.Container.CIDR != "" {
		m.ContainerCIDR = cidrtypes.NewIPPrefixValue(block.Container.CIDR)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
