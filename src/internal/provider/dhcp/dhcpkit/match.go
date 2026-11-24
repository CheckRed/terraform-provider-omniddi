package dhcpkit

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func MatchName(name string) func(map[string]any) bool {
	return func(details map[string]any) bool { return details["name"] == name }
}

func MatchID(key string, id int64) func(map[string]any) bool {
	return func(details map[string]any) bool { return details[key] == float64(id) }
}

func OptionalString(remote *string, prior types.String) types.String {
	if remote == nil || (*remote == "" && (prior.IsNull() || prior.IsUnknown())) {
		return types.StringNull()
	}
	return types.StringValue(*remote)
}

func OptionalInt64(remote *int64) types.Int64 {
	if remote == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*remote)
}
