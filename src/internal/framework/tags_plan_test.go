package framework_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

var listOfStrings = types.ListType{ElemType: types.StringType}

func stringMap(pairs map[string]string) types.Map {
	values := map[string]attr.Value{}
	for k, v := range pairs {
		values[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, values)
}

func listMap(pairs map[string][]string) types.Map {
	values := map[string]attr.Value{}
	for k, items := range pairs {
		elems := make([]attr.Value, len(items))
		for i, item := range items {
			elems[i] = types.StringValue(item)
		}
		values[k] = types.ListValueMust(types.StringType, elems)
	}
	return types.MapValueMust(listOfStrings, values)
}

func tagsPlan(t *testing.T, model *framework.TagsModel) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	s := schema.Schema{Attributes: framework.TagsAttributes()}
	plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if model != nil {
		if diags := plan.Set(ctx, model); diags.HasError() {
			t.Fatal(diags)
		}
	}
	return plan
}

func plannedTagsAll(t *testing.T, resp *resource.ModifyPlanResponse) types.Map {
	t.Helper()
	var all types.Map
	if diags := resp.Plan.GetAttribute(context.Background(), path.Root(framework.AttrTagsAll), &all); diags.HasError() {
		t.Fatal(diags)
	}
	return all
}

func TestModifyTagsAllPlanSetsTheDerivedValue(t *testing.T) {
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod", "team": "net"}}
	plan := tagsPlan(t, &framework.TagsModel{
		Tags:     stringMap(map[string]string{"owner": "x", "team": "dns"}),
		TagsList: listMap(map[string][]string{"env": {"a", "b"}}),
		TagsAll:  types.MapUnknown(types.StringType),
	})
	resp := &resource.ModifyPlanResponse{Plan: plan}

	pd.ModifyTagsAllPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	got := plannedTagsAll(t, resp)
	if got.IsUnknown() || fmt.Sprint(got.Elements()) != `map[owner:"x" team:"dns"]` {
		t.Errorf("tags_all = %v: single-valued tags win over defaults and a tags_list key drops the default", got)
	}
}

func TestModifyTagsAllPlanIsANoOpWhenThereIsNothingToDerive(t *testing.T) {
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	unknownAll := types.MapUnknown(types.StringType)
	unknownElement := types.MapValueMust(types.StringType, map[string]attr.Value{"a": types.StringUnknown()})
	unknownListElement := types.MapValueMust(listOfStrings, map[string]attr.Value{
		"a": types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
	})

	tests := map[string]struct {
		pd    *framework.ProviderData
		model *framework.TagsModel
	}{
		"destroy":              {pd, nil},
		"nil provider data":    {nil, &framework.TagsModel{Tags: stringMap(map[string]string{"a": "b"}), TagsList: types.MapNull(listOfStrings), TagsAll: unknownAll}},
		"unknown tags":         {pd, &framework.TagsModel{Tags: types.MapUnknown(types.StringType), TagsList: types.MapNull(listOfStrings), TagsAll: unknownAll}},
		"unknown tag value":    {pd, &framework.TagsModel{Tags: unknownElement, TagsList: types.MapNull(listOfStrings), TagsAll: unknownAll}},
		"unknown tags_list":    {pd, &framework.TagsModel{Tags: types.MapNull(types.StringType), TagsList: types.MapUnknown(listOfStrings), TagsAll: unknownAll}},
		"unknown list element": {pd, &framework.TagsModel{Tags: types.MapNull(types.StringType), TagsList: unknownListElement, TagsAll: unknownAll}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			plan := tagsPlan(t, tc.model)
			resp := &resource.ModifyPlanResponse{Plan: plan}
			tc.pd.ModifyTagsAllPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !plan.Raw.Equal(resp.Plan.Raw) {
				t.Error("the plan must be left untouched")
			}
		})
	}
}

func TestModifyTagsAllPlanWithNullTagsPlansTheDefaultsOnly(t *testing.T) {
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	plan := tagsPlan(t, &framework.TagsModel{Tags: types.MapNull(types.StringType), TagsList: types.MapNull(listOfStrings), TagsAll: types.MapUnknown(types.StringType)})
	resp := &resource.ModifyPlanResponse{Plan: plan}
	pd.ModifyTagsAllPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, resp)
	if got := plannedTagsAll(t, resp); fmt.Sprint(got.Elements()) != `map[env:"prod"]` {
		t.Errorf("tags_all = %v", got)
	}
}

func TestTagsAllIsDerivedFromTagsAndDefaultsNotFromRemote(t *testing.T) {
	ctx := context.Background()
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	remote := []client.ObjectTag{
		{Name: "env", Values: []string{"prod"}},
		{Name: "owner", Values: []string{"netops"}},
		{Name: "teams", Values: []string{"a"}, MultiValue: true},
	}
	prior := stringMap(map[string]string{"owner": "netops"})

	model, diags := pd.TagsFromRemote(ctx, remote, prior, types.MapNull(listOfStrings))
	if diags.HasError() {
		t.Fatal(diags)
	}
	want, _ := pd.DerivedTagsAll(ctx, model.Tags, model.TagsList)
	if !model.TagsAll.Equal(want) {
		t.Errorf("tags_all = %v, want merge(default_tags, tags) = %v", model.TagsAll, want)
	}
	if _, listed := model.TagsAll.Elements()["teams"]; listed {
		t.Error("multi-valued tags never appear in tags_all")
	}

	drifted := append(remote, client.ObjectTag{Name: "env", Values: []string{"dev"}})
	drifted = drifted[1:]
	model, _ = pd.TagsFromRemote(ctx, drifted, prior, types.MapNull(listOfStrings))
	if model.Tags.Elements()["env"] == nil || fmt.Sprint(model.TagsAll.Elements()["env"]) != `"dev"` {
		t.Errorf("a remote value that differs from the default is drift: tags=%v tags_all=%v", model.Tags, model.TagsAll)
	}
}

func TestTagsAfterApplyKeepsThePlannedValues(t *testing.T) {
	ctx := context.Background()
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	tags := stringMap(map[string]string{"owner": "NetOps"})
	list := listMap(map[string][]string{"teams": {"b", "a"}})

	got, diags := pd.TagsAfterApply(ctx, tags, list)
	if diags.HasError() || !got.Tags.Equal(tags) || !got.TagsList.Equal(list) {
		t.Fatalf("planned tags must be kept verbatim: %+v %v", got, diags)
	}
	if fmt.Sprint(got.TagsAll.Elements()) != `map[env:"prod" owner:"NetOps"]` {
		t.Errorf("tags_all = %v", got.TagsAll)
	}

	unknown, _ := pd.TagsAfterApply(ctx, types.MapUnknown(types.StringType), types.MapUnknown(listOfStrings))
	if !unknown.Tags.IsNull() || !unknown.TagsList.IsNull() || unknown.TagsAll.IsUnknown() {
		t.Errorf("unknown values must settle to null: %+v", unknown)
	}

	nilPD, _ := (*framework.ProviderData)(nil).TagsAfterApply(ctx, tags, types.MapNull(listOfStrings))
	if len(nilPD.TagsAll.Elements()) != 1 {
		t.Errorf("nil provider data: %v", nilPD.TagsAll)
	}
}
