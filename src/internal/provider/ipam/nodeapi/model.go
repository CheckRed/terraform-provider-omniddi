package nodeapi

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type DataModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Type           types.String `tfsdk:"type"`
	ParentID       types.Int64  `tfsdk:"parent_id"`
	Name           types.String `tfsdk:"name"`
	CIDR           types.String `tfsdk:"cidr"`
	Protocol       types.String `tfsdk:"protocol"`
	PrefixLength   types.Int64  `tfsdk:"prefix_length"`
	Status         types.String `tfsdk:"status"`
	VLAN           types.String `tfsdk:"vlan"`
	Owner          types.String `tfsdk:"owner"`
	Description    types.String `tfsdk:"description"`
	BusinessUnitID types.Int64  `tfsdk:"business_unit_id"`
	HasChildren    types.Bool   `tfsdk:"has_children"`
	CreatedOn      types.String `tfsdk:"created_on"`
	UpdatedOn      types.String `tfsdk:"updated_on"`
	Tags           types.Map    `tfsdk:"tags"`
	TagsList       types.Map    `tfsdk:"tags_list"`
}

var listType = types.ListType{ElemType: types.StringType}

func DataAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id": types.Int64Type, "type": types.StringType, "parent_id": types.Int64Type, "name": types.StringType,
		"cidr": types.StringType, "protocol": types.StringType, "prefix_length": types.Int64Type,
		"status": types.StringType, "vlan": types.StringType, "owner": types.StringType,
		"description": types.StringType, "business_unit_id": types.Int64Type,
		"has_children": types.BoolType, "created_on": types.StringType, "updated_on": types.StringType,
		"tags": types.MapType{ElemType: types.StringType}, "tags_list": types.MapType{ElemType: listType},
	}
}

func DataObjectType() types.ObjectType { return types.ObjectType{AttrTypes: DataAttrTypes()} }

func DataAttributes(idLookup, cidrLookup, typeLookup bool) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":               schema.Int64Attribute{Optional: idLookup, Computed: true, MarkdownDescription: "The node id. For a subnet this equals the `networks.id` used by DHCP resources."},
		"type":             schema.StringAttribute{Optional: typeLookup, Computed: true, MarkdownDescription: "Node type: `ROOT`, `FAMILY`, `CONTAINER` or `SUBNET`."},
		"parent_id":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Id of the parent node."},
		"name":             schema.StringAttribute{Computed: true, MarkdownDescription: "Node label. Defaults to the CIDR when no name was given."},
		"cidr":             schema.StringAttribute{Optional: cidrLookup, Computed: true, MarkdownDescription: "The network in CIDR notation, such as `10.0.0.0/24`."},
		"protocol":         schema.StringAttribute{Computed: true, MarkdownDescription: "`IPv4` or `IPv6`."},
		"prefix_length":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Prefix length of the network."},
		"status":           schema.StringAttribute{Computed: true, MarkdownDescription: "`ACTIVE`, `INACTIVE` or `DEPRECATED`."},
		"vlan":             schema.StringAttribute{Computed: true, MarkdownDescription: "VLAN label."},
		"owner":            schema.StringAttribute{Computed: true, MarkdownDescription: "Owner label."},
		"description":      schema.StringAttribute{Computed: true, MarkdownDescription: "Free-text description."},
		"business_unit_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Business unit the node belongs to (own or inherited)."},
		"has_children":     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the node has child networks."},
		"created_on":       schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
		"updated_on":       schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp."},
		"tags":             schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Single-valued tags on the node (inherited values excluded when reported as inherited)."},
		"tags_list":        schema.MapAttribute{Computed: true, ElementType: listType, MarkdownDescription: "Multi-valued tags on the node."},
	}
}

func Flatten(ctx context.Context, n Node) (DataModel, diag.Diagnostics) {
	single, multi := SplitTags(n.Tags)
	tags, d1 := types.MapValueFrom(ctx, types.StringType, single)
	tagsList, d2 := types.MapValueFrom(ctx, listType, multi)
	var diags diag.Diagnostics
	diags.Append(d1...)
	diags.Append(d2...)
	return DataModel{
		ID:             types.Int64Value(n.ID),
		Type:           types.StringValue(n.Type),
		ParentID:       Int64OrNull(n.ParentID),
		Name:           types.StringValue(n.Label),
		CIDR:           StringOrNull(n.CIDR),
		Protocol:       StringOrNull(n.Protocol),
		PrefixLength:   Int64OrNull(n.PrefixLength),
		Status:         StringOrNull(n.Status),
		VLAN:           OptionalString(n.VLAN, types.StringNull()),
		Owner:          OptionalString(n.Owner, types.StringNull()),
		Description:    OptionalString(n.Description, types.StringNull()),
		BusinessUnitID: Int64OrNull(n.BusinessUnitID),
		HasChildren:    types.BoolValue(n.HasChildren),
		CreatedOn:      StringOrNull(n.CreatedOn),
		UpdatedOn:      StringOrNull(n.UpdatedOn),
		Tags:           tags,
		TagsList:       tagsList,
	}, diags
}

func SplitTags(raw map[string]any) (map[string]string, map[string][]string) {
	single, multi := map[string]string{}, map[string][]string{}
	for name, value := range raw {
		if name == client.InheritedTagKey {
			continue
		}
		if list, ok := value.([]any); ok {
			items := make([]string, 0, len(list))
			for _, item := range list {
				items = append(items, fmt.Sprint(item))
			}
			multi[name] = items
			continue
		}
		if value != nil {
			single[name] = fmt.Sprint(value)
		}
	}
	return single, multi
}

func MatchesTags(raw map[string]any, want map[string]string) bool {
	single, multi := SplitTags(raw)
	for name, value := range want {
		if got, ok := single[name]; ok && got == value {
			continue
		}
		if list, ok := multi[name]; ok && slices.Contains(list, value) {
			continue
		}
		return false
	}
	return true
}

func FlattenList(ctx context.Context, nodes []Node) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	items := make([]DataModel, 0, len(nodes))
	for _, n := range nodes {
		item, d := Flatten(ctx, n)
		diags.Append(d...)
		items = append(items, item)
	}
	list, d := types.ListValueFrom(ctx, DataObjectType(), items)
	diags.Append(d...)
	return list, diags
}

func KeepConfiguredCIDR(configured, fetched types.String) types.String {
	if configured.IsNull() || configured.IsUnknown() || fetched.IsNull() {
		return fetched
	}
	want, err1 := ParsePrefix(configured.ValueString())
	got, err2 := ParsePrefix(fetched.ValueString())
	if err1 == nil && err2 == nil && want == got {
		return configured
	}
	return fetched
}
