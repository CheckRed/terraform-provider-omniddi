package container

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const (
	resourceTypeName = "omniddi_ipam_container"
	createPath       = "/ipam/containers"
)

type resourceModel struct {
	ID                       types.Int64        `tfsdk:"id"`
	ParentID                 types.Int64        `tfsdk:"parent_id"`
	Name                     types.String       `tfsdk:"name"`
	CIDR                     cidrtypes.IPPrefix `tfsdk:"cidr"`
	Description              types.String       `tfsdk:"description"`
	VLAN                     types.String       `tfsdk:"vlan"`
	Owner                    types.String       `tfsdk:"owner"`
	BusinessUnitID           types.Int64        `tfsdk:"business_unit_id"`
	ReparentChildrenOnDelete types.Bool         `tfsdk:"reparent_children_on_destroy"`
	DeletionProtection       types.Bool         `tfsdk:"deletion_protection"`
	ForceDestroy             types.Bool         `tfsdk:"force_destroy"`
	Tags                     types.Map          `tfsdk:"tags"`
	TagsList                 types.Map          `tfsdk:"tags_list"`
	TagsAll                  types.Map          `tfsdk:"tags_all"`
	Protocol                 types.String       `tfsdk:"protocol"`
	PrefixLength             types.Int64        `tfsdk:"prefix_length"`
	Status                   types.String       `tfsdk:"status"`
	CreatedOn                types.String       `tfsdk:"created_on"`
	UpdatedOn                types.String       `tfsdk:"updated_on"`
	Timeouts                 timeouts.Value     `tfsdk:"timeouts"`
}

type containerResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*containerResource)(nil)
	_ resource.ResourceWithIdentity    = (*containerResource)(nil)
	_ resource.ResourceWithImportState = (*containerResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*containerResource)(nil)
)

func NewResource() resource.Resource { return &containerResource{} }

func (r *containerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_container"
}

func (r *containerResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *containerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
}

func (r *containerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *containerResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:                 framework.IDAttribute(),
		framework.AttrDeletionProtection: framework.DeletionProtectionAttribute(),
		framework.AttrForceDestroy:       forceDestroyAttribute(),
		framework.AttrTimeouts:           framework.TimeoutsAttribute(ctx),
		"parent_id": schema.Int64Attribute{
			Required: true,
			MarkdownDescription: "Id of the parent `FAMILY` or `CONTAINER` node (see the `omniddi_ipam_node` data source). " +
				"OmniDDI places a container under the deepest container that holds its CIDR, so use the immediate parent. " +
				"Changing it forces a new resource.",
			PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		},
		"name": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Name of the container.",
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
		},
		"cidr": schema.StringAttribute{
			Required:   true,
			CustomType: cidrtypes.IPPrefixType{},
			MarkdownDescription: "The address block the container covers, as a CIDR whose address is the network address, " +
				"for example `10.0.0.0/16` or `2001:db8::/32`. It must sit inside the parent and cannot be a single host. " +
				"Changing it forces a new resource.",
			Validators:    []validator.String{nodeapi.NetworkCIDR()},
			PlanModifiers: []planmodifier.String{nodeapi.RequiresReplaceIfPrefixDiffers()},
		},
		"description": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Free-text description.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(1000)},
		},
		"vlan": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "VLAN label.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(64)},
		},
		"owner": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Owner label.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(255)},
		},
		"business_unit_id": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "Business unit of the container. Inherited from the parent when not set. " +
				"Changing a configured value forces a new resource.",
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplaceIfConfigured()},
		},
		"reparent_children_on_destroy": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
			MarkdownDescription: "When true, deleting the container moves its direct child networks to the container's own parent " +
				"instead of being refused. Children are never deleted. Defaults to `false`.",
		},
		"protocol": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "`IPv4` or `IPv6`.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"prefix_length": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Prefix length of the container.",
			PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		},
		"status": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "`ACTIVE`, `INACTIVE` or `DEPRECATED`.",
		},
		"created_on": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Creation timestamp.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"updated_on": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Last update timestamp.",
		},
	}
	for name, attr := range framework.TagsAttributes() {
		attrs[name] = attr
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an IPAM container: an address block that groups subnets and other containers. " +
			"Writes are synchronous. Creating a container adopts any existing networks that fall inside its CIDR.",
		Attributes: attrs,
	}
}

