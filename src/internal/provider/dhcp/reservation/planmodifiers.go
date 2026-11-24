package reservation

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

const stableWhileAddressUnchanged = "Keeps the prior value while `ip_address` is unchanged, because the subnet derived from the address cannot change."

type keepWhileAddressUnchanged struct{}

func (keepWhileAddressUnchanged) Description(context.Context) string {
	return stableWhileAddressUnchanged
}

func (keepWhileAddressUnchanged) MarkdownDescription(context.Context) string {
	return stableWhileAddressUnchanged
}

func addressUnchanged(ctx context.Context, plan tfsdk.Plan, state tfsdk.State) bool {
	if state.Raw.IsNull() || plan.Raw.IsNull() {
		return false
	}
	var planned, prior iptypes.IPAddress
	if plan.GetAttribute(ctx, path.Root("ip_address"), &planned).HasError() || state.GetAttribute(ctx, path.Root("ip_address"), &prior).HasError() {
		return false
	}
	if planned.IsNull() || planned.IsUnknown() || prior.IsNull() || prior.IsUnknown() {
		return false
	}
	return canonicalIP(planned.ValueString()) == canonicalIP(prior.ValueString())
}

func (keepWhileAddressUnchanged) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.PlanValue.IsUnknown() && !req.StateValue.IsUnknown() && addressUnchanged(ctx, req.Plan, req.State) {
		resp.PlanValue = req.StateValue
	}
}

func (keepWhileAddressUnchanged) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.PlanValue.IsUnknown() && !req.StateValue.IsUnknown() && addressUnchanged(ctx, req.Plan, req.State) {
		resp.PlanValue = req.StateValue
	}
}

var (
	_ planmodifier.Int64  = keepWhileAddressUnchanged{}
	_ planmodifier.String = keepWhileAddressUnchanged{}
)
