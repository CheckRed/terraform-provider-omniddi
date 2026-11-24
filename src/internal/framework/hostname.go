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
	_ basetypes.StringTypable                    = HostnameType{}
	_ basetypes.StringValuableWithSemanticEquals = Hostname{}
)

func normalizeHostname(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type HostnameType struct {
	basetypes.StringType
}

func (t HostnameType) String() string { return "framework.HostnameType" }

func (t HostnameType) ValueType(context.Context) attr.Value { return Hostname{} }

func (t HostnameType) Equal(o attr.Type) bool {
	other, ok := o.(HostnameType)
	return ok && t.StringType.Equal(other.StringType)
}

func (t HostnameType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return Hostname{StringValue: in}, nil
}

func (t HostnameType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	raw, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	str, ok := raw.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type of %T", raw)
	}
	return Hostname{StringValue: str}, nil
}

type Hostname struct {
	basetypes.StringValue
}

func NewHostnameValue(value string) Hostname {
	return Hostname{StringValue: basetypes.NewStringValue(value)}
}

func NewHostnameNull() Hostname { return Hostname{StringValue: basetypes.NewStringNull()} }

func NewHostnameUnknown() Hostname { return Hostname{StringValue: basetypes.NewStringUnknown()} }

func (v Hostname) Type(context.Context) attr.Type { return HostnameType{} }

func (v Hostname) Equal(o attr.Value) bool {
	other, ok := o.(Hostname)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v Hostname) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	o, ok := other.(Hostname)
	if !ok {
		diags.AddError("Semantic equality check error", fmt.Sprintf("Expected Hostname value, got %T. Please report this to the provider developers.", other))
		return false, diags
	}
	return normalizeHostname(v.ValueString()) == normalizeHostname(o.ValueString()), diags
}
