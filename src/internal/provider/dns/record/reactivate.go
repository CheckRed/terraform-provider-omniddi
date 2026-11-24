package record

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

type inactiveRecordError struct{ id int64 }

func (e *inactiveRecordError) Error() string {
	return fmt.Sprintf("record %d is inactive in OmniDDI (removed from the live zone)", e.id)
}

func (e *inactiveRecordError) NotFound() bool { return true }

func isInactive(err error) bool {
	var inactive *inactiveRecordError
	return errors.As(err, &inactive)
}

func isInactiveConflict(err error, recordType string) bool {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	detail := strings.ToLower(strings.TrimSpace(apiErr.Detail))
	return strings.HasPrefix(detail, "an inactive "+strings.ToLower(recordType)+" record named ") &&
		strings.Contains(detail, "already exists")
}

func (r *recordResource) reactivate(ctx context.Context, lifecycle framework.Lifecycle, id int64) (written bool, err error) {
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: fmt.Sprintf(recordPathFmt, id), Body: map[string]any{"is_active": true},
		ActionType: actionUpdateRecord, Match: recordIDMatcher(id),
	})
	if err != nil {
		return false, fmt.Errorf("reactivate record %d: %w", id, err)
	}
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		return true, fmt.Errorf("reactivate record %d: %w", id, err)
	}
	return true, nil
}

func (r *recordResource) adoptInactive(ctx context.Context, lifecycle framework.Lifecycle, plan recordModel) (int64, *framework.WriteOutcome, error) {
	zoneID, recordType, name := plan.ZoneID.ValueInt64(), plan.Type.ValueString(), normalizeName(plan.Name.ValueString())
	id, err := r.api().inactiveIDByKey(ctx, zoneID, name, recordType)
	if err != nil {
		return 0, nil, fmt.Errorf("find the inactive %s record %q: %w", recordType, name, err)
	}
	if written, err := r.reactivate(ctx, lifecycle, id); err != nil {
		if written {
			return id, nil, fmt.Errorf("record %d was reactivated, but the change was not confirmed: %w", id, err)
		}
		return 0, nil, err
	}
	return r.applyConfig(ctx, lifecycle, plan, id)
}

func (r *recordResource) applyConfig(ctx context.Context, lifecycle framework.Lifecycle, plan recordModel, id int64) (int64, *framework.WriteOutcome, error) {
	body, err := reactivatedBody(ctx, plan)
	if err != nil {
		return id, nil, fmt.Errorf("record %d was reactivated, but %w", id, err)
	}
	outcome, err := lifecycle.Write(ctx, framework.WriteSpec{
		Method: http.MethodPut, Path: fmt.Sprintf(recordPathFmt, id), Body: body,
		ActionType: actionUpdateRecord, Match: recordIDMatcher(id),
	})
	if err != nil {
		return id, nil, fmt.Errorf("record %d was reactivated, but applying the configuration failed: %w", id, err)
	}
	return id, outcome, nil
}

// approvedReactivation finds an active record whose reactivation request was approved after an earlier apply stopped.
func (r *recordResource) approvedReactivation(ctx context.Context, lifecycle framework.Lifecycle, plan recordModel) (int64, bool) {
	zoneID, recordType, name := plan.ZoneID.ValueInt64(), plan.Type.ValueString(), normalizeName(plan.Name.ValueString())
	id, err := r.api().idByKey(ctx, zoneID, name, recordType)
	if err != nil {
		return 0, false
	}
	requestID, err := lifecycle.AppliedRecently(ctx, actionUpdateRecord, func(details map[string]any) bool {
		return numberEquals(details["record_id"], id) && details["is_active"] == true
	})
	return id, err == nil && requestID != 0
}

func (r *recordResource) adopterFor(ctx context.Context, lifecycle framework.Lifecycle, plan recordModel, err error) func() (int64, *framework.WriteOutcome, error) {
	switch {
	case isInactiveConflict(err, plan.Type.ValueString()):
		return func() (int64, *framework.WriteOutcome, error) { return r.adoptInactive(ctx, lifecycle, plan) }
	case client.IsDuplicate(err):
		if id, ok := r.approvedReactivation(ctx, lifecycle, plan); ok {
			return func() (int64, *framework.WriteOutcome, error) { return r.applyConfig(ctx, lifecycle, plan, id) }
		}
	}
	return nil
}

func reactivatedBody(ctx context.Context, plan recordModel) (map[string]any, error) {
	body, diags := createBody(ctx, plan)
	data, dataDiags := expandData(ctx, plan)
	diags.Append(dataDiags...)
	flags, flagDiags := expandEnabledFlags(ctx, plan, data)
	diags.Append(flagDiags...)
	if diags.HasError() {
		return nil, fmt.Errorf("build the record body: %s", diags)
	}
	delete(body, "zone_id")
	if flags != nil {
		body["enabled_flags"] = flags
	}
	return body, nil
}
