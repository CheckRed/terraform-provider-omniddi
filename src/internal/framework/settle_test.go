package framework_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

var nestedAttrTypes = map[string]attr.Type{
	"name":  types.StringType,
	"ports": types.ListType{ElemType: types.Int64Type},
}

func nestedObject(name attr.Value, ports attr.Value) types.Object {
	return types.ObjectValueMust(nestedAttrTypes, map[string]attr.Value{"name": name, "ports": ports})
}

type innerRow struct {
	Name types.String `tfsdk:"name"`
	Size types.Int64  `tfsdk:"size"`
}

type settleModel struct {
	ID       types.Int64             `tfsdk:"id"`
	Name     types.String            `tfsdk:"name"`
	Host     framework.FQDN          `tfsdk:"host"`
	Flag     types.Bool              `tfsdk:"flag"`
	Tags     types.Map               `tfsdk:"tags"`
	List     types.List              `tfsdk:"list"`
	Set      types.Set               `tfsdk:"set"`
	Obj      types.Object            `tfsdk:"obj"`
	Whole    types.Object            `tfsdk:"whole"`
	Rows     []innerRow              `tfsdk:"rows"`
	Optional *innerRow               `tfsdk:"optional"`
	Nested   map[string]innerRow     `tfsdk:"nested"`
	Dynamic  types.Dynamic           `tfsdk:"dynamic"`
	Plain    string                  `tfsdk:"plain"`
	Skipped  string                  `tfsdk:"-"`
	hidden   types.String            `tfsdk:"hidden"`
	Pointers []*innerRow             `tfsdk:"pointers"`
	Lists    map[string]types.String `tfsdk:"lists"`
}

func TestSettleUnknownsConvertsEveryUnknownToNull(t *testing.T) {
	ctx := context.Background()
	model := settleModel{
		ID:   types.Int64Unknown(),
		Name: types.StringValue("kept"),
		Host: framework.NewFQDNUnknown(),
		Flag: types.BoolUnknown(),
		Tags: types.MapValueMust(types.StringType, map[string]attr.Value{"a": types.StringUnknown(), "b": types.StringValue("x")}),
		List: types.ListValueMust(types.ObjectType{AttrTypes: nestedAttrTypes}, []attr.Value{
			nestedObject(types.StringUnknown(), types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Unknown(), types.Int64Value(80)})),
			nestedObject(types.StringValue("keep"), types.ListUnknown(types.Int64Type)),
		}),
		Set:      types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
		Obj:      nestedObject(types.StringValue("o"), types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Unknown()})),
		Whole:    types.ObjectUnknown(nestedAttrTypes),
		Rows:     []innerRow{{Name: types.StringUnknown(), Size: types.Int64Value(2)}, {Name: types.StringValue("n"), Size: types.Int64Unknown()}},
		Optional: &innerRow{Name: types.StringUnknown(), Size: types.Int64Unknown()},
		Nested:   map[string]innerRow{"k": {Name: types.StringUnknown(), Size: types.Int64Value(1)}},
		Dynamic:  types.DynamicValue(types.StringUnknown()),
		Plain:    "plain",
		hidden:   types.StringUnknown(),
		Pointers: []*innerRow{{Name: types.StringUnknown()}, nil},
		Lists:    map[string]types.String{"u": types.StringUnknown()},
	}

	if diags := framework.SettleUnknowns(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}

	assertNull := func(name string, v attr.Value) {
		t.Helper()
		if !v.IsNull() || v.IsUnknown() {
			t.Errorf("%s = %v, want null", name, v)
		}
	}
	assertNull("id", model.ID)
	assertNull("host", model.Host)
	assertNull("flag", model.Flag)
	assertNull("whole", model.Whole)
	assertNull("rows[0].name", model.Rows[0].Name)
	assertNull("rows[1].size", model.Rows[1].Size)
	assertNull("optional.name", model.Optional.Name)
	assertNull("nested.k.name", model.Nested["k"].Name)
	assertNull("pointers[0].name", model.Pointers[0].Name)
	assertNull("lists.u", model.Lists["u"])
	if model.Name.ValueString() != "kept" || model.Rows[0].Size.ValueInt64() != 2 || model.Plain != "plain" {
		t.Errorf("known values must be untouched: %+v", model)
	}
	if !model.hidden.IsUnknown() {
		t.Error("unexported fields are out of reach and must be skipped")
	}
	if _, ok := model.Host.Type(ctx).(framework.FQDNType); !ok {
		t.Error("custom types must keep their type")
	}

	if !model.Tags.Elements()["a"].IsNull() || model.Tags.Elements()["b"].IsNull() {
		t.Errorf("map elements: %v", model.Tags)
	}
	outer := model.List.Elements()
	first := outer[0].(types.Object).Attributes()
	if !first["name"].IsNull() || !first["ports"].(types.List).Elements()[0].IsNull() || first["ports"].(types.List).Elements()[1].IsNull() {
		t.Errorf("first nested object = %v", outer[0])
	}
	second := outer[1].(types.Object).Attributes()
	if second["name"].IsNull() || !second["ports"].IsNull() || second["ports"].IsUnknown() {
		t.Errorf("second nested object = %v", outer[1])
	}
	if !model.Set.Elements()[0].IsNull() {
		t.Errorf("set element = %v", model.Set)
	}
	if !model.Obj.Attributes()["ports"].(types.List).Elements()[0].IsNull() {
		t.Errorf("object = %v", model.Obj)
	}
	inner, ok := model.Dynamic.UnderlyingValue().(types.String)
	if !ok || !inner.IsNull() {
		t.Errorf("dynamic = %v", model.Dynamic)
	}
}

func TestSettleUnknownsRejectsNonPointers(t *testing.T) {
	ctx := context.Background()
	for name, target := range map[string]any{"value": settleModel{}, "nil pointer": (*settleModel)(nil), "nil": nil} {
		if diags := framework.SettleUnknowns(ctx, target); !diags.HasError() {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestSettleUnknownsLeavesNullAndKnownValuesAlone(t *testing.T) {
	model := settleModel{
		ID: types.Int64Value(1), Host: framework.NewFQDNValue("a.example.com"), Tags: types.MapNull(types.StringType),
		List: types.ListNull(types.StringType), Set: types.SetNull(types.StringType), Obj: types.ObjectNull(nestedAttrTypes),
		Whole: types.ObjectNull(nestedAttrTypes),
	}
	if diags := framework.SettleUnknowns(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	if model.ID.ValueInt64() != 1 || model.Host.ValueString() != "a.example.com" || !model.Tags.IsNull() || !model.Obj.IsNull() {
		t.Errorf("model changed: %+v", model)
	}
}
