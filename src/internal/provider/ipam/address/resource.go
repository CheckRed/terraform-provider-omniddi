package address

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	maxExclude      = 100
	maxNameLength   = 253
	maxBusyAttempts = 5
	recoveryTimeout = 30 * time.Second
)

type model struct {
	ID         types.Int64        `tfsdk:"id"`
	SubnetID   types.Int64        `tfsdk:"subnet_id"`
	Name       framework.Hostname `tfsdk:"name"`
	Exclude    types.Set          `tfsdk:"exclude"`
	IPAddress  iptypes.IPAddress  `tfsdk:"ip_address"`
	SubnetCIDR cidrtypes.IPPrefix `tfsdk:"subnet_cidr"`
	Status     types.String       `tfsdk:"status"`
	Timeouts   timeouts.Value     `tfsdk:"timeouts"`
}

type addressResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*addressResource)(nil)
	_ resource.ResourceWithIdentity    = (*addressResource)(nil)
	_ resource.ResourceWithImportState = (*addressResource)(nil)
)

func NewResource() resource.Resource { return &addressResource{} }

func (r *addressResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_address"
}

func (r *addressResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *addressResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *addressResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Allocates the next free IPv4 or IPv6 address in an IPAM subnet and records `name` on it. " +
			"OmniDDI picks the lowest free host address outside the subnet's DHCP pools, so parallel applies never get the same one. " +
			"The address stays the same across plans; changing `subnet_id` or `name` allocates a new one. " +
			"Pass `ip_address` to an `omniddi_dns_record` to publish it in DNS in the same apply. " +
			"OmniDDI has no API that releases an address, so destroying this resource only removes it from Terraform state " +
			"and the address stays allocated in OmniDDI. Some OmniDDI versions allocate only in subnets attached to a DHCP server group " +
			"and otherwise answer `SUBNET_NOT_ALLOCATABLE`.",
		Attributes: map[string]schema.Attribute{
			framework.AttrID:       idAttribute(),
			framework.AttrTimeouts: framework.TimeoutsAttribute(ctx),
			"subnet_id": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "ID of the IPAM subnet to allocate from (`omniddi_ipam_subnet.id`). Changing it allocates a new address.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:   true,
				CustomType: framework.HostnameType{},
				MarkdownDescription: "Hostname or FQDN recorded on the address in OmniDDI, compared without regard to case or a trailing dot. " +
					"Changing it allocates a new address. Later edits to the hostname in OmniDDI are not tracked.",
				Validators:    []validator.String{stringvalidator.LengthBetween(1, maxNameLength)},
				PlanModifiers: []planmodifier.String{replaceUnlessSameName{}},
			},
			"exclude": schema.SetAttribute{
				Optional:    true,
				ElementType: iptypes.IPAddressType{},
				MarkdownDescription: "Addresses never to allocate, at most 100, in the subnet's address family. " +
					"Used only when the address is allocated; changing it later does not move the address.",
				Validators: []validator.Set{setvalidator.SizeBetween(1, maxExclude)},
			},
			"ip_address": schema.StringAttribute{
				Computed:            true,
				CustomType:          iptypes.IPAddressType{},
				MarkdownDescription: "The allocated address.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"subnet_cidr": schema.StringAttribute{
				Computed:            true,
				CustomType:          cidrtypes.IPPrefixType{},
				MarkdownDescription: "CIDR of the subnet holding the address.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Status OmniDDI reported when the address was allocated, `Used`. An imported address takes the status shown in OmniDDI's address details.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func idAttribute() schema.Int64Attribute {
	attr := framework.IDAttribute()
	attr.MarkdownDescription = "The OmniDDI id of the address record."
	return attr
}

func (r *addressResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, r.idByKey)
}

