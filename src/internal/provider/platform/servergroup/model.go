package servergroup

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var memberType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"server_id": types.Int64Type,
	"role":      types.StringType,
}}

type memberModel struct {
	ServerID types.Int64  `tfsdk:"server_id"`
	Role     types.String `tfsdk:"role"`
}

func memberModels(dto []memberDTO) []memberModel {
	members := make([]memberModel, 0, len(dto))
	for _, m := range dto {
		role := types.StringNull()
		if m.Role != nil {
			role = types.StringValue(strings.ToLower(*m.Role))
		}
		members = append(members, memberModel{ServerID: types.Int64Value(m.ID), Role: role})
	}
	slices.SortFunc(members, func(a, b memberModel) int { return cmp.Compare(a.ServerID.ValueInt64(), b.ServerID.ValueInt64()) })
	return members
}

func membersSet(ctx context.Context, dto []memberDTO) (types.Set, diag.Diagnostics) {
	return types.SetValueFrom(ctx, memberType, memberModels(dto))
}
