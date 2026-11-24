package record

import (
	"context"
	"fmt"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

type writeFailure struct {
	summary string
	err     error
}

func (r *recordResource) finishWrite(
	ctx context.Context,
	outcome *framework.WriteOutcome,
	lifecycle framework.Lifecycle,
	plan recordModel,
	verb string,
) (recordModel, *writeFailure) {
	if verb == "created" && outcome != nil && outcome.Result != nil {
		if err := outcome.Result.RequireCorrelationID(); err != nil {
			return plan, &writeFailure{"DNS record was " + verb + " but cannot be tracked", err}
		}
	}
	if err := lifecycle.Converge(ctx, outcome); err != nil {
		return plan, &writeFailure{"DNS record change was not confirmed", err}
	}
	desired, tagDiags := r.pd.DesiredTags(ctx, plan.Tags, plan.TagsList)
	if tagDiags.HasError() {
		return plan, &writeFailure{"DNS record tags could not be mapped", fmt.Errorf("map tags: %s", tagDiags)}
	}
	if err := r.pd.Client.ApplyTags(ctx, client.TagObjectDNSRecord, plan.ID.ValueInt64(), desired); err != nil {
		return plan, &writeFailure{"DNS record tags could not be applied", err}
	}
	next, err := r.readRecord(ctx, plan)
	if isInactive(err) {
		if _, err = r.reactivate(ctx, lifecycle, plan.ID.ValueInt64()); err == nil {
			next, err = r.readRecord(ctx, plan)
		}
	}
	if err != nil {
		return plan, &writeFailure{"DNS record could not be read back", err}
	}
	tags, tagDiags := r.pd.TagsAfterApply(ctx, plan.Tags, plan.TagsList)
	if tagDiags.HasError() {
		return plan, &writeFailure{"DNS record tags could not be mapped", fmt.Errorf("map tags: %s", tagDiags)}
	}
	next.Tags, next.TagsList, next.TagsAll = tags.Tags, tags.TagsList, tags.TagsAll
	return next, nil
}