func (r *addressResource) idByKey(ctx context.Context, key string) (int64, error) {
	parsed, err := parseImportKey(key)
	if err != nil {
		return 0, err
	}
	var networkID int64
	if parsed.cidr != "" {
		node, err := nodeapi.FindSubnetByCIDR(ctx, r.pd.Client, parsed.cidr)
		if err != nil {
			return 0, err
		}
		networkID = node.ID
	}
	rows, err := search(ctx, r.pd.Client, networkID, parsed.ip.String())
	if err != nil {
		return 0, err
	}
	switch found := matchIP(rows, parsed.ip); len(found) {
	case 0:
		return 0, client.NewNotFound("IP address")
	case 1:
		return found[0].ID, nil
	default:
		return 0, fmt.Errorf("%s is recorded in %d subnets; import it as <subnet cidr>/%s", parsed.ip, len(found), parsed.ip)
	}
}

func excludeList(ctx context.Context, set types.Set) ([]string, diag.Diagnostics) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}
	var values []iptypes.IPAddress
	diags := set.ElementsAs(ctx, &values, false)
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.ValueString())
	}
	return out, diags
}

var allocationLocks sync.Map

func lockAllocation(subnetID int64, name string) func() {
	key := fmt.Sprintf("%d/%s", subnetID, strings.ToLower(strings.TrimSuffix(name, ".")))
	held, _ := allocationLocks.LoadOrStore(key, &sync.Mutex{})
	mu, _ := held.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (r *addressResource) holders(ctx context.Context, subnetID int64, name string) (map[int64]bool, error) {
	rows, err := search(ctx, r.pd.Client, subnetID, strings.TrimSuffix(name, "."))
	if err != nil {
		return nil, err
	}
	return idsHolding(rows, subnetID, name), nil
}

func (r *addressResource) recoverNew(ctx context.Context, subnetID int64, name string, before map[int64]bool) (int64, bool) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return 0, false
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), recoveryTimeout)
		defer cancel()
	}
	after, err := r.holders(ctx, subnetID, name)
	if err != nil {
		return 0, false
	}
	var created []int64
	for id := range after {
		if !before[id] {
			created = append(created, id)
		}
	}
	if len(created) != 1 {
		return 0, false
	}
	return created[0], true
}

func (r *addressResource) allocateWithRetry(ctx context.Context, body map[string]any) (*allocateResponse, error) {
	poll := r.pd.Client.PollSettings()
	wait := poll.InitialInterval
	for attempt := 1; ; attempt++ {
		out, err := allocate(ctx, r.pd.Client, body)
		if apiCode(err) != codeAllocationBusy || attempt == maxBusyAttempts {
			return out, err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, err
		case <-timer.C:
		}
		wait = min(wait*2, poll.MaxInterval)
	}
}

func (r *addressResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()

	exclude, diags := excludeList(ctx, plan.Exclude)
	resp.Diagnostics.Append(diags...)
	subnetID, name := plan.SubnetID.ValueInt64(), plan.Name.ValueString()
	defer lockAllocation(subnetID, name)()
	before, err := r.holders(ctx, subnetID, name)
	if resp.Diagnostics.HasError() || framework.AppendIfErr(&resp.Diagnostics, "Unable to list the subnet's addresses before allocating", err) {
		return
	}
	out, err := r.allocateWithRetry(ctx, allocateBody(subnetID, name, exclude))
	if err != nil {
		r.createFailed(ctx, resp, &plan, before, err)
		return
	}
	plan.ID = types.Int64Value(out.RecordID)
	plan.IPAddress = iptypes.NewIPAddressValue(canonical(out.Address))
	plan.SubnetCIDR = cidrtypes.NewIPPrefixValue(out.Subnet.CIDR)
	plan.Status = types.StringValue(out.Status)
	r.saveCreated(ctx, resp, &plan)
}

