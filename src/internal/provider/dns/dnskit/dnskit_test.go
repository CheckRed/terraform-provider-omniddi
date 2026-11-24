package dnskit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

func TestValueHelpersMapNilToNull(t *testing.T) {
	n, b, s := int64(3), true, "x"
	if !dnskit.Int64Value(nil).IsNull() || dnskit.Int64Value(&n).ValueInt64() != 3 {
		t.Error("Int64Value")
	}
	if !dnskit.BoolValue(nil).IsNull() || !dnskit.BoolValue(&b).ValueBool() {
		t.Error("BoolValue")
	}
	if !dnskit.StringPointer(nil).IsNull() || dnskit.StringPointer(&s).ValueString() != "x" {
		t.Error("StringPointer")
	}
}

func TestCanonicalIP(t *testing.T) {
	cases := map[string]string{"2001:0db8::0001": "2001:db8::1", "::ffff:192.0.2.1": "192.0.2.1", "nonsense": "nonsense"}
	for in, want := range cases {
		if got := dnskit.CanonicalIP(in); got != want {
			t.Errorf("CanonicalIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyBuilderCreateSendsOnlyKnownNonNullValues(t *testing.T) {
	b := dnskit.NewBodyBuilder(false)
	b.Put("a", types.Int64Value(1), types.Int64Null(), func() any { return int64(1) })
	b.Put("unknown", types.Int64Unknown(), types.Int64Null(), func() any { return 0 })
	b.Put("null", types.Int64Null(), types.Int64Value(5), func() any { return 0 })
	b.PutIfSet("skipped", types.StringNull(), types.StringValue("x"), func() any { return "" })
	if len(b.Body) != 1 || b.Body["a"] != int64(1) {
		t.Errorf("body = %v", b.Body)
	}
}

func TestBodyBuilderUpdateSendsChangesAndClears(t *testing.T) {
	b := dnskit.NewBodyBuilder(true)
	b.Put("same", types.Int64Value(1), types.Int64Value(1), func() any { return 1 })
	b.Put("changed", types.Int64Value(2), types.Int64Value(1), func() any { return int64(2) })
	b.Put("cleared", types.Int64Null(), types.Int64Value(1), func() any { return 0 })
	b.Put("still-null", types.Int64Null(), types.Int64Null(), func() any { return 0 })
	b.PutIfSet("if-set-null", types.StringNull(), types.StringValue("x"), func() any { return "" })
	if v, ok := b.Body["cleared"]; !ok || v != nil {
		t.Errorf("a removed value must be sent as null: %v", b.Body)
	}
	if _, ok := b.Body["same"]; ok {
		t.Error("an unchanged value must not be sent")
	}
	if len(b.Body) != 2 || b.Body["changed"] != int64(2) {
		t.Errorf("body = %v", b.Body)
	}
}

func TestDeleteMatcher(t *testing.T) {
	match := dnskit.DeleteMatcher(4)
	if !match(map[string]any{"delete": map[string]any{"id": float64(4)}}) || match(map[string]any{"delete": map[string]any{"id": float64(5)}}) || match(map[string]any{}) {
		t.Error("DeleteMatcher")
	}
}

func TestRecoverFunc(t *testing.T) {
	ctx := context.Background()
	found := dnskit.RecoverFunc(func(context.Context) (int64, error) { return 9, nil })
	if id, ok, err := found(ctx); id != 9 || !ok || err != nil {
		t.Errorf("found = %d %v %v", id, ok, err)
	}
	missing := dnskit.RecoverFunc(func(context.Context) (int64, error) { return 0, client.NewNotFound("x") })
	if id, ok, err := missing(ctx); id != 0 || ok || err != nil {
		t.Errorf("missing = %d %v %v", id, ok, err)
	}
	boom := errors.New("boom")
	failing := dnskit.RecoverFunc(func(context.Context) (int64, error) { return 0, boom })
	if _, ok, err := failing(ctx); ok || !errors.Is(err, boom) {
		t.Errorf("failing = %v %v", ok, err)
	}
}

func TestRequireTaskHandle(t *testing.T) {
	if err := dnskit.RequireTaskHandle(&framework.WriteOutcome{}); err != nil {
		t.Errorf("an approval or recovered outcome carries no result and needs no handle: %v", err)
	}
	var missing *client.MissingTaskHandleError
	if err := dnskit.RequireTaskHandle(&framework.WriteOutcome{Result: &client.WriteResult{Method: "POST", Path: "/x"}}); !errors.As(err, &missing) {
		t.Errorf("a direct write without a correlation id must be refused: %v", err)
	}
	if err := dnskit.RequireTaskHandle(&framework.WriteOutcome{Result: &client.WriteResult{CorrelationID: "c"}}); err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func filterList(t *testing.T, items ...map[string]attr.Value) types.List {
	t.Helper()
	objType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"field": types.StringType, "operator": types.StringType, "value": types.StringType, "values": types.ListType{ElemType: types.StringType},
	}}
	elems := make([]attr.Value, 0, len(items))
	for _, item := range items {
		elems = append(elems, types.ObjectValueMust(objType.AttrTypes, item))
	}
	return types.ListValueMust(objType, elems)
}

func filter(field, op string, value types.String, values ...string) map[string]attr.Value {
	list := types.ListNull(types.StringType)
	if len(values) > 0 {
		elems := make([]attr.Value, 0, len(values))
		for _, v := range values {
			elems = append(elems, types.StringValue(v))
		}
		list = types.ListValueMust(types.StringType, elems)
	}
	return map[string]attr.Value{"field": types.StringValue(field), "operator": types.StringValue(op), "value": value, "values": list}
}

func TestBuildFilters(t *testing.T) {
	ctx := context.Background()
	if f, diags := dnskit.BuildFilters(ctx, types.ListNull(types.StringType)); f != nil || diags.HasError() {
		t.Error("a null list means no filters")
	}
	filters, diags := dnskit.BuildFilters(ctx, filterList(t,
		filter("name", "EQ", types.StringValue("a")),
		filter("name", "IN", types.StringNull(), "a", "b"),
		filter("description", "IS_NULL", types.StringNull()),
	))
	if diags.HasError() || len(filters) != 3 || filters[0].Value != "a" || filters[1].Values[1] != "b" || filters[2].Value != nil {
		t.Fatalf("filters = %+v diags = %v", filters, diags)
	}
	_, diags = dnskit.BuildFilters(ctx, filterList(t, filter("name", "IN", types.StringNull()), filter("name", "EQ", types.StringNull())))
	if diags.ErrorsCount() != 2 {
		t.Errorf("a value operator without values and a comparison without value must each be rejected: %v", diags)
	}
}

func TestExactFilterIsCaseSensitive(t *testing.T) {
	f := dnskit.ExactFilter("name", "Corp")
	if f.CaseInsensitive == nil || *f.CaseInsensitive || f.Operator != "EQ" || f.Value != "Corp" {
		t.Errorf("filter = %+v", f)
	}
}

func TestFiltersAttributeDescribesTheQuery(t *testing.T) {
	attribute := dnskit.FiltersAttribute("viewsPaginated", "view", "`name`")
	if !attribute.Optional || attribute.MarkdownDescription == "" {
		t.Errorf("attribute = %+v", attribute)
	}
	if got := attribute.NestedObject.Attributes["field"].GetMarkdownDescription(); got != "View field to filter on." {
		t.Errorf("field description = %q", got)
	}
}
