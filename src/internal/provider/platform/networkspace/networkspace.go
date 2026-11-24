package networkspace

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

const (
	spaceFields = "id name description isDefault state available serverCount"

	spaceByIDQuery = `query NetworkSpaceByID($id: Int!) { networkSpaceById(id: $id) { ` + spaceFields + ` } }`

	spacesQuery = `query NetworkSpaces($pagination: PaginationInput) {
  networkSpacesPaginated(pagination: $pagination) {
    items { ` + spaceFields + ` }
    total
    hasMore
  }
}`
)

var states = []string{"PROVISIONING", "ACTIVE", "INACTIVE", "DELETING"}

type spaceDTO struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsDefault   bool    `json:"isDefault"`
	State       string  `json:"state"`
	Available   bool    `json:"available"`
	ServerCount *int64  `json:"serverCount"`
}

var itemType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":           types.Int64Type,
	"name":         types.StringType,
	"description":  types.StringType,
	"is_default":   types.BoolType,
	"state":        types.StringType,
	"available":    types.BoolType,
	"server_count": types.Int64Type,
}}

func (s spaceDTO) values() map[string]attr.Value {
	return map[string]attr.Value{
		"id": types.Int64Value(s.ID), "name": types.StringValue(s.Name), "description": types.StringPointerValue(s.Description),
		"is_default": types.BoolValue(s.IsDefault), "state": types.StringValue(s.State), "available": types.BoolValue(s.Available),
		"server_count": types.Int64PointerValue(s.ServerCount),
	}
}

func itemAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Network space id."},
		"name":         schema.StringAttribute{Computed: true, MarkdownDescription: "Network space name."},
		"description":  schema.StringAttribute{Computed: true, MarkdownDescription: "Description of the space."},
		"is_default":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is the default space."},
		"state":        schema.StringAttribute{Computed: true, MarkdownDescription: "`PROVISIONING`, `ACTIVE`, `INACTIVE` or `DELETING`."},
		"available":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the space is active and current, so it can be worked in."},
		"server_count": schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of servers in the space."},
	}
}

func fetchByID(ctx context.Context, c *client.Client, id int64) (*spaceDTO, error) {
	var out struct {
		Space *spaceDTO `json:"networkSpaceById"`
	}
	if err := c.Query(ctx, spaceByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Space == nil {
		return nil, client.NewNotFound("network space")
	}
	return out.Space, nil
}

func list(ctx context.Context, c *client.Client, name, state string) ([]spaceDTO, error) {
	vars := map[string]any{}
	if name != "" {
		vars["pagination"] = map[string]any{"filters": []map[string]any{{"field": "name", "operator": "EQ", "value": name}}}
	}
	all, err := client.PaginateField[spaceDTO](ctx, c, spacesQuery, "networkSpacesPaginated", vars, client.PageOptions{SortBy: "id"})
	if err != nil {
		return nil, err
	}
	matched := all[:0:0]
	for _, s := range all {
		if (name == "" || s.Name == name) && (state == "" || s.State == state) {
			matched = append(matched, s)
		}
	}
	return matched, nil
}

type singleModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
	State       types.String `tfsdk:"state"`
	Available   types.Bool   `tfsdk:"available"`
	ServerCount types.Int64  `tfsdk:"server_count"`
}

type listModel struct {
	Name          types.String `tfsdk:"name"`
	State         types.String `tfsdk:"state"`
	NetworkSpaces types.List   `tfsdk:"network_spaces"`
}

func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewDataSource, NewListDataSource}
}

type singleDataSource struct{ pd *framework.ProviderData }

var (
	_ datasource.DataSourceWithConfigure        = (*singleDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*singleDataSource)(nil)
)

func NewDataSource() datasource.DataSource { return &singleDataSource{} }

func (d *singleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_space"
}

func (d *singleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *singleDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *singleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := itemAttributes()
	attrs["id"] = schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Network space id. Set exactly one of `id` or `name`."}
	attrs["name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Exact network space name. Set exactly one of `id` or `name`."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a network space by `id` or exact `name`. Reading network spaces needs a platform role with `read_network_spaces`.",
		Attributes:          attrs,
	}
}

func (d *singleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config singleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var found *spaceDTO
	if !config.ID.IsNull() {
		dto, err := fetchByID(ctx, d.pd.Client, config.ID.ValueInt64())
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Network space not found", fmt.Sprintf("No network space has id %d.", config.ID.ValueInt64()))
			return
		}
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the network space", err) {
			return
		}
		found = dto
	} else {
		name := config.Name.ValueString()
		matches, err := list(ctx, d.pd.Client, name, "")
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to look up the network space", err) {
			return
		}
		if len(matches) != 1 {
			summary := "Network space not found"
			if len(matches) > 1 {
				summary = "Network space name is ambiguous"
			}
			resp.Diagnostics.AddError(summary, fmt.Sprintf("%d network spaces are named %q.", len(matches), name))
			return
		}
		found = &matches[0]
	}
	state := singleModel{
		ID: types.Int64Value(found.ID), Name: types.StringValue(found.Name), Description: types.StringPointerValue(found.Description),
		IsDefault: types.BoolValue(found.IsDefault), State: types.StringValue(found.State), Available: types.BoolValue(found.Available),
		ServerCount: types.Int64PointerValue(found.ServerCount),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

type listDataSource struct{ pd *framework.ProviderData }

var _ datasource.DataSourceWithConfigure = (*listDataSource)(nil)

func NewListDataSource() datasource.DataSource { return &listDataSource{} }

func (d *listDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_spaces"
}

func (d *listDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *listDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists network spaces, optionally narrowed by exact `name` and by `state`. Matching none is not an error. Needs a platform role with `read_network_spaces`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Optional: true, MarkdownDescription: "Return only the space with exactly this name."},
			"state": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only spaces in this state: `PROVISIONING`, `ACTIVE`, `INACTIVE` or `DELETING`.",
				Validators:          []validator.String{stringvalidator.OneOf(states...)},
			},
			"network_spaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching network spaces.",
				NestedObject:        schema.NestedAttributeObject{Attributes: itemAttributes()},
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
	matches, err := list(ctx, d.pd.Client, config.Name.ValueString(), config.State.ValueString())
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to list network spaces", err) {
		return
	}
	items := make([]attr.Value, 0, len(matches))
	for _, s := range matches {
		items = append(items, types.ObjectValueMust(itemType.AttrTypes, s.values()))
	}
	config.NetworkSpaces = types.ListValueMust(itemType, items)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
