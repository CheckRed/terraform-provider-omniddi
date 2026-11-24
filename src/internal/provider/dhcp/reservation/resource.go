package reservation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

const (
	reservationsPath = "/dhcp/reservations"
	actionCreate     = "create_host_reservation"
	actionUpdate     = "update_host_reservation"
	actionDelete     = "delete_host_reservation"
)

type model struct {
	ID             types.Int64       `tfsdk:"id"`
	Name           types.String      `tfsdk:"name"`
	IPAddress      iptypes.IPAddress `tfsdk:"ip_address"`
	SubnetID       types.Int64       `tfsdk:"subnet_id"`
	IdentifierType types.String      `tfsdk:"identifier_type"`
	Identifier     types.String      `tfsdk:"identifier"`
	Description    types.String      `tfsdk:"description"`
	Options        types.List        `tfsdk:"options"`
	IsDHCP6        types.Bool        `tfsdk:"is_dhcp6"`
	SubnetCIDR     types.String      `tfsdk:"subnet_cidr"`
	ServerGroupID  types.Int64       `tfsdk:"server_group_id"`
	ApprovalMode   types.String      `tfsdk:"approval_mode"`
	Tags           types.Map         `tfsdk:"tags"`
	TagsList       types.Map         `tfsdk:"tags_list"`
	TagsAll        types.Map         `tfsdk:"tags_all"`
	Timeouts       timeouts.Value    `tfsdk:"timeouts"`
}

type reservationResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure      = (*reservationResource)(nil)
	_ resource.ResourceWithIdentity       = (*reservationResource)(nil)
	_ resource.ResourceWithImportState    = (*reservationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*reservationResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*reservationResource)(nil)
)

func NewResource() resource.Resource { return &reservationResource{} }

func Resources() []func() resource.Resource { return []func() resource.Resource{NewResource} }

func (r *reservationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_host_reservation"
}

func (r *reservationResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *reservationResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:           framework.IDAttribute(),
		framework.AttrApprovalMode: framework.ApprovalModeAttribute(),
		framework.AttrTimeouts:     framework.TimeoutsAttribute(ctx),
		"name": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Reservation (host) name. OmniDDI requires it to be unique across all reservations.",
		},
		"ip_address": schema.StringAttribute{
			Required:   true,
			CustomType: iptypes.IPAddressType{},
			MarkdownDescription: "Reserved IPv4 or IPv6 address. It must lie inside the reservation's subnet; to move a reservation to " +
				"another subnet, set `subnet_id` (which replaces the reservation). Before creating or moving a reservation, the provider " +
				"checks that no other reservation holds the address and names the one that does.",
		},
		"subnet_id": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "ID of the subnet (IPAM node) holding the reservation. When omitted OmniDDI resolves it from `ip_address`. " +
				"Changing it replaces the reservation.",
			PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIfConfigured(), keepWhileAddressUnchanged{}},
		},
		"identifier_type": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Type of `identifier`: `Mac Address` or `DUID`. Leave both unset to reserve the address in IPAM only, until the client identifier is known.",
			Validators:          []validator.String{stringvalidator.OneOf(identifierMAC, identifierDUID), stringvalidator.AlsoRequires(path.MatchRoot("identifier"))},
		},
		"identifier": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Client identifier: a MAC address (`aa:bb:cc:dd:ee:ff`) or DUID (hex, colons allowed). OmniDDI stores MAC addresses in lower case " +
				"with `:` separators.",
			Validators: []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("identifier_type"))},
		},
		"description": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Free-text description.",
		},
		"options": dhcpkit.OptionsAttribute("DHCP options handed to this host. OmniDDI does not return reservation options on read, so " +
			"drift in them is not detected and an imported reservation shows them as unset."),
		"is_dhcp6": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether the reservation is for DHCPv6.",
			PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		},
		"subnet_cidr": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "CIDR of the DHCP subnet holding the reservation. Unset when the subnet is IPAM-only.",
			PlanModifiers:       []planmodifier.String{keepWhileAddressUnchanged{}},
		},
		"server_group_id": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "ID of the DHCP server group serving the reservation's subnet.",
			PlanModifiers:       []planmodifier.Int64{keepWhileAddressUnchanged{}},
		},
	}
	for name, attr := range framework.TagsAttributes() {
		attrs[name] = attr
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A DHCP host reservation: a fixed address, optionally bound to a MAC address or DUID. " +
			"Changes may need approval in OmniDDI; see `approval_mode`.",
		Attributes: attrs,
	}
}

