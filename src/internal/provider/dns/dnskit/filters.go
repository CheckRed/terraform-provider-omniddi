package dnskit

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var FilterOperators = []string{
	"EQ", "NEQ", "IN", "NIN", "CONTAINS", "NOT_CONTAINS", "STARTS_WITH", "ENDS_WITH",
	"GT", "GTE", "LT", "LTE", "IS_NULL", "NOT_NULL",
}

type QueryFilter struct {
	Field           string   `json:"field"`
	Operator        string   `json:"operator"`
	Value           any      `json:"value,omitempty"`
	Values          []string `json:"values,omitempty"`
	CaseInsensitive *bool    `json:"caseInsensitive,omitempty"`
}

type FilterModel struct {
	Field    types.String `tfsdk:"field"`
	Operator types.String `tfsdk:"operator"`
	Value    types.String `tfsdk:"value"`
	Values   types.List   `tfsdk:"values"`
}

func ExactFilter(field string, value any) QueryFilter {
	caseInsensitive := false
	return QueryFilter{Field: field, Operator: "EQ", Value: value, CaseInsensitive: &caseInsensitive}
}

func FiltersAttribute(query, noun, example string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional: true,
		MarkdownDescription: fmt.Sprintf("Filters sent to the OmniDDI `%s` query. Field names are the camelCase or snake_case %s fields, for example %s.",
			query, noun, example),
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"field": schema.StringAttribute{Required: true, MarkdownDescription: strings.ToUpper(noun[:1]) + noun[1:] + " field to filter on."},
			"operator": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Comparison operator: `" + strings.Join(FilterOperators, "`, `") + "`.",
				Validators:          []validator.String{stringvalidator.OneOf(FilterOperators...)},
			},
			"value":  schema.StringAttribute{Optional: true, MarkdownDescription: "Comparison value. Not used with `IN`, `NIN`, `IS_NULL` and `NOT_NULL`."},
			"values": schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Comparison values for `IN` and `NIN`."},
		}},
	}
}

func BuildFilters(ctx context.Context, list types.List) ([]QueryFilter, diag.Diagnostics) {
	var diags diag.Diagnostics
	if list.IsNull() || list.IsUnknown() {
		return nil, diags
	}
	var items []FilterModel
	diags.Append(list.ElementsAs(ctx, &items, false)...)
	filters := make([]QueryFilter, 0, len(items))
	for i, item := range items {
		filter := QueryFilter{Field: item.Field.ValueString(), Operator: item.Operator.ValueString()}
		at := path.Root("filters").AtListIndex(i)
		switch filter.Operator {
		case "IS_NULL", "NOT_NULL":
		case "IN", "NIN":
			var values []string
			diags.Append(item.Values.ElementsAs(ctx, &values, false)...)
			if len(values) == 0 {
				diags.AddAttributeError(at, "Invalid filter", fmt.Sprintf("The %s operator needs values.", filter.Operator))
			}
			filter.Values = values
		default:
			if item.Value.IsNull() {
				diags.AddAttributeError(at, "Invalid filter", fmt.Sprintf("The %s operator needs value.", filter.Operator))
			}
			filter.Value = item.Value.ValueString()
		}
		filters = append(filters, filter)
	}
	return filters, diags
}
