package view

import "github.com/hashicorp/terraform-plugin-framework/types"

func descriptionValue(p *string, prior types.String) types.String {
	if p == nil || (*p == "" && prior.IsNull()) {
		return types.StringNull()
	}
	return types.StringValue(*p)
}
