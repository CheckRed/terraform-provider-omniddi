package address

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

const sameNameDescription = "Keeps the stored name when only its case or trailing dot differs; any other change allocates a new address."

type replaceUnlessSameName struct{}

func (replaceUnlessSameName) Description(context.Context) string { return sameNameDescription }

func (replaceUnlessSameName) MarkdownDescription(context.Context) string { return sameNameDescription }

func (replaceUnlessSameName) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if sameName(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
		return
	}
	resp.RequiresReplace = true
}
