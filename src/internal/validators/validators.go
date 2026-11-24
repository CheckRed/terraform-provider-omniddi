package validators

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var fqdnLabel = regexp.MustCompile(`^(?:[A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?|\*)$`)

const maxFQDNLength = 253

type stringFunc struct {
	description string
	check       func(string) error
}

func (v stringFunc) Description(context.Context) string         { return v.description }
func (v stringFunc) MarkdownDescription(context.Context) string { return v.description }

func (v stringFunc) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := v.check(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", err.Error())
	}
}

func ParseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid duration such as \"90s\" or \"10m\"", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration %q must be greater than zero", s)
	}
	return d, nil
}

func Duration() validator.String {
	return stringFunc{
		description: "value must be a positive duration string such as \"90s\" or \"10m\"",
		check:       func(s string) error { _, err := ParseDuration(s); return err },
	}
}

func ValidateFQDN(name string) error {
	trimmed := strings.TrimSuffix(name, ".")
	if trimmed == "" || len(trimmed) > maxFQDNLength {
		return fmt.Errorf("%q must be between 1 and %d characters", name, maxFQDNLength)
	}
	for _, label := range strings.Split(trimmed, ".") {
		if !fqdnLabel.MatchString(label) {
			return fmt.Errorf("%q contains the invalid DNS label %q", name, label)
		}
	}
	return nil
}

func FQDN() validator.String {
	return stringFunc{description: "value must be a valid fully qualified domain name", check: ValidateFQDN}
}
