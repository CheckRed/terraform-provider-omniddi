package framework_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func TestNormalizeFQDNKeepsTheRoot(t *testing.T) {
	for in, want := range map[string]string{
		".": ".", " . ": ".", "": "", "Example.COM.": "example.com", "example.com": "example.com", "  Host.Example.Com. ": "host.example.com",
	} {
		if got := framework.NormalizeFQDN(in); got != want {
			t.Errorf("NormalizeFQDN(%q) = %q, want %q", in, got, want)
		}
	}
	root, empty := framework.NewFQDNValue("."), framework.NewFQDNValue("")
	if eq, _ := root.StringSemanticEquals(context.Background(), empty); eq {
		t.Error("the root and an empty name must not be equal")
	}
	if eq, _ := root.StringSemanticEquals(context.Background(), framework.NewFQDNValue(".")); !eq {
		t.Error("the root equals itself")
	}
}

func TestHostnameIsCaseInsensitiveButTrailingDotSensitive(t *testing.T) {
	ctx := context.Background()
	equal := func(a, b string) bool {
		eq, diags := framework.NewHostnameValue(a).StringSemanticEquals(ctx, framework.NewHostnameValue(b))
		return eq && !diags.HasError()
	}
	if !equal("Mail.Example.COM", "mail.example.com") || !equal(" mail ", "MAIL") {
		t.Error("case and surrounding whitespace must not matter")
	}
	if equal("mail", "mail.") || equal("a", "b") {
		t.Error("a trailing dot makes a different name (relative vs absolute)")
	}
	if _, diags := framework.NewHostnameValue("a").StringSemanticEquals(ctx, types.StringValue("a")); !diags.HasError() {
		t.Error("a non-Hostname value must be a diagnostic")
	}
}

func TestHostnameTypeRoundTrip(t *testing.T) {
	ctx := context.Background()
	typ := framework.HostnameType{}
	val, err := typ.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.String, "Mail"))
	if err != nil {
		t.Fatal(err)
	}
	host, ok := val.(framework.Hostname)
	if !ok || host.ValueString() != "Mail" || !host.Type(ctx).Equal(typ) {
		t.Fatalf("val = %#v", val)
	}
	if !host.Equal(framework.NewHostnameValue("Mail")) || host.Equal(framework.NewHostnameValue("mail")) || host.Equal(types.StringValue("Mail")) {
		t.Error("Equal must be exact and type-aware")
	}
	if typ.Equal(types.StringType) || typ.Equal(framework.FQDNType{}) || typ.String() == "" {
		t.Error("type equality")
	}
	if _, ok := typ.ValueType(ctx).(framework.Hostname); !ok {
		t.Error("ValueType")
	}
	if null, err := typ.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.String, nil)); err != nil || !null.IsNull() {
		t.Errorf("null: %v %v", null, err)
	}
	if _, err := typ.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.Number, 1)); err == nil {
		t.Error("a non-string must fail")
	}
	if v, diags := typ.ValueFromString(ctx, types.StringValue("x")); diags.HasError() || v == nil {
		t.Error("ValueFromString")
	}
	if !framework.NewHostnameNull().IsNull() || !framework.NewHostnameUnknown().IsUnknown() {
		t.Error("constructors")
	}
}