func (r *reservationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *reservationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var idType, identifier types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("identifier_type"), &idType)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("identifier"), &identifier)...)
	if resp.Diagnostics.HasError() || idType.IsNull() || idType.IsUnknown() || identifier.IsNull() || identifier.IsUnknown() {
		return
	}
	pattern, label := macPattern, "MAC address"
	if idType.ValueString() == identifierDUID {
		pattern, label = duidPattern, "DUID"
	}
	if !pattern.MatchString(identifier.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("identifier"), "Invalid identifier", fmt.Sprintf("%q is not a valid %s.", identifier.ValueString(), label))
	}
}

func (r *reservationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.IPAddress.IsNull() || plan.IPAddress.IsUnknown() || state.SubnetCIDR.IsNull() || state.SubnetCIDR.IsUnknown() {
		return
	}
	if !plan.SubnetID.IsNull() && !plan.SubnetID.IsUnknown() && !plan.SubnetID.Equal(state.SubnetID) {
		return
	}
	prefix, perr := netip.ParsePrefix(state.SubnetCIDR.ValueString())
	ip, ierr := netip.ParseAddr(plan.IPAddress.ValueString())
	if perr == nil && ierr == nil && !prefix.Contains(ip) {
		resp.Diagnostics.AddAttributeError(path.Root("ip_address"), "Address is outside the reservation's subnet",
			fmt.Sprintf("%s is not inside %s. A reservation cannot change subnet in place; set subnet_id to the target subnet to replace it.", ip, prefix))
	}
}

func (r *reservationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, r.idByKey)
}

func (r *reservationResource) idByKey(ctx context.Context, key string) (int64, error) {
	cidr, name := parseImportKey(key)
	items, err := findAll(ctx, r.pd.Client, []map[string]any{dhcpkit.EqFilter("name", name)}, nil)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if item.Name == name && (cidr == "" || sameSubnet(item.Subnet, cidr)) {
			return item.ID, nil
		}
	}
	return 0, client.NewNotFound("host reservation")
}

func (r *reservationResource) ensureAddressFree(ctx context.Context, m model, selfID int64) error {
	ip := canonicalIP(m.IPAddress.ValueString())
	holders, err := findAll(ctx, r.pd.Client, []map[string]any{dhcpkit.EqFilter("ip_address", ip)}, nil)
	if err != nil {
		return err
	}
	for _, h := range holders {
		if h.ID != selfID && h.IPAddress != nil && canonicalIP(*h.IPAddress) == ip {
			return &addressTakenError{holder: h.Name, holderID: h.ID, ip: ip}
		}
	}
	return nil
}

type addressTakenError struct {
	holder   string
	holderID int64
	ip       string
}

func (e *addressTakenError) Error() string {
	return fmt.Sprintf("host reservation %q (id %d) already holds %s; OmniDDI would accept a second reservation on the same address, "+
		"so the provider refuses. Import the existing reservation or choose another address", e.holder, e.holderID, e.ip)
}

