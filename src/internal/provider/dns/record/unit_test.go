package record

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

func TestGraphQLOperationsMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, recordByIDQuery, recordsInZoneQuery, recordKeyQuery, zoneLookupQuery)
}

func stringList(t *testing.T, values ...string) types.List {
	t.Helper()
	list, diags := types.ListValueFrom(context.Background(), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("list: %v", diags)
	}
	return list
}

func baseModel(recordType string) recordModel {
	return recordModel{
		Type:           types.StringValue(recordType),
		Values:         types.ListNull(types.StringType),
		DisabledValues: types.SetNull(types.StringType),
		DataJSON:       newNormalizedJSONNull(),
	}
}

func TestExpandDataPerValueForm(t *testing.T) {
	ctx := context.Background()
	mx := baseModel("MX")
	mx.MX = []mxModel{{Preference: types.Int64Value(10), Exchange: framework.NewFQDNValue("mail.example.com")}}
	srv := baseModel("SRV")
	srv.SRV = []srvModel{{Priority: types.Int64Value(1), Weight: types.Int64Value(2), Port: types.Int64Value(3), Host: framework.NewFQDNValue("h.example.com")}}
	caa := baseModel("CAA")
	caa.CAA = []caaModel{{Flag: types.Int64Value(0), Tag: types.StringValue("issue"), Value: types.StringValue("ca.example")}}
	single := baseModel("A")
	single.Values = stringList(t, "192.0.2.1")
	multi := baseModel("A")
	multi.Values = stringList(t, "192.0.2.1", "192.0.2.2")
	cname := baseModel("CNAME")
	cname.Values = stringList(t, "t.example.com")
	js := baseModel("SVCB")
	js.DataJSON = newNormalizedJSON(`{"priority":1}`)

	tests := map[string]struct {
		model recordModel
		want  any
	}{
		"mx":     {mx, []map[string]any{{"preference": int64(10), "exchange": "mail.example.com"}}},
		"srv":    {srv, []map[string]any{{"priority": int64(1), "weight": int64(2), "port": int64(3), "host": "h.example.com"}}},
		"caa":    {caa, []map[string]any{{"flag": int64(0), "tag": "issue", "value": "ca.example"}}},
		"single": {single, "192.0.2.1"},
		"multi":  {multi, []string{"192.0.2.1", "192.0.2.2"}},
		"cname":  {cname, "t.example.com"},
	}
	for name, tc := range tests {
		got, diags := expandData(ctx, tc.model)
		if diags.HasError() || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v (%v), want %#v", name, got, diags, tc.want)
		}
	}
	got, diags := expandData(ctx, js)
	if diags.HasError() || !sameJSONShape(got, map[string]any{"priority": 1}) {
		t.Errorf("data_json: got %#v (%v)", got, diags)
	}
	if _, diags := expandData(ctx, baseModel("A")); !diags.HasError() {
		t.Errorf("a model without a value form must fail")
	}
}

func TestExpandEnabledFlags(t *testing.T) {
	ctx := context.Background()
	model := baseModel("A")
	model.Values = stringList(t, "192.0.2.1", "192.0.2.2", "192.0.2.3")
	model.DisabledValues, _ = types.SetValueFrom(ctx, types.StringType, []string{"192.0.2.2"})
	flags, _ := expandEnabledFlags(ctx, model, []string{"a", "b", "c"})
	if !reflect.DeepEqual(flags, []bool{true, false, true}) {
		t.Errorf("flags = %v", flags)
	}
	if flags, _ := expandEnabledFlags(ctx, baseModel("CNAME"), "x"); flags != nil {
		t.Errorf("CNAME has no per-value flags, got %v", flags)
	}
	if flags, _ := expandEnabledFlags(ctx, baseModel("HINFO"), []any{"a", "b"}); flags != nil {
		t.Errorf("HINFO flags are skipped, got %v", flags)
	}
	for _, recordType := range []string{"MX", "SRV", "DS", "NAPTR"} {
		if flags, _ := expandEnabledFlags(ctx, baseModel(recordType), []map[string]any{{}, {}}); flags != nil {
			t.Errorf("%s flags must be omitted so values disabled in OmniDDI stay disabled, got %v", recordType, flags)
		}
	}
}

