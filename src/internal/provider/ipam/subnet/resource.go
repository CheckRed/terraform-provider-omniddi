package subnet

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	resourceTypeName = "omniddi_ipam_subnet"
	createPath       = "/ipam/subnets"
)

type resourceModel struct {
	ID                 types.Int64        `tfsdk:"id"`
	ParentID           types.Int64        `tfsdk:"parent_id"`
	NetworkAddr        iptypes.IPAddress  `tfsdk:"network_addr"`
	Prefix             types.Int64        `tfsdk:"prefix"`
	Exclude            types.Set          `tfsdk:"exclude"`
	Protocol           types.String       `tfsdk:"protocol"`
	Name               types.String       `tfsdk:"name"`
	Description        types.String       `tfsdk:"description"`
	Owner              types.String       `tfsdk:"owner"`
	VLAN               types.String       `tfsdk:"vlan"`
	VLANID             types.Int64        `tfsdk:"vlan_id"`
	BusinessUnitID     types.Int64        `tfsdk:"business_unit_id"`
	DeletionProtection types.Bool         `tfsdk:"deletion_protection"`
	ForceDestroy       types.Bool         `tfsdk:"force_destroy"`
	Tags               types.Map          `tfsdk:"tags"`
	TagsList           types.Map          `tfsdk:"tags_list"`
	TagsAll            types.Map          `tfsdk:"tags_all"`
	CIDR               cidrtypes.IPPrefix `tfsdk:"cidr"`
	Status             types.String       `tfsdk:"status"`
	CreatedOn          types.String       `tfsdk:"created_on"`
	UpdatedOn          types.String       `tfsdk:"updated_on"`
	Timeouts           timeouts.Value     `tfsdk:"timeouts"`
}

type subnetResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure      = (*subnetResource)(nil)
	_ resource.ResourceWithIdentity       = (*subnetResource)(nil)
	_ resource.ResourceWithImportState    = (*subnetResource)(nil)
	_ resource.ResourceWithValidateConfig = (*subnetResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*subnetResource)(nil)
)

func NewResource() resource.Resource { return &subnetResource{} }

func (r *subnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_subnet"
}

func (r *subnetResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *subnetResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
}

