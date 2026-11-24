package nodeapi

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

const (
	ProtocolIPv4 = "IPv4"
	ProtocolIPv6 = "IPv6"
)

func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid CIDR: %w", s, err)
	}
	return p.Masked(), nil
}

func ProtocolOf(p netip.Prefix) string {
	if p.Addr().Is4() {
		return ProtocolIPv4
	}
	return ProtocolIPv6
}

func CheckNetworkAddress(addr netip.Addr, bits int) error {
	if !addr.IsValid() {
		return fmt.Errorf("invalid IP address")
	}
	if bits < 0 || bits > addr.BitLen() {
		return fmt.Errorf("prefix %d is out of range for %s (0-%d)", bits, addr, addr.BitLen())
	}
	if masked := netip.PrefixFrom(addr, bits).Masked().Addr(); masked != addr {
		return fmt.Errorf("%s/%d has host bits set; the network address is %s/%d", addr, bits, masked, bits)
	}
	return nil
}

type networkCIDRValidator struct{}

func NetworkCIDR() validator.String { return networkCIDRValidator{} }

func (networkCIDRValidator) Description(context.Context) string {
	return "value must be a CIDR whose address is the network address, such as 10.0.0.0/24"
}

func (v networkCIDRValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (networkCIDRValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	prefix, err := netip.ParsePrefix(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", err.Error())
		return
	}
	if err := CheckNetworkAddress(prefix.Addr(), prefix.Bits()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", err.Error())
	}
}
