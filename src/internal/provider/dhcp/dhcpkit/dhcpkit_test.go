package dhcpkit_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

func TestOptionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	obj, diags := types.ObjectValue(dhcpkit.OptionObjectType().AttrTypes, map[string]attr.Value{
		"code": types.Int64Value(3), "name": types.StringValue("routers"), "space": types.StringNull(),
		"csv_format": types.BoolValue(true), "data": types.StringValue("10.0.0.1"), "data_type": types.StringNull(),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	list := types.ListValueMust(dhcpkit.OptionObjectType(), []attr.Value{obj})
	wire, diags := dhcpkit.OptionsToWire(ctx, list)
	if diags.HasError() {
		t.Fatal(diags)
	}
	want := []map[string]any{{"code": int64(3), "data": "10.0.0.1", "name": "routers", "csv_format": true}}
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("wire = %v, want %v", wire, want)
	}
	back, diags := dhcpkit.OptionsFromWire([]map[string]any{{"code": float64(3), "name": "routers", "csv_format": true, "data": 7.0}}, list)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if got := back.Elements()[0].(types.Object).Attributes()["data"]; !got.Equal(types.StringValue("7")) {
		t.Errorf("non-string data = %v", got)
	}
	if empty, _ := dhcpkit.OptionsFromWire(nil, types.ListNull(dhcpkit.OptionObjectType())); !empty.IsNull() {
		t.Errorf("an empty remote list must keep a null prior null")
	}
	if wire, _ := dhcpkit.OptionsToWire(ctx, types.ListNull(dhcpkit.OptionObjectType())); wire != nil {
		t.Errorf("a null list must produce no wire value")
	}
}

func TestMatchHelpers(t *testing.T) {
	if !dhcpkit.MatchName("x")(map[string]any{"name": "x"}) || dhcpkit.MatchName("x")(map[string]any{"name": "y"}) {
		t.Error("MatchName")
	}
	if !dhcpkit.MatchID("k", 5)(map[string]any{"k": float64(5)}) || dhcpkit.MatchID("k", 5)(map[string]any{"k": float64(6)}) {
		t.Error("MatchID")
	}
	empty := ""
	if !dhcpkit.OptionalString(&empty, types.StringNull()).IsNull() || dhcpkit.OptionalString(&empty, types.StringValue("x")).IsNull() {
		t.Error("OptionalString must treat an empty remote as unset only when unset before")
	}
	if !dhcpkit.OptionalInt64(nil).IsNull() {
		t.Error("OptionalInt64")
	}
}
