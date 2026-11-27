package zone

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

const resourceTypeName = "omniddi_dns_zone"

type zoneResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*zoneResource)(nil)
	_ resource.ResourceWithIdentity    = (*zoneResource)(nil)
	_ resource.ResourceWithImportState = (*zoneResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*zoneResource)(nil)
)

func NewResource() resource.Resource { return &zoneResource{} }

func (r *zoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (r *zoneResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *zoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *zoneResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
}

func (r *zoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, func(ctx context.Context, key string) (int64, error) {
		return resolveZoneKey(ctx, r.pd.Client, key)
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(framework.ImportDefaults(ctx, &resp.State, framework.AttrDeletionProtection, framework.AttrForceDestroy)...)
}

func resolveZoneKey(ctx context.Context, c *client.Client, key string) (int64, error) {
	viewName, zoneName, ok := strings.Cut(key, "/")
	if !ok || viewName == "" || zoneName == "" {
		return 0, fmt.Errorf("expected a numeric zone id or <view>/<zone>, got %q", key)
	}
	viewID, err := resolveViewID(ctx, c, viewName)
	if err != nil {
		return 0, err
	}
	zone, err := findZone(ctx, c, viewID, zoneName)
	if err != nil {
		return 0, err
	}
	return zone.ID, nil
}

func zonePath(id int64) string { return zonesPath + strconv.FormatInt(id, 10) }

func createMatcher(viewID int64, name, cidr string) func(map[string]any) bool {
	return func(details map[string]any) bool {
		if details["view_id"] != float64(viewID) {
			return false
		}
		if name != "" {
			stored, _ := details["name"].(string)
			return framework.NormalizeFQDN(stored) == name
		}
		return cidr != "" && details["cidr"] == cidr
	}
}

func zoneIDMatcher(id int64) func(map[string]any) bool {
	return func(details map[string]any) bool { return details["zone_id"] == float64(id) }
}

func (r *zoneResource) refreshTags(ctx context.Context, m *zoneModel) error {
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectDNSZone, m.ID.ValueInt64())
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

func (r *zoneResource) applyTags(ctx context.Context, m *zoneModel) error {
	desired, diags := r.pd.DesiredTags(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("resolve tags: %s", diags)
	}
	return r.pd.Client.ApplyTags(ctx, client.TagObjectDNSZone, m.ID.ValueInt64(), desired)
}

func (r *zoneResource) readInto(ctx context.Context, m *zoneModel) error {
	dto, err := fetchZone(ctx, r.pd.Client, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	if diags := m.readAll(ctx, dto); diags.HasError() {
		return fmt.Errorf("map zone: %s", diags)
	}
	return r.refreshTags(ctx, m)
}

func (r *zoneResource) completeWrite(ctx context.Context, m *zoneModel, planned zoneModel) error {
	dto, err := fetchZone(ctx, r.pd.Client, m.ID.ValueInt64())
	if err != nil {
		return err
	}
	if diags := m.fillComputed(ctx, dto, planned); diags.HasError() {
		return fmt.Errorf("map zone: %s", diags)
	}
	tags, diags := r.pd.TagsAfterApply(ctx, m.Tags, m.TagsList)
	if diags.HasError() {
		return fmt.Errorf("map tags: %s", diags)
	}
	m.Tags, m.TagsList, m.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return nil
}

func (r *zoneResource) idByKey(ctx context.Context, viewID int64, name string) (int64, error) {
	if name == "" {
		return 0, client.NewNotFound("zone name")
	}
	zone, err := findZone(ctx, r.pd.Client, viewID, name)
	if err != nil {
		return 0, err
	}
	return zone.ID, nil
}

func (r *zoneResource) recoverByKey(viewID int64, name string) func(context.Context) (int64, bool, error) {
	return dnskit.RecoverFunc(func(ctx context.Context) (int64, error) { return r.idByKey(ctx, viewID, name) })
}

func (r *zoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan zoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	body, diags := buildBody(ctx, plan, nil)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	viewID, name := plan.ViewID.ValueInt64(), expectedName(plan)
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: zonesPath, Body: body, ActionType: actionCreate,
		Match:   createMatcher(viewID, name, plan.CIDR.ValueString()),
		Recover: r.recoverByKey(viewID, name),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to create zone", err) {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the zone id", dnskit.RequireTaskHandle(outcome)) {
		return
	}
	id, err := outcome.ResolveID(ctx, func(ctx context.Context) (int64, error) { return r.idByKey(ctx, viewID, name) }, "zone_id")
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the zone id", err) {
		return
	}

	plan.ID = types.Int64Value(id)
	if plan.Name.IsUnknown() || plan.Name.IsNull() {
		plan.Name = framework.NewFQDNValue(firstNonEmpty(createdName(outcome), name))
	}
	plan.TagsAll, _ = r.pd.PlannedTagsAll(ctx, plan.Tags)
	planned := plan
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, &plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := lifecycle.Converge(ctx, outcome); err != nil {
		var syncErr *client.SyncTaskError
		if !errors.As(err, &syncErr) {
			framework.CreateFailedAfterSave(ctx, resp, "Zone was created but not confirmed", err)
			return
		}
		resp.Diagnostics.AddWarning("Zone created; its first sync did not finish", fmt.Sprintf(
			"The zone was created and deployed, but OmniDDI's automatic sync that follows a create did not finish (%s). "+
				"Records created in this apply may be retired by that sync; the provider reactivates them on the next apply. "+
				"Check Agent Tasks in OmniDDI if records look out of date.", framework.ErrorDetail(syncErr.Err)))
	}
	if err := r.applyTags(ctx, &plan); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Zone tags could not be applied", err)
		return
	}
	if err := r.completeWrite(ctx, &plan, planned); err != nil {
		framework.CreateFailedAfterSave(ctx, resp, "Zone could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func createdName(outcome *framework.WriteOutcome) string {
	var body struct {
		ZoneName string `json:"zone_name"`
	}
	_ = outcome.Result.Decode(&body)
	return body.ZoneName
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (r *zoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state zoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpRead, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	if err := r.readInto(ctx, &state); err != nil {
		framework.HandleReadError(ctx, resp, err)
		return
	}
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, state.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *zoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state zoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpUpdate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	id := state.ID.ValueInt64()
	plan.ID = state.ID
	body, diags := buildBody(ctx, plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)

	var outcome *framework.WriteOutcome
	if len(body) > 0 {
		var err error
		outcome, err = lifecycle.Write(ctx, framework.WriteSpec{
			Method: http.MethodPut, Path: zonePath(id), Body: body,
			ActionType: actionUpdate, Match: zoneIDMatcher(id),
		})
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to update zone", err) {
			return
		}
		resp.Diagnostics.Append(outcome.Warnings()...)
	}

	if err := lifecycle.Converge(ctx, outcome); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Zone update was not confirmed", err)
		return
	}
	if err := r.applyTags(ctx, &plan); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Zone tags could not be applied", err)
		return
	}
	planned := plan
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, &plan)...)
	if err := r.completeWrite(ctx, &plan, planned); err != nil {
		framework.UpdateFailedAfterWrite(req, resp, "Zone could not be read back", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *zoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state zoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !framework.CheckDeletionProtection(&resp.Diagnostics, resourceTypeName, state.DeletionProtection) {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	id := state.ID.ValueInt64()

	if _, err := fetchZone(ctx, r.pd.Client, id); client.IsNotFound(err) {
		return
	} else if framework.AppendIfErr(&resp.Diagnostics, "Unable to read the zone before deleting it", err) {
		return
	}
	blockers, err := r.recordBlockers(ctx, id, state.ForceDestroy)
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to count the zone records", err) ||
		!framework.CheckForceDestroy(&resp.Diagnostics, resourceTypeName, state.ForceDestroy, blockers) {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, state.ApprovalMode)
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodDelete, Path: zonePath(id),
		ActionType: actionDelete, Match: dnskit.DeleteMatcher(id),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete zone", framework.IgnoreNotFound(err)) || outcome == nil {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	var noTasks *client.NoTasksError
	if err := lifecycle.Converge(ctx, outcome); err != nil && !errors.As(err, &noTasks) {
		framework.AddClientError(&resp.Diagnostics, "Zone deletion was not confirmed", err)
		return
	}
	err = lifecycle.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		_, fetchErr := fetchZone(ctx, r.pd.Client, id)
		if client.IsNotFound(fetchErr) {
			return false, nil
		}
		return fetchErr == nil, fetchErr
	})
	framework.AppendIfErr(&resp.Diagnostics, "Zone still exists after deletion", err)
}

func (r *zoneResource) recordBlockers(ctx context.Context, id int64, force types.Bool) (string, error) {
	if force.ValueBool() {
		return "", nil
	}
	count, err := countUserRecords(ctx, r.pd.Client, id)
	if err != nil || count == 0 {
		return "", err
	}
	return fmt.Sprintf("%d record(s) that were not created automatically", count), nil
}
