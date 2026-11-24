package nodeapi_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

func TestSamePrefixAndSameAddressCompareSemantically(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b       string
		prefixSame bool
	}{
		{"2001:db8::/32", "2001:DB8:0:0::/32", true},
		{"10.0.0.0/8", "10.0.0.0/8", true},
		{"10.0.0.0/8", "10.0.0.0/9", false},
		{"10.0.0.0/8", "bogus", false},
		{"bogus", "bogus", false},
	} {
		if got := nodeapi.SamePrefix(tc.a, tc.b); got != tc.prefixSame {
			t.Errorf("SamePrefix(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
	if !nodeapi.SameAddress("2001:db8:1::", "2001:DB8:1:0::") || nodeapi.SameAddress("192.0.2.0", "192.0.2.1") || nodeapi.SameAddress("x", "x") {
		t.Errorf("SameAddress compares addresses incorrectly")
	}
}

func TestReplacementModifiersOnlyReplaceForADifferentNetwork(t *testing.T) {
	t.Parallel()
	check := func(modifier planmodifier.String, plan, state string) bool {
		req := planmodifier.StringRequest{
			PlanValue: types.StringValue(plan), StateValue: types.StringValue(state), ConfigValue: types.StringValue(plan),
		}
		req.State.Raw = tftypes.NewValue(tftypes.String, "present")
		req.Plan.Raw = tftypes.NewValue(tftypes.String, "present")
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		modifier.PlanModifyString(context.Background(), req, resp)
		return resp.RequiresReplace
	}
	prefix, addr := nodeapi.RequiresReplaceIfPrefixDiffers(), nodeapi.RequiresReplaceIfAddressDiffers()
	if check(prefix, "2001:DB8::/32", "2001:db8::/32") || !check(prefix, "2001:db8::/33", "2001:db8::/32") {
		t.Errorf("prefix modifier mishandled the comparison")
	}
	if check(addr, "2001:DB8::", "2001:db8::") || !check(addr, "2001:db8::1", "2001:db8::") {
		t.Errorf("address modifier mishandled the comparison")
	}
}
