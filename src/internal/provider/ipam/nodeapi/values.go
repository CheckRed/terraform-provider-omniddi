package nodeapi

import (
	"context"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func OptionalString(remote *string, prior types.String) types.String {
	if remote != nil && *remote != "" {
		return types.StringValue(*remote)
	}
	if !prior.IsNull() && !prior.IsUnknown() && prior.ValueString() == "" {
		return prior
	}
	return types.StringNull()
}

func StringOrNull(remote *string) types.String { return types.StringPointerValue(remote) }

func Int64OrNull(remote *int64) types.Int64 { return types.Int64PointerValue(remote) }

func ClearableString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

func SetIfConfigured(body map[string]any, key string, v types.String) {
	if !v.IsNull() && !v.IsUnknown() {
		body[key] = v.ValueString()
	}
}

func VLANFromAPI(remote *string, priorVLAN types.String, priorVLANID types.Int64) (vlan types.String, vlanID types.Int64) {
	if !priorVLANID.IsNull() && !priorVLANID.IsUnknown() {
		if remote != nil {
			if n, err := strconv.ParseInt(*remote, 10, 64); err == nil {
				return types.StringNull(), types.Int64Value(n)
			}
		}
		return types.StringNull(), types.Int64Null()
	}
	return OptionalString(remote, priorVLAN), types.Int64Null()
}

func ReconcileParent(ctx context.Context, c *client.Client, prior types.Int64, node *Node) (types.Int64, error) {
	actual := Int64OrNull(node.ParentID)
	if prior.IsNull() || prior.IsUnknown() || node.ParentID == nil || prior.ValueInt64() == *node.ParentID {
		return actual, nil
	}
	ancestors, err := AncestorIDs(ctx, c, node.ID)
	if err != nil {
		return actual, err
	}
	if slices.Contains(ancestors, prior.ValueInt64()) {
		return prior, nil
	}
	return actual, nil
}
