package framework

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var (
	_ basetypes.StringTypable                    = FQDNType{}
	_ basetypes.StringValuableWithSemanticEquals = FQDN{}
)

const rootName = "."

func NormalizeFQDN(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == rootName {
		return rootName
	}
	return strings.TrimSuffix(trimmed, ".")
}

type FQDNType struct {
	basetypes.StringType
}

func (t FQDNType) String() string { return "framework.FQDNType" }

func (t FQDNType) ValueType(context.Context) attr.Value { return FQDN{} }

func (t FQDNType) Equal(o attr.Type) bool {
	other, ok := o.(FQDNType)
	return ok && t.StringType.Equal(other.StringType)
}

func (t FQDNType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return FQDN{StringValue: in}, nil
}

func (t FQDNType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	raw, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	str, ok := raw.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type of %T", raw)
	}
	return FQDN{StringValue: str}, nil
}

type FQDN struct {
	basetypes.StringValue
}

func NewFQDNValue(value string) FQDN { return FQDN{StringValue: basetypes.NewStringValue(value)} }

func NewFQDNNull() FQDN { return FQDN{StringValue: basetypes.NewStringNull()} }

func NewFQDNUnknown() FQDN { return FQDN{StringValue: basetypes.NewStringUnknown()} }

func (v FQDN) Type(context.Context) attr.Type { return FQDNType{} }

func (v FQDN) Equal(o attr.Value) bool {
	other, ok := o.(FQDN)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v FQDN) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	o, ok := other.(FQDN)
	if !ok {
		diags.AddError("Semantic equality check error", fmt.Sprintf("Expected FQDN value, got %T. Please report this to the provider developers.", other))
		return false, diags
	}
	return NormalizeFQDN(v.ValueString()) == NormalizeFQDN(o.ValueString()), diags
}