func forceDestroyAttribute() schema.BoolAttribute {
	attr := framework.ForceDestroyAttribute()
	attr.MarkdownDescription = "Skip the provider's check that refuses to delete a container that still has child networks. " +
		"OmniDDI itself still refuses unless `reparent_children_on_destroy` is set, and never deletes children. Defaults to `false`."
	return attr
}

func (r *containerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, func(ctx context.Context, key string) (int64, error) {
		node, err := nodeapi.ResolveCIDR(ctx, r.pd.Client, key, nodeapi.TypeContainer)
		if err != nil {
			return 0, err
		}
		return node.ID, nil
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(framework.ImportDefaults(ctx, &resp.State, framework.AttrDeletionProtection, framework.AttrForceDestroy)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("reparent_children_on_destroy"), false)...)
}

func (r *containerResource) createBody(m resourceModel) map[string]any {
	body := map[string]any{"parent_id": m.ParentID.ValueInt64(), "name": m.Name.ValueString(), "cidr": m.CIDR.ValueString()}
	nodeapi.SetIfConfigured(body, "description", m.Description)
	nodeapi.SetIfConfigured(body, "vlan", m.VLAN)
	nodeapi.SetIfConfigured(body, "owner", m.Owner)
	if !m.BusinessUnitID.IsNull() && !m.BusinessUnitID.IsUnknown() {
		body["business_unit_id"] = m.BusinessUnitID.ValueInt64()
	}
	return body
}

func updateBody(m resourceModel) map[string]any {
	return map[string]any{
		"name":        m.Name.ValueString(),
		"description": nodeapi.ClearableString(m.Description),
		"vlan":        nodeapi.ClearableString(m.VLAN),
		"owner":       nodeapi.ClearableString(m.Owner),
	}
}

func (m *resourceModel) deriveComputed() {
	if !m.Protocol.IsUnknown() && !m.PrefixLength.IsUnknown() {
		return
	}
	m.Protocol, m.PrefixLength = types.StringNull(), types.Int64Null()
	if prefix, err := nodeapi.ParsePrefix(m.CIDR.ValueString()); err == nil {
		m.Protocol, m.PrefixLength = types.StringValue(nodeapi.ProtocolOf(prefix)), types.Int64Value(int64(prefix.Bits()))
	}
}

func (r *containerResource) readNode(ctx context.Context, m *resourceModel) error {
	node, err := nodeapi.FetchOfType(ctx, r.pd.Client, m.ID.ValueInt64(), nodeapi.TypeContainer)
	if err != nil {
		return err
	}
	parent, err := nodeapi.ReconcileParent(ctx, r.pd.Client, m.ParentID, node)
	if err != nil {
		return err
	}
	m.ParentID = parent
	m.Name = types.StringValue(node.Label)
	m.CIDR = cidrtypes.NewIPPrefixNull()
	if node.CIDR != nil {
		m.CIDR = cidrtypes.NewIPPrefixValue(*node.CIDR)
	}
	m.Description = nodeapi.OptionalString(node.Description, m.Description)
	m.VLAN = nodeapi.OptionalString(node.VLAN, m.VLAN)
	m.Owner = nodeapi.OptionalString(node.Owner, m.Owner)
	m.BusinessUnitID = nodeapi.Int64OrNull(node.BusinessUnitID)
	m.Protocol = nodeapi.StringOrNull(node.Protocol)
	m.PrefixLength = nodeapi.Int64OrNull(node.PrefixLength)
	m.Status = nodeapi.StringOrNull(node.Status)
	m.CreatedOn = nodeapi.StringOrNull(node.CreatedOn)
	m.UpdatedOn = nodeapi.StringOrNull(node.UpdatedOn)
	return nil
}

