package dnskit

import (
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func Int64Value(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

func BoolValue(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}

func StringPointer(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func CanonicalIP(value string) string {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return value
	}
	return addr.Unmap().String()
}