func TestCreateAndUpdateBodies(t *testing.T) {
	ctx := context.Background()
	plan := baseModel("A")
	plan.ZoneID = types.Int64Value(7)
	plan.Name = framework.NewHostnameValue("WWW")
	plan.TTL = types.Int64Unknown()
	plan.RecordSetOrdering = types.StringUnknown()
	plan.Values = stringList(t, "192.0.2.1")
	body, diags := createBody(ctx, plan)
	if diags.HasError() || body["name"] != "www" || body["data"] != "192.0.2.1" {
		t.Fatalf("create body = %v (%v)", body, diags)
	}
	for _, key := range []string{"ttl", "record_set_ordering", "enabled_flags"} {
		if _, present := body[key]; present {
			t.Errorf("%s must be omitted when unset: %v", key, body)
		}
	}

	state := plan
	state.TTL = types.Int64Value(3600)
	state.RecordSetOrdering = types.StringNull()
	next := state
	next.Name = framework.NewHostnameValue("www")
	if body, _ := updateBody(ctx, next, state); len(body) != 0 {
		t.Errorf("an unchanged record must not produce a write, got %v", body)
	}
	next.TTL = types.Int64Value(60)
	next.Values = stringList(t, "192.0.2.9")
	next.Name = framework.NewHostnameValue("other")
	next.RecordSetOrdering = types.StringValue("cyclic")
	body, _ = updateBody(ctx, next, state)
	if body["name"] != "other" || body["ttl"] != int64(60) || body["data"] != "192.0.2.9" || body["record_set_ordering"] != "cyclic" {
		t.Errorf("update body = %v", body)
	}
	if !reflect.DeepEqual(body["enabled_flags"], []bool{true}) {
		t.Errorf("a value change must resend the flags, got %v", body["enabled_flags"])
	}
}

func dto(recordType string, data any, states ...valueState) *recordDTO {
	ttl, name := int64(300), "www"
	return &recordDTO{ID: 5, ZoneID: 7, Name: &name, Type: recordType, TTL: &ttl, EffectiveTTL: &ttl, Data: data, Values: states}
}

func TestFlattenStringValuesKeepsPriorSpelling(t *testing.T) {
	ctx := context.Background()
	prior := baseModel("CNAME")
	prior.Name = framework.NewHostnameValue("WWW")
	prior.Values = stringList(t, "Target.Example.com.")
	got, diags := flattenRecord(ctx, dto("CNAME", map[string]any{"values": []any{"target.example.com"}}), prior)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if got.Values.Elements()[0].String() != `"Target.Example.com."` || got.Name.ValueString() != "WWW" {
		t.Errorf("prior spelling was not kept: %v %v", got.Values, got.Name)
	}
	if !got.DisabledValues.IsNull() {
		t.Errorf("single-valued types never report disabled values")
	}
}

func TestFlattenDisabledValuesAndEmptySetSemantics(t *testing.T) {
	ctx := context.Background()
	states := []valueState{{Value: "192.0.2.1", Enabled: true}, {Value: "192.0.2.2", Enabled: false}}
	got, _ := flattenRecord(ctx, dto("A", nil, states...), baseModel("A"))
	if len(got.DisabledValues.Elements()) != 1 {
		t.Errorf("disabled = %v", got.DisabledValues)
	}
	allEnabled := []valueState{{Value: "192.0.2.1", Enabled: true}}
	if got, _ := flattenRecord(ctx, dto("A", nil, allEnabled...), baseModel("A")); !got.DisabledValues.IsNull() {
		t.Errorf("no disabled values and a null prior must stay null")
	}
	prior := baseModel("A")
	prior.DisabledValues, _ = types.SetValueFrom(ctx, types.StringType, []string{})
	if got, _ := flattenRecord(ctx, dto("A", nil, allEnabled...), prior); got.DisabledValues.IsNull() {
		t.Errorf("an empty configured set must stay empty")
	}
}

