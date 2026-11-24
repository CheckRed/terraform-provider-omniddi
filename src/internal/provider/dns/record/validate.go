package record

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithValidateConfig = (*recordResource)(nil)

type formPresence struct {
	attribute string
	path      path.Path
	known     bool
	set       bool
}

func (r *recordResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg recordModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Type.IsUnknown() || cfg.Type.IsNull() {
		return
	}
	recordType := cfg.Type.ValueString()
	validateFormMatchesType(resp, recordType, presentForms(cfg))
	validateValuesShape(resp, recordType, cfg)
	validateDisabledValues(ctx, resp, recordType, cfg)
}

func presentForms(cfg recordModel) []formPresence {
	return []formPresence{
		{"values", path.Root("values"), !cfg.Values.IsUnknown(), !cfg.Values.IsNull()},
		{"mx", path.Root("mx"), true, len(cfg.MX) > 0},
		{"srv", path.Root("srv"), true, len(cfg.SRV) > 0},
		{"caa", path.Root("caa"), true, len(cfg.CAA) > 0},
		{"data_json", path.Root("data_json"), !cfg.DataJSON.IsUnknown(), !cfg.DataJSON.IsNull()},
	}
}

func validateFormMatchesType(resp *resource.ValidateConfigResponse, recordType string, forms []formPresence) {
	allowed := allowedFormNames(recordType)
	var present []formPresence
	anyUnknown := false
	for _, form := range forms {
		switch {
		case !form.known:
			anyUnknown = true
		case form.set:
			present = append(present, form)
		}
	}
	for _, form := range present {
		if !slices.Contains(allowed, form.attribute) {
			resp.Diagnostics.AddAttributeError(form.path, "Value form does not match the record type",
				fmt.Sprintf("%s records are configured with %s, not %q.", recordType, allowedFormsDescription(recordType), form.attribute))
		}
	}
	if len(present) > 1 {
		resp.Diagnostics.AddAttributeError(present[1].path, "Conflicting value forms",
			fmt.Sprintf("Exactly one of values, mx, srv, caa or data_json may be set; found %q and %q.", present[0].attribute, present[1].attribute))
	}
	if len(present) == 0 && !anyUnknown {
		resp.Diagnostics.AddAttributeError(path.Root("type"), "Missing record value",
			fmt.Sprintf("%s records need %s.", recordType, allowedFormsDescription(recordType)))
	}
}

func allowedFormNames(recordType string) []string {
	switch {
	case isStringType(recordType):
		return []string{"values"}
	case typedBlockFor(recordType) != "":
		return []string{typedBlockFor(recordType), "data_json"}
	}
	return []string{"data_json"}
}

func validateValuesShape(resp *resource.ValidateConfigResponse, recordType string, cfg recordModel) {
	if cfg.Values.IsNull() || cfg.Values.IsUnknown() {
		return
	}
	elements := cfg.Values.Elements()
	if len(elements) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("values"), "Empty values", "values must contain at least one element.")
		return
	}
	if isSingleValue(recordType) && len(elements) != 1 {
		resp.Diagnostics.AddAttributeError(path.Root("values"), "Too many values",
			fmt.Sprintf("%s records take exactly one value, found %d.", recordType, len(elements)))
	}
	for index, element := range elements {
		if text, ok := element.(types.String); ok && text.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("values").AtListIndex(index), "Null value", "values must not contain null elements.")
		}
	}
}

func validateDisabledValues(ctx context.Context, resp *resource.ValidateConfigResponse, recordType string, cfg recordModel) {
	if cfg.DisabledValues.IsNull() || cfg.DisabledValues.IsUnknown() {
		return
	}
	disabledPath := path.Root("disabled_values")
	if !supportsDisabledValues(recordType) {
		resp.Diagnostics.AddAttributeError(disabledPath, "Unsupported attribute for this record type",
			fmt.Sprintf("disabled_values is only supported for A, AAAA, NS, PTR, TXT and SPF, not %s.", recordType))
		return
	}
	if cfg.Values.IsNull() || cfg.Values.IsUnknown() || hasUnknownElement(cfg.Values.Elements()) || hasUnknownElement(cfg.DisabledValues.Elements()) {
		return
	}
	var values, disabled []string
	resp.Diagnostics.Append(cfg.Values.ElementsAs(ctx, &values, false)...)
	resp.Diagnostics.Append(cfg.DisabledValues.ElementsAs(ctx, &disabled, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, value := range disabled {
		if !slices.Contains(values, value) {
			resp.Diagnostics.AddAttributeError(disabledPath, "Disabled value is not in values",
				fmt.Sprintf("disabled_values contains %q, which is not one of values.", value))
		}
	}
	if len(disabled) >= len(values) {
		resp.Diagnostics.AddAttributeError(disabledPath, "No enabled value left",
			"At least one value must remain enabled; delete the record to remove it from DNS entirely.")
	}
}

func hasUnknownElement(elements []attr.Value) bool {
	return slices.ContainsFunc(elements, func(element attr.Value) bool { return element.IsUnknown() || element.IsNull() })
}
