package record

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

type recordResource struct {
	pd *framework.ProviderData
}

var (
	_ resource.ResourceWithConfigure   = (*recordResource)(nil)
	_ resource.ResourceWithIdentity    = (*recordResource)(nil)
	_ resource.ResourceWithImportState = (*recordResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*recordResource)(nil)
)

func newRecordResource() resource.Resource { return &recordResource{} }

func (r *recordResource) api() recordAPI { return recordAPI{pd: r.pd} }

func (r *recordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record"
}

func (r *recordResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = framework.IdentitySchema()
}

func (r *recordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *recordResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.pd.ModifyTagsAllPlan(ctx, req, resp)
	framework.KeepStateWhenUnchanged(ctx, req, resp)
}

func (r *recordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	framework.ImportByIDOrKey(ctx, req, resp, r.api().idByNaturalKey)
}

func numberEquals(value any, want int64) bool {
	number, ok := value.(float64)
	return ok && int64(number) == want
}

func detailString(details map[string]any, key string) string {
	text, _ := details[key].(string)
	return text
}

func createMatcher(zoneID int64, name, recordType string) func(map[string]any) bool {
	return func(details map[string]any) bool {
		return numberEquals(details["zone_id"], zoneID) &&
			strings.EqualFold(detailString(details, "name"), name) &&
			strings.EqualFold(detailString(details, "type"), recordType)
	}
}

func recordIDMatcher(id int64) func(map[string]any) bool {
	return func(details map[string]any) bool { return numberEquals(details["record_id"], id) }
}

func (r *recordResource) readRecord(ctx context.Context, prior recordModel) (recordModel, error) {
	id := prior.ID.ValueInt64()
	dto, err := r.api().fetch(ctx, id)
	if err != nil {
		return prior, err
	}
	next, diags := flattenRecord(ctx, dto, prior)
	if diags.HasError() {
		return prior, fmt.Errorf("map record %d: %s", id, diags)
	}
	return next, nil
}

func (r *recordResource) refresh(ctx context.Context, prior recordModel) (recordModel, error) {
	next, err := r.readRecord(ctx, prior)
	if err != nil {
		return prior, err
	}
	id := prior.ID.ValueInt64()
	remote, err := r.pd.Client.GetTagValues(ctx, client.TagObjectDNSRecord, id)
	if err != nil {
		return prior, err
	}
	tags, diags := r.pd.TagsFromRemote(ctx, remote, prior.Tags, prior.TagsList)
	if diags.HasError() {
		return prior, fmt.Errorf("map tags of record %d: %s", id, diags)
	}
	next.Tags, next.TagsList, next.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return next, nil
}

func (r *recordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan recordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpCreate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	body, diags := createBody(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)
	zoneID, recordType, name := plan.ZoneID.ValueInt64(), plan.Type.ValueString(), normalizeName(plan.Name.ValueString())
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: recordsPath, Body: body,
		ActionType: actionCreateRecord, Match: createMatcher(zoneID, name, recordType),
		Recover: r.api().recoverByKey(zoneID, name, recordType),
	})
	verb := "created"
	var id int64
	if adopt := r.adopterFor(ctx, lifecycle, plan, err); adopt != nil {
		verb = "reactivated"
		id, outcome, err = adopt()
		if err != nil && id != 0 {
			if r.saveCreated(ctx, resp, &plan, id) {
				framework.CreateFailedAfterSave(ctx, resp, "DNS record was reactivated but this configuration was not applied to it", err)
			}
			return
		}
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to reactivate the inactive DNS record", err) {
			return
		}
		resp.Diagnostics.AddWarning("Reactivated an inactive DNS record", fmt.Sprintf(
			"OmniDDI already had a %s record named %q in this zone that was inactive (removed from the live zone, for example by a zone sync). "+
				"The provider reactivated record %d, or adopted its approved reactivation, and applied this configuration to it instead of creating a new one.", recordType, name, id))
	} else {
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to create DNS record", err) {
			return
		}
		id, err = outcome.ResolveID(ctx, func(ctx context.Context) (int64, error) {
			return r.api().idByKey(ctx, zoneID, name, recordType)
		}, "record_id")
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to determine the DNS record id", err) {
			return
		}
	}
	resp.Diagnostics.Append(outcome.Warnings()...)

	if !r.saveCreated(ctx, resp, &plan, id) {
		return
	}
	next, failure := r.finishWrite(ctx, outcome, lifecycle, plan, verb)
	if failure != nil {
		framework.CreateFailedAfterSave(ctx, resp, failure.summary, failure.err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *recordResource) saveCreated(ctx context.Context, resp *resource.CreateResponse, plan *recordModel, id int64) bool {
	plan.ID = types.Int64Value(id)
	plan.TagsAll, _ = r.pd.PlannedTagsAll(ctx, plan.Tags)
	resp.Diagnostics.Append(framework.SettleUnknowns(ctx, plan)...)
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, id)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	return !resp.Diagnostics.HasError()
}

func (r *recordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state recordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpRead, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	next, err := r.refresh(ctx, state)
	if err != nil {
		framework.HandleReadError(ctx, resp, err)
		return
	}
	resp.Diagnostics.Append(framework.SetIdentity(ctx, resp.Identity, next.ID.ValueInt64())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *recordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state recordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpUpdate, plan.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	body, diags := updateBody(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	lifecycle := framework.NewLifecycle(r.pd, plan.ApprovalMode)
	id := plan.ID.ValueInt64()
	var outcome *framework.WriteOutcome
	if len(body) > 0 {
		var err error
		outcome, err = lifecycle.Write(ctx, framework.WriteSpec{
			Method: http.MethodPut, Path: fmt.Sprintf(recordPathFmt, id), Body: body,
			ActionType: actionUpdateRecord, Match: recordIDMatcher(id),
		})
		if framework.AppendIfErr(&resp.Diagnostics, "Unable to update DNS record", err) {
			return
		}
		resp.Diagnostics.Append(outcome.Warnings()...)
	}

	next, failure := r.finishWrite(ctx, outcome, lifecycle, plan, "updated")
	if failure != nil {
		framework.UpdateFailedAfterWrite(req, resp, failure.summary, failure.err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *recordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state recordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := framework.WithTimeout(ctx, framework.OpDelete, state.Timeouts, r.pd, &resp.Diagnostics)
	defer cancel()
	lifecycle := framework.NewLifecycle(r.pd, state.ApprovalMode)
	id := state.ID.ValueInt64()

	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodDelete, Path: fmt.Sprintf(recordPathFmt, id),
		ActionType: actionDeleteRecord, Match: recordIDMatcher(id),
	})
	if framework.AppendIfErr(&resp.Diagnostics, "Unable to delete DNS record", framework.IgnoreNotFound(err)) || outcome == nil {
		return
	}
	resp.Diagnostics.Append(outcome.Warnings()...)
	if framework.AppendIfErr(&resp.Diagnostics, "DNS record deletion was not confirmed", lifecycle.Converge(ctx, outcome)) {
		return
	}
	err = lifecycle.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		_, fetchErr := r.api().fetch(ctx, id)
		if client.IsNotFound(fetchErr) {
			return false, nil
		}
		return fetchErr == nil, fetchErr
	})
	framework.AppendIfErr(&resp.Diagnostics, "DNS record still exists after deletion", err)
}