func TestFlattenTypedBlocksPerType(t *testing.T) {
	ctx := context.Background()
	mx, _ := flattenRecord(ctx, dto("MX", nil, valueState{Value: map[string]any{"preference": 10.0, "exchange": "m.example.com"}, Enabled: true}), baseModel("MX"))
	if len(mx.MX) != 1 || mx.MX[0].Preference.ValueInt64() != 10 || !mx.DataJSON.IsNull() {
		t.Errorf("mx = %+v", mx)
	}
	srv, _ := flattenRecord(ctx, dto("SRV", nil, valueState{Value: map[string]any{"priority": 1.0, "weight": 2.0, "port": 3.0, "target": "h.example.com"}, Enabled: true}), baseModel("SRV"))
	if len(srv.SRV) != 1 || srv.SRV[0].Host.ValueString() != "h.example.com" {
		t.Errorf("srv legacy target not mapped: %+v", srv.SRV)
	}
	caa, _ := flattenRecord(ctx, dto("CAA", []any{map[string]any{"flags": 128.0, "tag": "iodef", "value": "mailto:a@example.com"}}), baseModel("CAA"))
	if len(caa.CAA) != 1 || caa.CAA[0].Flag.ValueInt64() != 128 {
		t.Errorf("caa = %+v", caa.CAA)
	}
	odd, _ := flattenRecord(ctx, dto("MX", []any{"10 mail.example.com"}), baseModel("MX"))
	if len(odd.MX) != 0 || odd.DataJSON.IsNull() {
		t.Errorf("unrecognized shapes must fall back to data_json: %+v", odd)
	}
}

func TestFlattenDataJSONShapes(t *testing.T) {
	ctx := context.Background()
	object, _ := flattenRecord(ctx, dto("SVCB", map[string]any{"priority": 1.0, "host": "s.example.com"}), baseModel("SVCB"))
	if object.DataJSON.ValueString() != `{"host":"s.example.com","priority":1}` {
		t.Errorf("object-shaped type = %s", object.DataJSON.ValueString())
	}
	list, _ := flattenRecord(ctx, dto("NAPTR", []any{map[string]any{"order": 1.0}}), baseModel("NAPTR"))
	if list.DataJSON.ValueString() != `[{"order":1}]` {
		t.Errorf("list-shaped type = %s", list.DataJSON.ValueString())
	}
	prior := baseModel("NAPTR")
	prior.DataJSON = newNormalizedJSON(`{"order":1}`)
	single, _ := flattenRecord(ctx, dto("NAPTR", []any{map[string]any{"order": 1.0}}), prior)
	if single.DataJSON.ValueString() != `{"order":1}` {
		t.Errorf("a configured single object must stay an object: %s", single.DataJSON.ValueString())
	}
}

func TestFlattenNameTTLAndOrdering(t *testing.T) {
	ctx := context.Background()
	remote := dto("A", nil, valueState{Value: "192.0.2.1", Enabled: true})
	remote.Name, remote.TTL = nil, nil
	ordering := "random"
	remote.RecordSetOrdering = &ordering
	got, _ := flattenRecord(ctx, remote, baseModel("A"))
	if got.Name.ValueString() != "@" || got.TTL.ValueInt64() != 300 || got.RecordSetOrdering.ValueString() != "random" {
		t.Errorf("name=%v ttl=%v ordering=%v", got.Name, got.TTL, got.RecordSetOrdering)
	}
	prior := baseModel("A")
	prior.RecordSetOrdering = types.StringValue("cyclic")
	remote.RecordSetOrdering = nil
	if got, _ := flattenRecord(ctx, remote, prior); got.RecordSetOrdering.ValueString() != "cyclic" {
		t.Errorf("a configured ordering the backend does not persist must be kept")
	}
}

func TestRemoteTokensUnwrapStorageShapes(t *testing.T) {
	cases := map[string][]any{
		"wrapped": unwrapTokens(map[string]any{"values": []any{"a", "b"}, "value_meta": []any{}}),
		"nested":  unwrapTokens(map[string]any{"data": []any{"a", "b"}}),
		"list":    unwrapTokens([]any{"a", "b"}),
		"object":  unwrapTokens(map[string]any{"priority": 1.0, "host": "h"}),
		"scalar":  unwrapTokens("a"),
		"nothing": unwrapTokens(nil),
	}
	want := map[string]int{"wrapped": 2, "nested": 2, "list": 2, "object": 1, "scalar": 1, "nothing": 0}
	for name, tokens := range cases {
		if len(tokens) != want[name] {
			t.Errorf("%s: %d tokens, want %d", name, len(tokens), want[name])
		}
	}
}