func (r *addressResource) createFailed(ctx context.Context, resp *resource.CreateResponse, plan *model, before map[int64]bool, err error) {
	if !ambiguous(err) {
		r.addAllocateError(resp, *plan, err)
		return
	}
	id, found := r.recoverNew(ctx, plan.SubnetID.ValueInt64(), plan.Name.ValueString(), before)
	if !found {
		r.addAllocateError(resp, *plan, err)
		return
	}
	resp.Diagnostics.AddWarning("Adopted an address after the allocate request failed", fmt.Sprintf(
		"The allocate request failed (%s), but OmniDDI recorded one new address for %q in subnet %d, so the provider adopted it.",
		framework.ErrorDetail(err), plan.Name.ValueString(), plan.SubnetID.ValueInt64()))
	plan.ID = types.Int64Value(id)
	r.saveCreated(ctx, resp, plan)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.refresh(ctx, plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Adopted address could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *addressResource) addAllocateError(resp *resource.CreateResponse, plan model, err error) {
	summary := "Unable to allocate an IP address"
	if apiCode(err) == codeNoFreeIP {
		summary = fmt.Sprintf("No free address in subnet %d", plan.SubnetID.ValueInt64())
	}
	framework.AddClientError(&resp.Diagnostics, summary, err)
	if ambiguous(err) {
		resp.Diagnostics.AddWarning("An address may have been allocated", fmt.Sprintf(
			"The allocate request failed without a definite answer and no new address for %q could be found in subnet %d. "+
				"If OmniDDI did allocate one, Terraform does not track it; check the subnet before applying again.",
			plan.Name.ValueString(), plan.SubnetID.ValueInt64()))
	}
}

func (r *addressResource) saveCreated(ctx context.Context, resp *resource.CreateResponse, plan *model) {
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, plan.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func canonical(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil {
		return a.String()
	}
	return ip
}

func (r *addressResource) refresh(ctx context.Context, m *model) error {
	row, err := fetchByID(ctx, r.pd.Client, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	ip, ok := row.addr()
	if !ok || row.SubnetID == nil {
		return fmt.Errorf("OmniDDI returned address record %d without a valid address or subnet", row.ID)
	}
	if moved(*m, *row, ip) {
		return client.NewNotFound("IP address")
	}
	m.SubnetID = types.Int64Value(*row.SubnetID)
	m.IPAddress = iptypes.NewIPAddressValue(ip.String())
	if m.Status.IsNull() || m.Status.IsUnknown() {
		m.Status = types.StringValue(statusLabel(row.Status))
	}
	if m.Name.IsNull() && row.Hostname != nil && *row.Hostname != "" {
		m.Name = framework.NewHostnameValue(*row.Hostname)
	}
	if m.SubnetCIDR.IsNull() || m.SubnetCIDR.IsUnknown() {
		node, err := nodeapi.FetchOfType(ctx, r.pd.Client, *row.SubnetID, nodeapi.TypeSubnet)
		if err != nil {
			return err
		}
		prefix, ok := node.Prefix()
		if !ok {
			return fmt.Errorf("subnet %d has no valid CIDR", node.ID)
		}
		m.SubnetCIDR = cidrtypes.NewIPPrefixValue(prefix.String())
	}
	return nil
}

func moved(m model, row dto, ip netip.Addr) bool {
	if !m.SubnetID.IsNull() && !m.SubnetID.IsUnknown() && !row.inSubnet(m.SubnetID.ValueInt64()) {
		return true
	}
	if m.IPAddress.IsNull() || m.IPAddress.IsUnknown() {
		return false
	}
	known, err := netip.ParseAddr(m.IPAddress.ValueString())
	return err == nil && known != ip
}

func (r *addressResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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

func (r *addressResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *addressResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.AddWarning("IP address left allocated in OmniDDI", fmt.Sprintf(
		"OmniDDI has no API that releases an allocated address, so %s (record %d) in subnet %s stays allocated "+
			"and was only removed from Terraform state. Free it in OmniDDI when it is no longer needed.",
		state.IPAddress.ValueString(), state.ID.ValueInt64(), state.SubnetCIDR.ValueString()))
}
