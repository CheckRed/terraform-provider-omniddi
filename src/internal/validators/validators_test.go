package validators_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/validators"
)

func run(v validator.String, value types.String) bool {
	resp := &validator.StringResponse{}
	v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("x"), ConfigValue: value}, resp)
	return resp.Diagnostics.HasError()
}

func TestDuration(t *testing.T) {
	v := validators.Duration()
	for _, ok := range []string{"90s", "10m", "1h30m", "250ms"} {
		if run(v, types.StringValue(ok)) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "soon", "10", "0s", "-5m"} {
		if !run(v, types.StringValue(bad)) {
			t.Errorf("%q must be invalid", bad)
		}
	}
	if run(v, types.StringNull()) || run(v, types.StringUnknown()) {
		t.Error("null and unknown values are skipped")
	}
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) == "" {
		t.Error("descriptions required")
	}
	if d, err := validators.ParseDuration("2m"); err != nil || d != 2*time.Minute {
		t.Errorf("ParseDuration = %v, %v", d, err)
	}
}

func TestFQDN(t *testing.T) {
	v := validators.FQDN()
	for _, ok := range []string{"example.com", "example.com.", "a.b-c.example.com", "_dmarc.example.com", "*.example.com", "1.2.0.192.in-addr.arpa", "xn--bcher-kva.example"} {
		if run(v, types.StringValue(ok)) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", ".", "-bad.example.com", "bad-.example.com", "sp ace.example.com", "a..b", strings.Repeat("a", 64) + ".example.com", strings.Repeat("a.", 130) + "com"} {
		if !run(v, types.StringValue(bad)) {
			t.Errorf("%q must be invalid", bad)
		}
	}
}
