package dhcpkit_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

func configuredOption(t *testing.T, code int64, name string) attr.Value {
	t.Helper()
	obj, diags := types.ObjectValue(dhcpkit.OptionObjectType().AttrTypes, map[string]attr.Value{
		"code": types.Int64Value(code), "name": types.StringValue(name), "space": types.StringValue("dhcp4"),
		"csv_format": types.BoolValue(true), "data": types.StringValue("configured"), "data_type": types.StringNull(),
	})
	if diags.HasError() {
		t.Fatalf("object: %s", diags)
	}
	return obj
}

func TestOptionsFromWireKeepingTakesOnlyCodeAndDataFromTheServer(t *testing.T) {
	ctx := context.Background()
	prior, _ := types.ListValue(dhcpkit.OptionObjectType(), []attr.Value{configuredOption(t, 3, "routers")})
	raw := []map[string]any{
		{"code": float64(3), "data": "10.0.0.1", "name": "server-name", "space": "dhcp6", "csv-format": false, "data_type": "string"},
		{"code": float64(6), "data": "10.0.0.2", "name": "dns"},
	}
	got, diags := dhcpkit.OptionsFromWireKeeping(ctx, raw, prior)
	if diags.HasError() {
		t.Fatalf("diags: %s", diags)
	}
	var models []dhcpkit.OptionModel
	if d := got.ElementsAs(ctx, &models, false); d.HasError() {
		t.Fatalf("elements: %s", d)
	}
	if len(models) != 2 || models[0].Data.ValueString() != "10.0.0.1" || models[0].Name.ValueString() != "routers" ||
		models[0].Space.ValueString() != "dhcp4" || !models[0].CSVFormat.ValueBool() {
		t.Errorf("configured descriptors must survive: %+v", models)
	}
	if !models[1].Name.IsNull() || !models[1].Space.IsNull() || !models[1].CSVFormat.IsNull() || !models[1].DataType.IsNull() {
		t.Errorf("an option without a configured counterpart must keep descriptors null: %+v", models[1])
	}
}

func TestOptionsFromWireKeepingNullAndEmptyHandling(t *testing.T) {
	ctx := context.Background()
	if got, _ := dhcpkit.OptionsFromWireKeeping(ctx, nil, types.ListNull(dhcpkit.OptionObjectType())); !got.IsNull() {
		t.Errorf("no remote options and a null prior must stay null: %v", got)
	}
	empty, _ := types.ListValue(dhcpkit.OptionObjectType(), nil)
	if got, _ := dhcpkit.OptionsFromWireKeeping(ctx, nil, empty); got.IsNull() || len(got.Elements()) != 0 {
		t.Errorf("an empty prior must stay an empty list: %v", got)
	}
	if got, _ := dhcpkit.OptionsFromWireKeeping(ctx, []map[string]any{{"code": float64(15), "data": "x"}}, types.ListNull(dhcpkit.OptionObjectType())); len(got.Elements()) != 1 {
		t.Errorf("remote options with a null prior must be listed: %v", got)
	}
}

func TestOptionsFromWireAcceptsHyphenatedCSVFormat(t *testing.T) {
	got, diags := dhcpkit.OptionsFromWire([]map[string]any{{"code": float64(3), "data": "x", "csv-format": true}}, types.ListNull(dhcpkit.OptionObjectType()))
	if diags.HasError() {
		t.Fatalf("diags: %s", diags)
	}
	var models []dhcpkit.OptionModel
	got.ElementsAs(context.Background(), &models, false)
	if len(models) != 1 || !models[0].CSVFormat.ValueBool() {
		t.Errorf("csv-format was not read: %+v", models)
	}
}