func TestNormalizedJSONSemanticEquality(t *testing.T) {
	ctx := context.Background()
	a := newNormalizedJSON(`{"b": 2, "a": [1, 2]}`)
	equal, _ := a.StringSemanticEquals(ctx, newNormalizedJSON(`{"a":[1,2],"b":2}`))
	different, _ := a.StringSemanticEquals(ctx, newNormalizedJSON(`{"a":[2,1],"b":2}`))
	broken, _ := a.StringSemanticEquals(ctx, newNormalizedJSON(`{`))
	if !equal || different || broken {
		t.Errorf("equal=%v different=%v broken=%v", equal, different, broken)
	}
	if _, err := decodeJSON(`{"a":1} trailing`); err == nil {
		t.Errorf("trailing data must be rejected")
	}
	if !newNormalizedJSON(`1`).Equal(newNormalizedJSON(`1`)) || newNormalizedJSON(`1`).Equal(newNormalizedJSONUnknown()) {
		t.Errorf("value equality")
	}
}

func TestNormalizedJSONTypeBehavior(t *testing.T) {
	typ := normalizedJSONType{}
	if !typ.Equal(normalizedJSONType{}) || typ.Equal(types.StringType) || typ.String() == "" {
		t.Errorf("type equality")
	}
	value, diags := typ.ValueFromString(context.Background(), types.StringValue(`{}`))
	if diags.HasError() || value.(normalizedJSON).ValueString() != `{}` {
		t.Errorf("ValueFromString: %v", diags)
	}
	if _, ok := typ.ValueType(context.Background()).(normalizedJSON); !ok {
		t.Errorf("ValueType")
	}
}

func TestSameStringValueAndNames(t *testing.T) {
	if !sameStringValue("AAAA", "2001:db8:0::1", "2001:db8::1") || sameStringValue("A", "192.0.2.1", "192.0.2.2") {
		t.Errorf("address comparison")
	}
	if !sameStringValue("NS", "NS1.example.com.", "ns1.example.com") || sameStringValue("TXT", "A", "a") {
		t.Errorf("hostname vs text comparison")
	}
	if normalizeName("  ") != "@" || normalizeName("WWW") != "www" {
		t.Errorf("name normalization")
	}
	if !sameOwnerName(nil, "@") || sameOwnerName(nil, "www") {
		t.Errorf("apex matching")
	}
}

func TestRecordTypeCatalog(t *testing.T) {
	if len(supportedTypes) != 37 {
		t.Errorf("the backend accepts 37 types, catalog has %d", len(supportedTypes))
	}
	for _, recordType := range supportedTypes {
		if allowedFormsDescription(recordType) == "" || len(allowedFormNames(recordType)) == 0 {
			t.Errorf("%s has no value form", recordType)
		}
	}
}

func TestOwnerNameKeepsTrailingDotSignificant(t *testing.T) {
	ctx := context.Background()
	relative, absolute, upper := framework.NewHostnameValue("www"), framework.NewHostnameValue("www."), framework.NewHostnameValue("WWW")
	if equal, _ := relative.StringSemanticEquals(ctx, upper); !equal {
		t.Errorf("WWW and www are the same owner name")
	}
	if equal, _ := relative.StringSemanticEquals(ctx, absolute); equal {
		t.Errorf("www is zone-relative and www. is absolute: they must differ")
	}
	if sameOwnerName(strPtr("www."), "www") || !sameOwnerName(strPtr("WWW"), "www") {
		t.Errorf("owner name lookup must compare dots exactly and case-insensitively")
	}

	remote := dto("A", nil, valueState{Value: "192.0.2.1", Enabled: true})
	prior := baseModel("A")
	prior.Name = framework.NewHostnameValue("www")
	if got, _ := flattenRecord(ctx, remote, prior); got.Name.ValueString() != "www" {
		t.Errorf("an equal name keeps the prior spelling, got %q", got.Name.ValueString())
	}
	prior.Name = framework.NewHostnameValue("WWW")
	if got, _ := flattenRecord(ctx, remote, prior); got.Name.ValueString() != "WWW" {
		t.Errorf("a case-only difference keeps the prior spelling, got %q", got.Name.ValueString())
	}
	prior.Name = framework.NewHostnameValue("www.")
	if got, _ := flattenRecord(ctx, remote, prior); got.Name.ValueString() != "www" {
		t.Errorf("a trailing-dot difference is real drift, got %q", got.Name.ValueString())
	}
}

func strPtr(s string) *string { return &s }
