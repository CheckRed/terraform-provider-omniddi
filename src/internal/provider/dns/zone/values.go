package zone

import "github.com/hashicorp/terraform-plugin-framework/types"

func keepInt64(current types.Int64, remote *int64) types.Int64 {
	switch {
	case remote != nil:
		return types.Int64Value(*remote)
	case current.IsUnknown():
		return types.Int64Null()
	}
	return current
}

func keepBool(current types.Bool, remote *bool) types.Bool {
	switch {
	case remote != nil:
		return types.BoolValue(*remote)
	case current.IsUnknown():
		return types.BoolNull()
	}
	return current
}

func keepString(current types.String, remote *string) types.String {
	switch {
	case remote != nil:
		return types.StringValue(*remote)
	case current.IsUnknown():
		return types.StringNull()
	}
	return current
}