func (r *containerResource) refresh(ctx context.Context, m *resourceModel) error {
	if err := r.readNode(ctx, m); err != nil {
		return err
	}
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectNetworkContain, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	tags, diags := r.pd.TagsFromRemote(ctx, remote, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func (r *containerResource) settleAfterApply(ctx context.Context, m *resourceModel) error {
	if err := r.readNode(ctx, m); err != nil {
		return err
	}
	tags, diags := r.pd.TagsAfterApply(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func (r *containerResource) applyTags(ctx context.Context, m resourceModel) error {
	desired, diags := r.pd.DesiredTags(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	return r.pd.Client.ApplyTags(ctx, client.TagObjectNetworkContain, m.ID.ValueInt64(), desired)
}

func (r *containerResource) idByCIDR(ctx context.Context, cidr string) (int64, error) {
	node, err := nodeapi.ResolveCIDR(ctx, r.pd.Client, cidr, nodeapi.TypeContainer)
	if err != nil {
		return 0, err
	}
	return node.ID, nil
}

func (r *containerResource) recoverByCIDR(cidr string) func(context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := r.idByCIDR(ctx, cidr)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}

func (r *containerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()

	cidr := plan.CIDR.ValueString()
	lifecycle := framework.NewLifecycle(r.pd, types.StringNull())
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: createPath, Body: r.createBody(plan), Recover: r.recoverByCIDR(cidr),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to create IPAM container", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	id, err := outcome.ResolveID(ctx, func(ctx context.Context) (int64, error) { return r.idByCIDR(ctx, cidr) }, "id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the container id", err) {
		return
	}

	plan.ID = types.Int64Value(id)
	plan.TagsAll, _ = r.pd.PlannedTagsAll(ctx, plan.Tags)
	plan.deriveComputed()
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, &plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.applyTags(ctx, plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Container tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Container could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *containerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpRead, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	if err := r.refresh(ctx, &state); err != nil {
		framework.HandleReadError(ctx, resp, err)
		return
	}
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, state.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *containerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpUpdate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()

	lifecycle := framework.NewLifecycle(r.pd, types.StringNull())
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: nodePath(plan.ID.ValueInt64()), Body: updateBody(plan),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to update IPAM container", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if err := r.applyTags(ctx, plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Container tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Container could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *containerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !framework.CheckDeletionProtection(&resp.Diagnostics, resourceTypeName, state.DeletionProtection) {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	id := state.ID.ValueInt64()
	reparent := state.ReparentChildrenOnDelete.ValueBool()

	node, err := nodeapi.Fetch(ctx, r.pd.Client, id)
	if client.IsNotFound(err) {
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the container before deleting it", err) {
		return
	}
	if node.HasChildren && !reparent {
		blockers, err := r.describeChildren(ctx, id)
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to list the container's children", err) ||
			!framework.CheckForceDestroy(&resp.Diagnostics, resourceTypeName, state.ForceDestroy, blockers) {
			return
		}
	}

	path := nodePath(id)
	if reparent {
		path += "?reparent_children=true"
	}
	lifecycle := framework.NewLifecycle(r.pd, types.StringNull())
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{Method: http.MethodDelete, Path: path})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete IPAM container", framework.IgnoreNotFound(err)) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
}

func (r *containerResource) describeChildren(ctx context.Context, id int64) (string, error) {
	children, err := nodeapi.Children(ctx, r.pd.Client, &id, "")
	if err != nil {
		return "", err
	}
	if len(children) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d child network(s) (set reparent_children_on_destroy = true to move them to the parent)", len(children)), nil
}

func nodePath(id int64) string { return createPath + "/" + strconv.FormatInt(id, 10) }