func (r *reservationResource) applyRemote(ctx context.Context, m *model) error {
	d, err := fetchByID(ctx, r.pd.Client, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	m.Name = types.StringValue(d.Name)
	if d.IPAddress != nil {
		m.IPAddress = iptypes.NewIPAddressValue(*d.IPAddress)
	}
	m.SubnetID = types.Int64Value(d.SubnetID)
	m.IdentifierType = identifierTypeFromWire(d.IdentifierType)
	m.Identifier = identifierFromWire(d.Identifier, m.IdentifierType, m.Identifier)
	m.Description = dhcpkit.OptionalString(d.Description, m.Description)
	m.IsDHCP6 = types.BoolValue(d.IsDHCP6)
	m.SubnetCIDR = types.StringPointerValue(d.Subnet)
	m.ServerGroupID = dhcpkit.OptionalInt64(d.ServerGroupID)
	return nil
}

func (r *reservationResource) refresh(ctx context.Context, m *model) error {
	if err := r.applyRemote(ctx, m); err != nil {
		return err
	}
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectHostReserv, m.ID.ValueInt64())
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

func (r *reservationResource) settleAfterApply(ctx context.Context, m *model) error {
	if err := r.applyRemote(ctx, m); err != nil {
		return err
	}
	tags, diags := r.pd.TagsAfterApply(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func (r *reservationResource) applyTags(ctx context.Context, m model, diags *diag.Diagnostics) error {
	desired, d := r.pd.DesiredTags(ctx, m.Tags, m.TagsList)
	diags.Append(d...)
	return r.pd.Client.ApplyTags(ctx, client.TagObjectHostReserv, m.ID.ValueInt64(), desired)
}

func (r *reservationResource) body(ctx context.Context, m model, create bool) (map[string]any, diag.Diagnostics) {
	body := map[string]any{"name": m.Name.ValueString(), "ip_address": m.IPAddress.ValueString()}
	options, diags := dhcpkit.OptionsToWire(ctx, m.Options)
	if create {
		if !m.SubnetID.IsNull() && !m.SubnetID.IsUnknown() {
			body["subnet_id"] = m.SubnetID.ValueInt64()
		}
		if !m.IdentifierType.IsNull() {
			body["identifier_type"] = m.IdentifierType.ValueString()
			body["identifier"] = m.Identifier.ValueString()
		}
		if !m.Description.IsNull() {
			body["description"] = m.Description.ValueString()
		}
		if options != nil {
			body["options"] = options
		}
		return body, diags
	}
	body["description"] = m.Description.ValueStringPointer()
	body["identifier_type"] = m.IdentifierType.ValueStringPointer()
	body["identifier"] = m.Identifier.ValueStringPointer()
	body["dhcp_options"] = options
	return body, diags
}

func (r *reservationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)

	body, diags := r.body(ctx, plan, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: reservationsPath, Body: body,
		ActionType: actionCreate, Match: dhcpkit.MatchName(plan.Name.ValueString()),
		Recover:  dhcpkit.RecoverBy(r.idByName(plan.Name.ValueString())),
		Precheck: func(ctx context.Context) error { return r.ensureAddressFree(ctx, plan, 0) },
	})
	var taken *addressTakenError
	if errors.As(err, &taken) {
		resp.Diagnostics.AddError("Address already reserved", err.Error())
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to create host reservation", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	id, err := outcome.ResolveID(ctx, r.idByName(plan.Name.ValueString()), "id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the host reservation id", err) {
		return
	}

	plan.ID = types.Int64Value(id)
	plan.TagsAll, _ = r.pd.PlannedTagsAll(ctx, plan.Tags)
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, &plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Host reservation was created but not confirmed", err)
		return
	}
	if err := r.applyTags(ctx, plan, &resp.Diagnostics); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Host reservation tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Host reservation could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *reservationResource) idByName(name string) func(ctx context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		found, err := findByName(ctx, r.pd.Client, name)
		if err != nil {
			return 0, err
		}
		return found.ID, nil
	}
}

func (r *reservationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state model
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

func (r *reservationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpUpdate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)
	id := plan.ID.ValueInt64()

	if !plan.IPAddress.Equal(prior.IPAddress) {
		if err := r.ensureAddressFree(ctx, plan, id); framework.AppendIfErr(&resp.Diagnostics, "Address already reserved", err) {
			return
		}
	}
	body, diags := r.body(ctx, plan, false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: reservationsPath + "/" + strconv.FormatInt(id, 10), Body: body,
		ActionType: actionUpdate, Match: dhcpkit.MatchID("host_reservation_id", id),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to update host reservation", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Host reservation update was not confirmed", err)
		return
	}
	if err := r.applyTags(ctx, plan, &resp.Diagnostics); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Host reservation tags could not be applied", err)
		return
	}
	if err := r.settleAfterApply(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Host reservation could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *reservationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, state.ApprovalMode)
	id := state.ID.ValueInt64()

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodDelete, Path: reservationsPath + "/" + strconv.FormatInt(id, 10),
		ActionType: actionDelete, Match: dhcpkit.MatchID("host_reservation_id", id),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete host reservation", framework.IgnoreNotFound(err)) || outcome == nil {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if framework.AppendIfErr(&resp.Diagnostics, "Host reservation deletion was not confirmed", lifecycle.Converge(ctx, outcome)) {
		return
	}
	err = lifecycle.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		_, fetchErr := fetchByID(ctx, r.pd.Client, id)
		if client.IsNotFound(fetchErr) {
			return false, nil
		}
		return fetchErr == nil, fetchErr
	})
	framework.AppendIfErr(&resp.Diagnostics, "Host reservation still exists after deletion", err)
}
