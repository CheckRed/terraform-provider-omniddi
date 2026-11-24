package framework

import (
	"context"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var attrValueType = reflect.TypeFor[attr.Value]()

func SettleUnknowns(ctx context.Context, model any) diag.Diagnostics {
	target := reflect.ValueOf(model)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return diag.Diagnostics{diag.NewErrorDiagnostic("Unable to settle unknown values", fmt.Sprintf("Expected a non-nil pointer to a model, got %T. Please report this to the provider developers.", model))}
	}
	var diags diag.Diagnostics
	settleReflect(ctx, target.Elem(), &diags)
	return diags
}

func settleReflect(ctx context.Context, v reflect.Value, diags *diag.Diagnostics) {
	switch {
	case v.Type().Implements(attrValueType):
		settleAttrField(ctx, v, diags)
	case v.Kind() == reflect.Pointer:
		if !v.IsNil() {
			settleReflect(ctx, v.Elem(), diags)
		}
	case v.Kind() == reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				settleReflect(ctx, v.Field(i), diags)
			}
		}
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Array:
		for i := range v.Len() {
			settleReflect(ctx, v.Index(i), diags)
		}
	case v.Kind() == reflect.Map:
		for _, key := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(key))
			settleReflect(ctx, elem, diags)
			v.SetMapIndex(key, elem)
		}
	}
}

func settleAttrField(ctx context.Context, field reflect.Value, diags *diag.Diagnostics) {
	if !field.CanSet() || field.Kind() == reflect.Interface && field.IsNil() {
		return
	}
	current, ok := field.Interface().(attr.Value)
	if !ok {
		return
	}
	settled, err := settleValue(ctx, current)
	if err != nil {
		diags.AddError("Unable to settle unknown values", err.Error())
		return
	}
	replacement := reflect.ValueOf(settled)
	if !replacement.Type().AssignableTo(field.Type()) {
		diags.AddError("Unable to settle unknown values", fmt.Sprintf("Cannot store %T in a %s field. Please report this to the provider developers.", settled, field.Type()))
		return
	}
	field.Set(replacement)
}

func settleValue(ctx context.Context, v attr.Value) (attr.Value, error) {
	switch {
	case v == nil || v.IsNull():
		return v, nil
	case v.IsUnknown():
		return nullOf(ctx, v.Type(ctx))
	}
	switch typed := v.(type) {
	case basetypes.ListValuable:
		return settleList(ctx, v, typed)
	case basetypes.SetValuable:
		return settleSet(ctx, v, typed)
	case basetypes.MapValuable:
		return settleMap(ctx, v, typed)
	case basetypes.ObjectValuable:
		return settleObject(ctx, v, typed)
	case basetypes.DynamicValuable:
		return settleDynamic(ctx, typed)
	}
	return v, nil
}

func nullOf(ctx context.Context, typ attr.Type) (attr.Value, error) {
	return typ.ValueFromTerraform(ctx, tftypes.NewValue(typ.TerraformType(ctx), nil))
}

func settleElements(ctx context.Context, in []attr.Value) ([]attr.Value, error) {
	out := make([]attr.Value, len(in))
	for i, elem := range in {
		settled, err := settleValue(ctx, elem)
		if err != nil {
			return nil, err
		}
		out[i] = settled
	}
	return out, nil
}

func settleList(ctx context.Context, original attr.Value, typed basetypes.ListValuable) (attr.Value, error) {
	list, diags := typed.ToListValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("convert list: %s", diags)
	}
	elems, err := settleElements(ctx, list.Elements())
	if err != nil {
		return nil, err
	}
	rebuilt, diags := basetypes.NewListValue(list.ElementType(ctx), elems)
	if diags.HasError() {
		return nil, fmt.Errorf("rebuild list: %s", diags)
	}
	return rewrap(ctx, original, rebuilt, func(t attr.Type) (attr.Value, diag.Diagnostics, bool) {
		listable, ok := t.(basetypes.ListTypable)
		if !ok {
			return nil, nil, false
		}
		v, d := listable.ValueFromList(ctx, rebuilt)
		return v, d, true
	})
}

func settleSet(ctx context.Context, original attr.Value, typed basetypes.SetValuable) (attr.Value, error) {
	set, diags := typed.ToSetValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("convert set: %s", diags)
	}
	elems, err := settleElements(ctx, set.Elements())
	if err != nil {
		return nil, err
	}
	rebuilt, diags := basetypes.NewSetValue(set.ElementType(ctx), elems)
	if diags.HasError() {
		return nil, fmt.Errorf("rebuild set: %s", diags)
	}
	return rewrap(ctx, original, rebuilt, func(t attr.Type) (attr.Value, diag.Diagnostics, bool) {
		settable, ok := t.(basetypes.SetTypable)
		if !ok {
			return nil, nil, false
		}
		v, d := settable.ValueFromSet(ctx, rebuilt)
		return v, d, true
	})
}

func settleMap(ctx context.Context, original attr.Value, typed basetypes.MapValuable) (attr.Value, error) {
	m, diags := typed.ToMapValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("convert map: %s", diags)
	}
	out := make(map[string]attr.Value, len(m.Elements()))
	for key, elem := range m.Elements() {
		settled, err := settleValue(ctx, elem)
		if err != nil {
			return nil, err
		}
		out[key] = settled
	}
	rebuilt, diags := basetypes.NewMapValue(m.ElementType(ctx), out)
	if diags.HasError() {
		return nil, fmt.Errorf("rebuild map: %s", diags)
	}
	return rewrap(ctx, original, rebuilt, func(t attr.Type) (attr.Value, diag.Diagnostics, bool) {
		mappable, ok := t.(basetypes.MapTypable)
		if !ok {
			return nil, nil, false
		}
		v, d := mappable.ValueFromMap(ctx, rebuilt)
		return v, d, true
	})
}

func settleObject(ctx context.Context, original attr.Value, typed basetypes.ObjectValuable) (attr.Value, error) {
	obj, diags := typed.ToObjectValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("convert object: %s", diags)
	}
	attrs := make(map[string]attr.Value, len(obj.Attributes()))
	for name, elem := range obj.Attributes() {
		settled, err := settleValue(ctx, elem)
		if err != nil {
			return nil, err
		}
		attrs[name] = settled
	}
	rebuilt, diags := basetypes.NewObjectValue(obj.AttributeTypes(ctx), attrs)
	if diags.HasError() {
		return nil, fmt.Errorf("rebuild object: %s", diags)
	}
	return rewrap(ctx, original, rebuilt, func(t attr.Type) (attr.Value, diag.Diagnostics, bool) {
		objectable, ok := t.(basetypes.ObjectTypable)
		if !ok {
			return nil, nil, false
		}
		v, d := objectable.ValueFromObject(ctx, rebuilt)
		return v, d, true
	})
}

func settleDynamic(ctx context.Context, typed basetypes.DynamicValuable) (attr.Value, error) {
	dyn, diags := typed.ToDynamicValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("convert dynamic value: %s", diags)
	}
	inner, err := settleValue(ctx, dyn.UnderlyingValue())
	if err != nil {
		return nil, err
	}
	return basetypes.NewDynamicValue(inner), nil
}

func rewrap(ctx context.Context, original, rebuilt attr.Value, convert func(attr.Type) (attr.Value, diag.Diagnostics, bool)) (attr.Value, error) {
	custom, diags, ok := convert(original.Type(ctx))
	switch {
	case !ok:
		return rebuilt, nil
	case diags.HasError():
		return nil, fmt.Errorf("convert to %s: %s", original.Type(ctx), diags)
	}
	return custom, nil
}
