package framework_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

var noopSchema = schema.Schema{Attributes: map[string]schema.Attribute{
	"name":    schema.StringAttribute{Optional: true},
	"value":   schema.StringAttribute{Optional: true},
	"updated": schema.StringAttribute{Computed: true},
}}

var noopType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"name": tftypes.String, "value": tftypes.String, "updated": tftypes.String,
}}

func object(name, value, updated any) tftypes.Value {
	return tftypes.NewValue(noopType, map[string]tftypes.Value{
		"name": tftypes.NewValue(tftypes.String, name), "value": tftypes.NewValue(tftypes.String, value),
		"updated": tftypes.NewValue(tftypes.String, updated),
	})
}

func keepState(config, plan, state tftypes.Value) tftypes.Value {
	req := resource.ModifyPlanRequest{
		Config: tfsdk.Config{Schema: noopSchema, Raw: config},
		Plan:   tfsdk.Plan{Schema: noopSchema, Raw: plan},
		State:  tfsdk.State{Schema: noopSchema, Raw: state},
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	framework.KeepStateWhenUnchanged(context.Background(), req, &resp)
	return resp.Plan.Raw
}

func TestKeepStateWhenUnchanged(t *testing.T) {
	state := object("svc", "a", "2026-01-01")
	unknown := tftypes.UnknownValue
	tests := map[string]struct {
		config, plan tftypes.Value
		wantState    bool
	}{
		"only computed unknown":         {object("svc", "a", nil), object("svc", "a", unknown), true},
		"configured value unknown":      {object("svc", unknown, nil), object("svc", unknown, unknown), false},
		"configured value changed":      {object("svc", "b", nil), object("svc", "b", unknown), false},
		"nothing unknown and unchanged": {object("svc", "a", nil), state, true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := keepState(tc.config, tc.plan, state)
			if got.Equal(state) != tc.wantState {
				t.Errorf("plan equals state = %v, want %v (plan %v)", got.Equal(state), tc.wantState, got)
			}
		})
	}
	if got := keepState(object("svc", "a", nil), object("svc", "a", unknown), tftypes.NewValue(noopType, nil)); !got.Equal(object("svc", "a", unknown)) {
		t.Errorf("a create must keep its plan, got %v", got)
	}
}