func (r *subnetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *subnetResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:                 idAttribute(),
		framework.AttrDeletionProtection: framework.DeletionProtectionAttribute(),
		framework.AttrForceDestroy:       forceDestroyAttribute(),
		framework.AttrTimeouts:           framework.TimeoutsAttribute(ctx),
		"parent_id": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "Id of the parent `FAMILY` or `CONTAINER` node. When omitted, the subnet is placed under the deepest container that holds its CIDR. " +
				"OmniDDI always places a subnet under the deepest containing container, so set this to the immediate parent. " +
				"Required, and must be a `CONTAINER`, when `network_addr` is not set. " +
				"Changing a configured value forces a new resource.",
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplaceIfConfigured()},
		},
		"network_addr": schema.StringAttribute{
			Optional:   true,
			Computed:   true,
			CustomType: iptypes.IPAddressType{},
			MarkdownDescription: "Network address of the subnet, for example `192.0.2.0` or `2001:db8:1::`. It must be the network address for `prefix`. " +
				"Leave it unset to allocate the next free `/prefix` block in the `parent_id` container; the allocated block then stays fixed. " +
				"Changing a configured value forces a new resource.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), nodeapi.RequiresReplaceIfAddressDiffers()},
		},
		"prefix": schema.Int64Attribute{
			Required:            true,
			MarkdownDescription: "Prefix length of the subnet (0-32 for IPv4, 0-128 for IPv6). Changing it forces a new resource.",
			Validators:          []validator.Int64{int64validator.Between(0, 128)},
			PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		},
		"exclude": schema.SetAttribute{
			Optional:    true,
			ElementType: cidrtypes.IPPrefixType{},
			MarkdownDescription: "CIDRs the allocated block must not overlap, at most 100. Only used when `network_addr` is not set, " +
				"and only when the block is allocated: changing it later does not move the subnet.",
			Validators: []validator.Set{setvalidator.SizeAtMost(100)},
		},
		"protocol": schema.StringAttribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "`IPv4` or `IPv6`. Derived from `network_addr` when not set and must match it. " +
				"Changing a configured value forces a new resource.",
			Validators:    []validator.String{stringvalidator.OneOf(nodeapi.ProtocolIPv4, nodeapi.ProtocolIPv6)},
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
		},
		"name": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Name of the subnet. Defaults to its CIDR.",
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"description": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Free-text description.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(1000)},
		},
		"owner": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Owner label.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(255)},
		},
		"vlan": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "VLAN label. Conflicts with `vlan_id`.",
			Validators:          []validator.String{stringvalidator.LengthAtMost(64), stringvalidator.ConflictsWith(path.MatchRoot("vlan_id"))},
		},
		"vlan_id": schema.Int64Attribute{
			Optional:            true,
			MarkdownDescription: "Numeric VLAN id, stored by OmniDDI as the VLAN label. Conflicts with `vlan`.",
			Validators:          []validator.Int64{int64validator.Between(0, 4094)},
		},
		"business_unit_id": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "Business unit of the subnet. Inherited from the parent when not set. " +
				"Changing a configured value forces a new resource.",
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplaceIfConfigured()},
		},
		"cidr": schema.StringAttribute{
			Computed:            true,
			CustomType:          cidrtypes.IPPrefixType{},
			MarkdownDescription: "The subnet in CIDR notation.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
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
		MarkdownDescription: "Manages a planning-only IPAM subnet. Writes are synchronous. DHCP service is enabled separately; " +
			"this resource never sends a DHCP server group. The `id` equals the OmniDDI network id used by DHCP resources. " +
			"This resource owns the `SUBNET` tags of the subnet.",
		Attributes: attrs,
	}
}

func idAttribute() schema.Int64Attribute {
	attr := framework.IDAttribute()
	attr.MarkdownDescription = "The OmniDDI network id of the subnet (`networks.id`)."
	return attr
}

func forceDestroyAttribute() schema.BoolAttribute {
	attr := framework.ForceDestroyAttribute()
	attr.MarkdownDescription = "Allow deleting a subnet that has DHCP service configured, which removes its DHCP configuration, " +
		"reservations and leases together with the subnet. Defaults to `false`."
	return attr
}

func (r *subnetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var (
		addr     iptypes.IPAddress
		prefix   types.Int64
		protocol types.String
	)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("network_addr"), &addr)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("prefix"), &prefix)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("protocol"), &protocol)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateAllocation(ctx, req, resp, addr)
	if resp.Diagnostics.HasError() || addr.IsNull() || addr.IsUnknown() {
		return
	}
	ip, err := netip.ParseAddr(addr.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("network_addr"), "Invalid network address", err.Error())
		return
	}
	if !protocol.IsNull() && !protocol.IsUnknown() && protocol.ValueString() != protocolOf(ip) {
		resp.Diagnostics.AddAttributeError(path.Root("protocol"), "Protocol does not match network_addr",
			fmt.Sprintf("network_addr %s is %s but protocol is %s.", ip, protocolOf(ip), protocol.ValueString()))
	}
	if prefix.IsNull() || prefix.IsUnknown() {
		return
	}
	if err := nodeapi.CheckNetworkAddress(ip, int(prefix.ValueInt64())); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("network_addr"), "Invalid subnet", err.Error())
	}
}

func protocolOf(ip netip.Addr) string {
	if ip.Is4() {
		return nodeapi.ProtocolIPv4
	}
	return nodeapi.ProtocolIPv6
}

