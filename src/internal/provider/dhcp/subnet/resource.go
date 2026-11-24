package subnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const (
	resourceTypeName = "omniddi_dhcp_subnet"
	actionPromote    = "create_subnet"
	actionUpdate     = "update_subnet"
	actionDemote     = "demote_subnet"
)

type subnetResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*subnetResource)(nil)
	_ resource.ResourceWithIdentity    = (*subnetResource)(nil)
	_ resource.ResourceWithImportState = (*subnetResource)(nil)
)

func NewResource() resource.Resource { return &subnetResource{} }

func Resources() []func() resource.Resource { return []func() resource.Resource{NewResource} }

func (r *subnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_subnet"
}

func (r *subnetResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *subnetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func promotePath(nodeID int64) string { return "/ipam/subnets/" + strconv.FormatInt(nodeID, 10) }

func settingsPath(id int64) string { return "/dhcp/subnets/" + strconv.FormatInt(id, 10) }

func demotePath(nodeID int64) string {
	return "/ipam/networks/" + strconv.FormatInt(nodeID, 10) + "/dhcp-config"
}

func matchPromote(nodeID, groupID int64) func(map[string]any) bool {
	return func(details map[string]any) bool {
		node, _ := details["ipam_node"].(map[string]any)
		return details["server_group_id"] == float64(groupID) && node["attach_node_id"] == float64(nodeID)
	}
}

func isBadRequest(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest
}

func (r *subnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, func(ctx context.Context, key string) (int64, error) {
		return resolveDHCPCIDR(ctx, r.pd.Client, key)
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(framework.ImportDefaults(ctx, &resp.State, framework.AttrDeletionProtection, framework.AttrForceDestroy)...)
}

func (r *subnetResource) recoverPromote(subnetID, groupID int64) func(context.Context) (int64, bool, error) {
	return dhcpkit.RecoverBy(func(ctx context.Context) (int64, error) {
		found, err := fetchByID(ctx, r.pd.Client, subnetID)
		if err != nil {
			return 0, err
		}
		if found.ServerGroupID == nil || *found.ServerGroupID != groupID {
			return 0, client.NewNotFound("DHCP subnet in the requested server group")
		}
		return found.ID, nil
	})
}

func (r *subnetResource) idAfterPromote(subnetID int64) func(context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		found, err := fetchByID(ctx, r.pd.Client, subnetID)
		if err != nil {
			return 0, fmt.Errorf("the promote request was accepted but IPAM subnet %d does not serve DHCP yet: %w", subnetID, err)
		}
		return found.ID, nil
	}
}

func emptyPrior() model {
	return model{
		Pools:          types.ListNull(poolObjectType()),
		OptionData:     types.ListNull(dhcpkit.OptionObjectType()),
		ClientClassIDs: types.SetNull(types.Int64Type),
	}
}

func alreadyServingDHCP(subnetID int64) (string, string) {
	return "The IPAM subnet already serves DHCP", fmt.Sprintf(
		"IPAM subnet %d already has DHCP service, so it cannot be promoted again and the provider will not silently take it over. "+
			"Bring it under Terraform with `terraform import omniddi_dhcp_subnet.<name> %d` (or the CIDR), "+
			"or demote it in OmniDDI first.", subnetID, subnetID)
}

func (r *subnetResource) sendSettings(ctx context.Context, lifecycle framework.Lifecycle, id int64, body map[string]any, diags *diag.Diagnostics) (*framework.WriteOutcome, error) {
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: settingsPath(id), Body: body,
		ActionType: actionUpdate, Match: dhcpkit.MatchID("subnet_id", id),
	})
	if err != nil {
		return nil, err
	}
	diags.Append(outcome.Warnings()...)
	return outcome, nil
}

