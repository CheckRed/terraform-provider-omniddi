package framework

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type TagsModel struct {
	Tags     types.Map `tfsdk:"tags"`
	TagsList types.Map `tfsdk:"tags_list"`
	TagsAll  types.Map `tfsdk:"tags_all"`
}

func (pd *ProviderData) DesiredTags(ctx context.Context, tags, tagsList types.Map) (map[string][]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	desired := map[string][]string{}
	if pd != nil {
		for name, value := range pd.DefaultTags {
			desired[name] = []string{value}
		}
	}
	single := map[string]string{}
	if !tags.IsNull() && !tags.IsUnknown() {
		diags.Append(tags.ElementsAs(ctx, &single, false)...)
	}
	multi := map[string][]string{}
	if !tagsList.IsNull() && !tagsList.IsUnknown() {
		diags.Append(tagsList.ElementsAs(ctx, &multi, false)...)
	}
	if diags.HasError() {
		return nil, diags
	}
	for name, value := range single {
		desired[name] = []string{value}
	}
	for name, values := range multi {
		if _, clash := single[name]; clash {
			diags.AddError("Conflicting tags", fmt.Sprintf("Tag %q is set in both tags and tags_list.", name))
			continue
		}
		desired[name] = slices.Clone(values)
	}
	return desired, diags
}

func (pd *ProviderData) defaults() map[string]string {
	if pd == nil {
		return nil
	}
	return pd.DefaultTags
}

func (pd *ProviderData) TagsFromRemote(ctx context.Context, remote []client.ObjectTag, priorTags, priorList types.Map) (TagsModel, diag.Diagnostics) {
	defaults := pd.defaults()
	single, multi := map[string]string{}, map[string][]string{}
	for _, tag := range remote {
		if tag.Name == client.InheritedTagKey || len(tag.Values) == 0 {
			continue
		}
		if tag.MultiValue {
			if !isDefaultOnly(defaults, tag, priorList) {
				multi[tag.Name] = tag.Values
			}
			continue
		}
		if !isDefaultOnly(defaults, tag, priorTags) {
			single[tag.Name] = tag.Values[0]
		}
	}

	var diags diag.Diagnostics
	model := TagsModel{}
	model.Tags = mapFrom(ctx, types.StringType, single, priorTags, &diags)
	model.TagsList = mapFrom(ctx, types.ListType{ElemType: types.StringType}, multi, priorList, &diags)
	all, d := derivedTagsAll(ctx, defaults, single, multi)
	diags.Append(d...)
	model.TagsAll = all
	return model, diags
}

func (pd *ProviderData) TagsAfterApply(ctx context.Context, tags, tagsList types.Map) (TagsModel, diag.Diagnostics) {
	all, diags := pd.DerivedTagsAll(ctx, tags, tagsList)
	model := TagsModel{Tags: knownOrNull(tags, types.StringType), TagsList: knownOrNull(tagsList, types.ListType{ElemType: types.StringType}), TagsAll: all}
	return model, diags
}

func knownOrNull(m types.Map, elem attr.Type) types.Map {
	if m.IsUnknown() {
		return types.MapNull(elem)
	}
	return m
}

func (pd *ProviderData) ModifyTagsAllPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if pd == nil || req.Plan.Raw.IsNull() {
		return
	}
	var tags, tagsList types.Map
	diags := req.Plan.GetAttribute(ctx, path.Root(AttrTags), &tags)
	diags.Append(req.Plan.GetAttribute(ctx, path.Root(AttrTagsList), &tagsList)...)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() || hasUnknown(tags) || hasUnknown(tagsList) {
		return
	}
	all, derivedDiags := pd.DerivedTagsAll(ctx, tags, tagsList)
	resp.Diagnostics.Append(derivedDiags...)
	if !derivedDiags.HasError() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(AttrTagsAll), all)...)
	}
}

func hasUnknown(m types.Map) bool {
	if m.IsUnknown() {
		return true
	}
	for _, v := range m.Elements() {
		if v.IsUnknown() {
			return true
		}
		if list, ok := v.(types.List); ok && slices.ContainsFunc(list.Elements(), func(e attr.Value) bool { return e.IsUnknown() }) {
			return true
		}
	}
	return false
}

func (pd *ProviderData) DerivedTagsAll(ctx context.Context, tags, tagsList types.Map) (types.Map, diag.Diagnostics) {
	var diags diag.Diagnostics
	single := map[string]string{}
	if !tags.IsNull() && !tags.IsUnknown() {
		diags.Append(tags.ElementsAs(ctx, &single, false)...)
	}
	multi := map[string][]string{}
	if !tagsList.IsNull() && !tagsList.IsUnknown() {
		diags.Append(tagsList.ElementsAs(ctx, &multi, false)...)
	}
	if diags.HasError() {
		return types.MapNull(types.StringType), diags
	}
	all, d := derivedTagsAll(ctx, pd.defaults(), single, multi)
	return all, append(diags, d...)
}

func derivedTagsAll(ctx context.Context, defaults, single map[string]string, multi map[string][]string) (types.Map, diag.Diagnostics) {
	merged := MergeStringMaps(defaults, single)
	for name := range multi {
		if _, own := single[name]; !own {
			delete(merged, name)
		}
	}
	return types.MapValueFrom(ctx, types.StringType, merged)
}

func isDefaultOnly(defaults map[string]string, tag client.ObjectTag, prior types.Map) bool {
	want, isDefault := defaults[tag.Name]
	if !isDefault || len(tag.Values) != 1 || tag.Values[0] != want {
		return false
	}
	_, configured := prior.Elements()[tag.Name]
	return !configured
}

func mapFrom[T any](ctx context.Context, elem attr.Type, values map[string]T, prior types.Map, diags *diag.Diagnostics) types.Map {
	if len(values) == 0 && prior.IsNull() {
		return types.MapNull(elem)
	}
	m, d := types.MapValueFrom(ctx, elem, values)
	diags.Append(d...)
	return m
}

func MergeStringMaps(base, overlay map[string]string) map[string]string {
	merged := maps.Clone(base)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, overlay)
	return merged
}

func (pd *ProviderData) PlannedTagsAll(ctx context.Context, tags types.Map) (types.Map, diag.Diagnostics) {
	return pd.DerivedTagsAll(ctx, tags, types.MapNull(types.ListType{ElemType: types.StringType}))
}
