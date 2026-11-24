package node

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

var (
	_ datasource.DataSourceWithConfigure        = (*dataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*dataSource)(nil)
)

type dataSource struct {
	pd *framework.ProviderData
}

const (
	protocolIPv4 = "IPv4"
	protocolIPv6 = "IPv6"
)

func NewDataSource() datasource.DataSource { return &dataSource{} }

func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewDataSource}
}

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_node"
}

func (d *dataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := nodeapi.DataAttributes(true, true, true)
	typeAttr := attrs["type"].(schema.StringAttribute)
	typeAttr.MarkdownDescription = "Node type: `FAMILY`, `CONTAINER` or `SUBNET`. When set together with `cidr`, the node found must have this type."
	typeAttr.Validators = []validator.String{stringvalidator.OneOf(nodeapi.TypeFamily, nodeapi.TypeContainer, nodeapi.TypeSubnet)}
	attrs["type"] = typeAttr
	protocolAttr := attrs["protocol"].(schema.StringAttribute)
	protocolAttr.Optional = true
	protocolAttr.MarkdownDescription = "`IPv4` or `IPv6`. Set it instead of `id` or `cidr` to look up that address family's root node, " +
		"the `parent_id` of a top-level `omniddi_ipam_container`."
	protocolAttr.Validators = []validator.String{stringvalidator.OneOf(protocolIPv4, protocolIPv6)}
	attrs["protocol"] = protocolAttr
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up any node of the IP space tree, such as a family root, container or subnet, by id, by CIDR, " +
			"or (for a family root) by protocol. Use it to find the `parent_id` for `omniddi_ipam_container` and `omniddi_ipam_subnet`. " +
			"Exactly one of `id`, `cidr` and `protocol` is required. A lookup by CIDR walks the tree from the family roots, one API call per level.",
		Attributes: attrs,
	}
}

func (d *dataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("cidr"), path.MatchRoot("protocol")),
	}
}

func (d *dataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config nodeapi.DataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	wantType := config.Type.ValueString()
	var (
		node *nodeapi.Node
		err  error
	)
	switch {
	case !config.Protocol.IsNull() && wantType != "" && wantType != nodeapi.TypeFamily:
		resp.Diagnostics.AddAttributeError(path.Root("type"), "Invalid lookup",
			"A lookup by `protocol` returns a FAMILY root; leave `type` unset or set it to FAMILY.")
		return
	case !config.Protocol.IsNull():
		node, err = nodeapi.FindFamily(ctx, d.pd.Client, config.Protocol.ValueString())
	case config.ID.IsNull() && wantType == nodeapi.TypeSubnet:
		node, err = nodeapi.FindSubnetByCIDR(ctx, d.pd.Client, config.CIDR.ValueString())
	case config.ID.IsNull():
		node, err = nodeapi.ResolveCIDR(ctx, d.pd.Client, config.CIDR.ValueString(), wantType)
	case wantType != "":
		node, err = nodeapi.FetchOfType(ctx, d.pd.Client, config.ID.ValueInt64(), wantType)
	default:
		node, err = nodeapi.Fetch(ctx, d.pd.Client, config.ID.ValueInt64())
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the IPAM node", err) {
		return
	}
	state, diags := nodeapi.Flatten(ctx, *node)
	resp.Diagnostics.Append(diags...)
	state.CIDR = nodeapi.KeepConfiguredCIDR(config.CIDR, state.CIDR)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func Resources() []func() resource.Resource { return nil }
