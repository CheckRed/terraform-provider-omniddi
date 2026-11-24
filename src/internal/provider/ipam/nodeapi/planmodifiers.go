package nodeapi

import (
	"context"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

const sameNetworkDescription = "Replaces the resource when the value changes to a different network; an equivalent spelling does not."

func SamePrefix(a, b string) bool {
	left, errLeft := ParsePrefix(a)
	right, errRight := ParsePrefix(b)
	return errLeft == nil && errRight == nil && left == right
}

func SameAddress(a, b string) bool {
	left, errLeft := netip.ParseAddr(a)
	right, errRight := netip.ParseAddr(b)
	return errLeft == nil && errRight == nil && left == right
}

func RequiresReplaceIfPrefixDiffers() planmodifier.String {
	return replaceUnless(SamePrefix)
}

func RequiresReplaceIfAddressDiffers() planmodifier.String {
	return replaceUnless(SameAddress)
}

func replaceUnless(same func(a, b string) bool) planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !same(req.PlanValue.ValueString(), req.StateValue.ValueString())
		},
		sameNetworkDescription, sameNetworkDescription,
	)
}
