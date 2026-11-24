package zone

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

const maxZoneNameLength = 253

var dnsLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type stringCheck struct {
	description string
	check       func(string) error
}

func (v stringCheck) Description(context.Context) string         { return v.description }
func (v stringCheck) MarkdownDescription(context.Context) string { return v.description }

func (v stringCheck) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := v.check(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", err.Error())
	}
}

func zoneName() validator.String {
	return stringCheck{description: "value must be a DNS name whose labels contain letters, digits and hyphens", check: validateZoneName}
}

func ipAddress() validator.String {
	return stringCheck{description: "value must be an IPv4 or IPv6 address", check: func(s string) error {
		if _, err := netip.ParseAddr(s); err != nil {
			return fmt.Errorf("%q is not a valid IPv4 or IPv6 address", s)
		}
		return nil
	}}
}

func cidrBlock() validator.String {
	return stringCheck{description: "value must be a CIDR prefix valid for a reverse zone", check: func(s string) error {
		_, err := reversePrefix(s)
		return err
	}}
}

func validateZoneName(name string) error {
	trimmed := strings.TrimSuffix(strings.TrimSpace(name), ".")
	if trimmed == "" || len(trimmed) > maxZoneNameLength {
		return fmt.Errorf("zone name %q must be between 1 and %d characters", name, maxZoneNameLength)
	}
	for _, label := range strings.Split(trimmed, ".") {
		if !dnsLabel.MatchString(label) {
			return fmt.Errorf("zone name %q has the invalid label %q: each label must start and end with a letter or digit and contain only letters, digits and hyphens", name, label)
		}
	}
	return nil
}

func reversePrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return prefix, fmt.Errorf("%q is not a valid CIDR", cidr)
	}
	switch {
	case prefix.Addr().Is4() && (prefix.Bits() < 8 || prefix.Bits()%8 != 0):
		return prefix, fmt.Errorf("an IPv4 reverse zone needs a /8, /16, /24 or /32 prefix, got /%d", prefix.Bits())
	case prefix.Addr().Is6() && prefix.Bits()%4 != 0:
		return prefix, fmt.Errorf("an IPv6 reverse zone needs a prefix that is a multiple of 4, got /%d", prefix.Bits())
	}
	return prefix, nil
}

func deriveReverseName(protocol, cidr string) (string, bool) {
	prefix, err := reversePrefix(cidr)
	if err != nil {
		return "", false
	}
	prefix = prefix.Masked()
	switch {
	case strings.EqualFold(protocol, "IPv4") && prefix.Addr().Is4() && prefix.Bits() >= 8 && prefix.Bits() <= 24:
		octets := strings.Split(prefix.Addr().String(), ".")[:prefix.Bits()/8]
		labels := make([]string, 0, len(octets)+2)
		for i := len(octets) - 1; i >= 0; i-- {
			labels = append(labels, octets[i])
		}
		return strings.Join(append(labels, "in-addr", "arpa"), "."), true
	case strings.EqualFold(protocol, "IPv6") && prefix.Addr().Is6():
		raw := prefix.Addr().As16()
		hex := fmt.Sprintf("%x", raw[:])
		nibbles := hex[:prefix.Bits()/4]
		labels := make([]string, 0, len(nibbles)+2)
		for i := len(nibbles) - 1; i >= 0; i-- {
			labels = append(labels, string(nibbles[i]))
		}
		return strings.Join(append(labels, "ip6", "arpa"), "."), true
	}
	return "", false
}

var _ resource.ResourceWithValidateConfig = (*zoneResource)(nil)

func known(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func (r *zoneResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg zoneModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateShape(cfg)...)
	resp.Diagnostics.Append(validatePrimaryServers(ctx, cfg.PrimaryServers)...)
}

func zoneTypeOf(cfg zoneModel) (string, bool) {
	if cfg.ZoneType.IsUnknown() {
		return "", false
	}
	if cfg.ZoneType.IsNull() {
		return typePrimary, true
	}
	return cfg.ZoneType.ValueString(), true
}

func validateShape(cfg zoneModel) diag.Diagnostics {
	var diags diag.Diagnostics
	zoneType, typeKnown := zoneTypeOf(cfg)
	hasCIDR, hasProtocol := !cfg.CIDR.IsNull(), !cfg.Protocol.IsNull()

	if typeKnown && cfg.Name.IsNull() && !(zoneType == typeReverse && hasCIDR && hasProtocol) {
		diags.AddAttributeError(path.Root("name"), "Missing zone name", "name is required, except for a Reverse zone that sets protocol and cidr.")
	}
	if typeKnown && zoneType != typeReverse && (hasCIDR || hasProtocol) {
		diags.AddAttributeError(path.Root("cidr"), "Invalid zone settings", "protocol and cidr only apply to a Reverse zone.")
	}
	if (hasCIDR && !hasProtocol && !cfg.CIDR.IsUnknown()) || (hasProtocol && !hasCIDR && !cfg.Protocol.IsUnknown()) {
		diags.AddAttributeError(path.Root("protocol"), "Invalid zone settings", "protocol and cidr must be set together.")
	}
	if known(cfg.CIDR) && known(cfg.Protocol) {
		if prefix, err := reversePrefix(cfg.CIDR.ValueString()); err == nil && prefix.Addr().Is4() != strings.EqualFold(cfg.Protocol.ValueString(), "IPv4") {
			diags.AddAttributeError(path.Root("cidr"), "Invalid zone settings", fmt.Sprintf("cidr %s does not match protocol %s.", cfg.CIDR.ValueString(), cfg.Protocol.ValueString()))
		}
	}
	if !typeKnown {
		return diags
	}
	if zoneType == typeForward && cfg.Nameservers.IsNull() {
		diags.AddAttributeError(path.Root("nameservers"), "Missing nameservers", "A Forward zone requires at least one entry in nameservers.")
	}
	if zoneType != typeForward && !cfg.Nameservers.IsNull() {
		diags.AddAttributeError(path.Root("nameservers"), "Invalid zone settings", "nameservers only apply to a Forward zone.")
	}
	if zoneType != typeForward && !cfg.Policy.IsNull() {
		diags.AddAttributeError(path.Root("policy"), "Invalid zone settings", "policy only applies to a Forward zone.")
	}
	if zoneType == typeSecondary && cfg.PrimaryServers.IsNull() {
		diags.AddAttributeError(path.Root("primary_servers"), "Missing primary servers", "A Secondary zone requires at least one entry in primary_servers.")
	}
	if zoneType != typeSecondary && !cfg.PrimaryServers.IsNull() {
		diags.AddAttributeError(path.Root("primary_servers"), "Invalid zone settings", "primary_servers only apply to a Secondary zone.")
	}
	return diags
}

func validatePrimaryServers(ctx context.Context, list types.List) diag.Diagnostics {
	var diags diag.Diagnostics
	if list.IsNull() || list.IsUnknown() {
		return diags
	}
	var items []primaryServerModel
	diags.Append(list.ElementsAs(ctx, &items, false)...)
	seen := map[string]bool{}
	for _, item := range items {
		if !known(item.IP) || item.Port.IsUnknown() {
			continue
		}
		port := item.Port.ValueInt64()
		if item.Port.IsNull() {
			port = defaultDNSPort
		}
		key := fmt.Sprintf("%s/%d", dnskit.CanonicalIP(item.IP.ValueString()), port)
		if seen[key] {
			diags.AddAttributeError(path.Root("primary_servers"), "Duplicate primary server", fmt.Sprintf("Primary server %s appears more than once.", item.IP.ValueString()))
		}
		seen[key] = true
	}
	return diags
}
