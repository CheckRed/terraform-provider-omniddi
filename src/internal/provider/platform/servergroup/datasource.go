package servergroup

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

type dataSourceModel struct {
	ID           types.Int64  `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Type         types.String `tfsdk:"type"`
	Description  types.String `tfsdk:"description"`
	DNSVendorID  types.Int64  `tfsdk:"dns_vendor_id"`
	DHCPVendorID types.Int64  `tfsdk:"dhcp_vendor_id"`
	ScopeID      types.Int64  `tfsdk:"scope_id"`
	Protocol     types.String `tfsdk:"protocol"`
	Servers      types.Set    `tfsdk:"servers"`
	Tags         types.Map    `tfsdk:"tags"`
	TagsList     types.Map    `tfsdk:"tags_list"`
}

type groupDataSource struct {
	pd *framework.ProviderData
}

var (
	_ datasource.DataSourceWithConfigure        = (*groupDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*groupDataSource)(nil)
)

func NewDataSource() datasource.DataSource { return &groupDataSource{} }

func (d *groupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_group"
}

func (d *groupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *groupDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one DNS or DHCP server group by `id`, or by `name` (add `type` when both a DNS and a DHCP group share the name).",
		Attributes: map[string]schema.Attribute{
			"id":   schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Server group id. Set exactly one of `id` or `name`."},
			"name": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Exact server group name. Set exactly one of `id` or `name`."},
			"type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Group type, `dns` or `dhcp`. Narrows a lookup by `name`.",
				Validators:          []validator.String{stringvalidator.OneOf(typeDNS, typeDHCP)},
			},
			"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description of the group."},
			"dns_vendor_id":  schema.Int64Attribute{Computed: true, MarkdownDescription: "DNS vendor id of a `dns` group."},
			"dhcp_vendor_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "DHCP vendor id of a `dhcp` group."},
			"scope_id":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Protocol scope id of a `dhcp` group."},
			"protocol":       schema.StringAttribute{Computed: true, MarkdownDescription: "Protocol served by a `dhcp` group: `IPv4`, `IPv6` or `Both`."},
			"servers": schema.SetNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Member servers.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"server_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Server id."},
					"role":      schema.StringAttribute{Computed: true, MarkdownDescription: "Lower-case role of the server in the group."},
				}},
			},
			"tags":      schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Single-valued tags on the group."},
			"tags_list": schema.MapAttribute{Computed: true, ElementType: types.ListType{ElemType: types.StringType}, MarkdownDescription: "Multi-valued tags on the group."},
		},
	}
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config dataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := config.ID.ValueInt64()
	if config.ID.IsNull() {
		found, err := findGroupID(ctx, d.pd.Client, config.Type.ValueString(), config.Name.ValueString())
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Server group not found", fmt.Sprintf("No server group is named %q.", config.Name.ValueString()))
			return
		}
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to look up the server group", err) {
			return
		}
		id = found
	}
	dto, err := fetchGroup(ctx, d.pd.Client, id)
	if client.IsNotFound(err) {
		resp.Diagnostics.AddError("Server group not found", fmt.Sprintf("No server group has id %d.", id))
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the server group", err) {
		return
	}
	if !config.Type.IsNull() && config.Type.ValueString() != dto.Type {
		resp.Diagnostics.AddError("Server group type mismatch", fmt.Sprintf("Server group %d is a %s group, not %s.", dto.ID, dto.Type, config.Type.ValueString()))
		return
	}
	state, err := d.state(ctx, dto)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the server group", err) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (d *groupDataSource) state(ctx context.Context, dto *groupDTO) (*dataSourceModel, error) {
	servers, diags := membersSet(ctx, dto.Servers)
	if diags.HasError() {
		return nil, fmt.Errorf("map servers: %s", diags)
	}
	remote, err := d.pd.Client.GetTagValues(ctx, dto.tagObject(), dto.ID)
	if err != nil {
		return nil, err
	}
	single, multi := map[string]attr.Value{}, map[string]attr.Value{}
	for _, tag := range remote {
		if len(tag.Values) == 0 {
			continue
		}
		if !tag.MultiValue {
			single[tag.Name] = types.StringValue(tag.Values[0])
			continue
		}
		elems := make([]attr.Value, 0, len(tag.Values))
		for _, v := range tag.Values {
			elems = append(elems, types.StringValue(v))
		}
		multi[tag.Name] = types.ListValueMust(types.StringType, elems)
	}
	return &dataSourceModel{
		ID:           types.Int64Value(dto.ID),
		Name:         types.StringValue(dto.Name),
		Type:         types.StringValue(dto.Type),
		Description:  types.StringPointerValue(dto.Description),
		DNSVendorID:  types.Int64PointerValue(dto.DNSVendorID),
		DHCPVendorID: types.Int64PointerValue(dto.DHCPVendorID),
		ScopeID:      types.Int64PointerValue(dto.scopeID()),
		Protocol:     types.StringPointerValue(dto.Protocol),
		Servers:      servers,
		Tags:         types.MapValueMust(types.StringType, single),
		TagsList:     types.MapValueMust(types.ListType{ElemType: types.StringType}, multi),
	}, nil
}