func (r *subnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	subnetID, groupID := plan.SubnetID.ValueInt64(), plan.ServerGroupID.ValueInt64()

	_, err := nodeapi.FetchOfType(ctx, r.pd.Client, subnetID, nodeapi.TypeSubnet)
	if client.IsNotFound(err) {
		resp.Diagnostics.AddError("IPAM subnet not found", fmt.Sprintf(
			"No IPAM subnet has id %d. Create it with omniddi_ipam_subnet first, or check subnet_id.", subnetID))
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "The IPAM subnet cannot get DHCP service", err) {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: promotePath(subnetID), Body: map[string]any{"server_group_id": groupID},
		Op: framework.WriteOpCreate, ActionType: actionPromote, Match: matchPromote(subnetID, groupID),
		Recover: r.recoverPromote(subnetID, groupID),
	})
	if err != nil && isBadRequest(err) {
		if configured, checkErr := nodeapi.HasDHCPConfig(ctx, r.pd.Client, subnetID); checkErr == nil && configured {
			summary, detail := alreadyServingDHCP(subnetID)
			resp.Diagnostics.AddError(summary, detail+"\n\nOmniDDI said: "+framework.ErrorDetail(err))
			return
		}
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to enable DHCP on the IPAM subnet", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	id, err := outcome.ResolveID(ctx, r.idAfterPromote(subnetID), "subnet_id", "id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to confirm that DHCP was enabled", err) {
		return
	}

	plan.ID = types.Int64Value(id)
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, &plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "DHCP was enabled but not confirmed", err)
		return
	}
	if err := r.applyInitialSettings(ctx, lifecycle, id, plan, &resp.Diagnostics); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "DHCP was enabled but its settings could not be applied", err)
		return
	}
	if err := r.readBack(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "The DHCP subnet could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) applyInitialSettings(ctx context.Context, lifecycle framework.Lifecycle, id int64, plan model, diags *diag.Diagnostics) error {
	body, bodyDiags := settingsBody(ctx, plan, emptyPrior())
	diags.Append(bodyDiags...)
	if bodyDiags.HasError() {
		return errors.New("the DHCP settings could not be converted to an API request")
	}
	if len(body) == 0 {
		return nil
	}
	outcome, err := r.sendSettings(ctx, lifecycle, id, body, diags)
	if err != nil {
		return err
	}
	return lifecycle.Converge(ctx, outcome)
}

func (r *subnetResource) readBack(ctx context.Context, m *model) error {
	found, err := fetchByID(ctx, r.pd.Client, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	if diags := m.applyRemote(ctx, found, false); diags.HasError() {
		return fmt.Errorf("map the DHCP subnet: %s", diags)
	}
	return nil
}

func (r *subnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpRead, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	found, err := fetchByID(ctx, r.pd.Client, state.ID.ValueInt64())
	if err != nil {
		framework.HandleReadError(ctx, resp, err)
		return
	}
	resp.Diagnostics.Append(state.applyRemote(ctx, found, false)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, state.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *subnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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

	body, diags := settingsBody(ctx, plan, prior)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(body) > 0 {
		outcome, err := r.sendSettings(ctx, lifecycle, id, body, &resp.Diagnostics)
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to update the DHCP subnet", err) {
			return
		}
		if err := lifecycle.Converge(ctx, outcome); err != nil {
			framework.UpdateFailedAfterWrite(req, resp, "The DHCP subnet update was not confirmed", err)
			return
		}
	}
	if err := r.readBack(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "The DHCP subnet could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) blockers(ctx context.Context, id int64) (string, error) {
	reservations, err := countReservations(ctx, r.pd.Client, id)
	if err != nil {
		return "", err
	}
	leases, err := countActiveLeases(ctx, r.pd.Client, id)
	if err != nil {
		return "", err
	}
	switch {
	case reservations > 0 && leases > 0:
		return fmt.Sprintf("%d host reservation(s) and %d active lease(s)", reservations, leases), nil
	case reservations > 0:
		return fmt.Sprintf("%d host reservation(s)", reservations), nil
	case leases > 0:
		return fmt.Sprintf("%d active lease(s)", leases), nil
	}
	return "", nil
}

func (r *subnetResource) allowDemote(ctx context.Context, state model, diags *diag.Diagnostics) bool {
	if state.ForceDestroy.ValueBool() {
		return true
	}
	blockers, err := r.blockers(ctx, state.ID.ValueInt64())
	if framework.AppendIfErr(diags, "Unable to check the subnet's host reservations and leases", err) {
		return false
	}
	if blockers == "" {
		return true
	}
	diags.AddError("Refusing to demote a DHCP subnet that is in use", fmt.Sprintf(
		"%s still has %s. Demoting stops DHCP service for them; the reservations stay on the IPAM subnet and the leases remain until they expire. "+
			"Remove them first, or set force_destroy = true and apply before destroying.", resourceTypeName, blockers))
	return false
}

func (r *subnetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !framework.CheckDeletionProtection(&resp.Diagnostics, resourceTypeName, state.DeletionProtection) {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	id := state.ID.ValueInt64()

	_, err := fetchByID(ctx, r.pd.Client, id)
	if client.IsNotFound(err) {
		return
	}
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the DHCP subnet before demoting it", err) || !r.allowDemote(ctx, state, &resp.Diagnostics) {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, state.ApprovalMode)
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodDelete, Path: demotePath(id),
		ActionType: actionDemote, Match: dhcpkit.MatchID("subnet_id", id),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to remove DHCP service from the IPAM subnet", framework.IgnoreNotFound(err)) || outcome == nil {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if framework.AppendIfErr(&resp.Diagnostics, "Removing DHCP service was not confirmed", lifecycle.Converge(ctx, outcome)) {
		return
	}
	err = lifecycle.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		_, fetchErr := fetchByID(ctx, r.pd.Client, id)
		if client.IsNotFound(fetchErr) {
			return false, nil
		}
		return fetchErr == nil, fetchErr
	})
	framework.AppendIfErr(&resp.Diagnostics, "The IPAM subnet still serves DHCP after the demote", err)
}