func (r *subnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, func(ctx context.Context, key string) (int64, error) {
		node, err := nodeapi.FindSubnetByCIDR(ctx, r.pd.Client, key)
		if err != nil {
			return 0, err
		}
		return node.ID, nil
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(framework.ImportDefaults(ctx, &resp.State, framework.AttrDeletionProtection, framework.AttrForceDestroy)...)
}

func (m resourceModel) cidr() string {
	return fmt.Sprintf("%s/%d", m.NetworkAddr.ValueString(), m.Prefix.ValueInt64())
}

func vlanLabel(m resourceModel) (string, bool) {
	switch {
	case !m.VLANID.IsNull() && !m.VLANID.IsUnknown():
		return strconv.FormatInt(m.VLANID.ValueInt64(), 10), true
	case !m.VLAN.IsNull() && !m.VLAN.IsUnknown():
		return m.VLAN.ValueString(), true
	}
	return "", false
}

func createBody(m resourceModel) map[string]any {
	body := map[string]any{"network_addr": m.NetworkAddr.ValueString(), "prefix": m.Prefix.ValueInt64()}
	if !m.ParentID.IsNull() && !m.ParentID.IsUnknown() {
		body["parent_id"] = m.ParentID.ValueInt64()
	}
	if !m.Protocol.IsNull() && !m.Protocol.IsUnknown() {
		body["protocol"] = m.Protocol.ValueString()
	} else if ip, err := netip.ParseAddr(m.NetworkAddr.ValueString()); err == nil {
		body["protocol"] = protocolOf(ip)
	}
	if !m.BusinessUnitID.IsNull() && !m.BusinessUnitID.IsUnknown() {
		body["business_unit_id"] = m.BusinessUnitID.ValueInt64()
	}
	nodeapi.SetIfConfigured(body, "name", m.Name)
	nodeapi.SetIfConfigured(body, "description", m.Description)
	nodeapi.SetIfConfigured(body, "owner", m.Owner)
	if label, ok := vlanLabel(m); ok {
		body["vlan"] = label
	}
	return body
}

func updateBody(m resourceModel) map[string]any {
	label, _ := vlanLabel(m)
	body := map[string]any{
		"description": nodeapi.ClearableString(m.Description),
		"owner":       nodeapi.ClearableString(m.Owner),
		"vlan":        label,
	}
	nodeapi.SetIfConfigured(body, "name", m.Name)
	return body
}

func (m *resourceModel) deriveComputed() {
	if m.CIDR.IsUnknown() {
		m.CIDR = cidrtypes.NewIPPrefixValue(m.cidr())
	}
	if m.Protocol.IsUnknown() {
		m.Protocol = types.StringNull()
		if ip, err := netip.ParseAddr(m.NetworkAddr.ValueString()); err == nil {
			m.Protocol = types.StringValue(protocolOf(ip))
		}
	}
	if m.Name.IsUnknown() {
		m.Name = types.StringValue(m.cidr())
	}
}

func (r *subnetResource) readNode(ctx context.Context, m *resourceModel) error {
	id := m.ID.ValueInt64()
	node, err := nodeapi.FetchOfType(ctx, r.pd.Client, id, nodeapi.TypeSubnet)
	if err != nil {
		return err
	}
	parent, err := nodeapi.ReconcileParent(ctx, r.pd.Client, m.ParentID, node)
	if err != nil {
		return err
	}
	prefix, ok := node.Prefix()
	if !ok {
		return fmt.Errorf("IP space node %d has no valid CIDR", id)
	}
	m.ParentID = parent
	m.NetworkAddr = iptypes.NewIPAddressValue(prefix.Addr().String())
	m.Prefix = types.Int64Value(int64(prefix.Bits()))
	m.CIDR = cidrtypes.NewIPPrefixValue(prefix.String())
	m.Protocol = nodeapi.StringOrNull(node.Protocol)
	m.Name = types.StringValue(node.Label)
	m.Description = nodeapi.OptionalString(node.Description, m.Description)
	m.Owner = nodeapi.OptionalString(node.Owner, m.Owner)
	m.VLAN, m.VLANID = nodeapi.VLANFromAPI(node.VLAN, m.VLAN, m.VLANID)
	m.BusinessUnitID = nodeapi.Int64OrNull(node.BusinessUnitID)
	m.Status = nodeapi.StringOrNull(node.Status)
	m.CreatedOn = nodeapi.StringOrNull(node.CreatedOn)
	m.UpdatedOn = nodeapi.StringOrNull(node.UpdatedOn)
	return nil
}

func (r *subnetResource) refresh(ctx context.Context, m *resourceModel) error {
	if err := r.readNode(ctx, m); err != nil {
		return err
	}
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectSubnet, m.ID.ValueInt64())
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

func (r *subnetResource) settleAfterApply(ctx context.Context, m *resourceModel) error {
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

func (r *subnetResource) applyTags(ctx context.Context, m resourceModel) error {
	desired, diags := r.pd.DesiredTags(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	return r.pd.Client.ApplyTags(ctx, client.TagObjectSubnet, m.ID.ValueInt64(), desired)
}

func (r *subnetResource) idByCIDR(ctx context.Context, cidr string) (int64, error) {
	node, err := nodeapi.FindSubnetByCIDR(ctx, r.pd.Client, cidr)
	if err != nil {
		return 0, err
	}
	return node.ID, nil
}

func (r *subnetResource) recoverByCIDR(cidr string) func(context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := r.idByCIDR(ctx, cidr)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}

func (r *subnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()

	var (
		outcome *framework.WriteOutcome
		err     error
	)
	if plan.NetworkAddr.IsUnknown() {
		outcome, err = r.createInNextFreeBlock(ctx, &plan)
	} else {
		outcome, err = r.createAt(ctx, plan)
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to create IPAM subnet", err) {
		return
	}
	cidr := plan.cidr()
	resp.Diagnostics.Append(outcome.Warnings()...)
	id, err := outcome.ResolveID(ctx, func(ctx context.Context) (int64, error) { return r.idByCIDR(ctx, cidr) }, "id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the subnet id", err) {
		return
	}
	if parent, found := outcome.Result.Int("parent_id"); found && plan.ParentID.IsUnknown() {
		plan.ParentID = types.Int64Value(parent)
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
		framework.CreateFailedAfterSave(ctx, resp, "Subnet tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Subnet could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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

func (r *subnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to update IPAM subnet", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if err := r.applyTags(ctx, plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Subnet tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Subnet could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !framework.CheckDeletionProtection(&resp.Diagnostics, resourceTypeName, state.DeletionProtection) {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	id := state.ID.ValueInt64()

	if _, err := nodeapi.Fetch(ctx, r.pd.Client, id); client.IsNotFound(err) {
		return
	} else if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the subnet before deleting it", err) {
		return
	}
	hasDHCP, err := nodeapi.HasDHCPConfig(ctx, r.pd.Client, id)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to check the subnet's DHCP configuration", err) {
		return
	}
	if hasDHCP && !framework.CheckForceDestroy(&resp.Diagnostics, resourceTypeName, state.ForceDestroy,
		"DHCP service configured (its configuration, reservations and leases)") {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, types.StringNull())
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{Method: http.MethodDelete, Path: nodePath(id)})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete IPAM subnet", framework.IgnoreNotFound(err)) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
}

func nodePath(id int64) string { return createPath + "/" + strconv.FormatInt(id, 10) }

func validateAllocation(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse, addr iptypes.IPAddress) {
	var (
		parent  types.Int64
		exclude types.Set
	)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("parent_id"), &parent)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("exclude"), &exclude)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if addr.IsNull() && parent.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("network_addr"), "Missing subnet address",
			"Either set network_addr, or set parent_id to a container to allocate the next free block in it.")
	}
	if !addr.IsNull() && !exclude.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("exclude"), "exclude needs an allocated subnet",
			"exclude only applies when network_addr is not set, because it steers which free block is allocated.")
	}
}
