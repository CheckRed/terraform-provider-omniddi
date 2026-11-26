package framework

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// KeepStateWhenUnchanged plans no update when only computed attributes unset in the configuration are unknown.
func KeepStateWhenUnchanged(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || resp.Plan.Raw.IsNull() || resp.Plan.Raw.Equal(req.State.Raw) {
		return
	}
	candidate, err := tftypes.Transform(resp.Plan.Raw, func(p *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if v.IsKnown() || !unsetInConfig(req.Config.Raw, p) {
			return v, nil
		}
		prior, _, err := tftypes.WalkAttributePath(req.State.Raw, p)
		if err != nil {
			return v, nil
		}
		if value, ok := prior.(tftypes.Value); ok {
			return value, nil
		}
		return v, nil
	})
	if err == nil && candidate.Equal(req.State.Raw) {
		resp.Plan.Raw = req.State.Raw
	}
}

func unsetInConfig(config tftypes.Value, p *tftypes.AttributePath) bool {
	found, _, err := tftypes.WalkAttributePath(config, p)
	if err != nil {
		return false
	}
	value, ok := found.(tftypes.Value)
	return ok && value.IsNull()
}
