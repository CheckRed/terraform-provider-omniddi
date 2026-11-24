package framework_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func TestErrorDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"approval", &client.ApprovalRequiredError{RequestID: 12, Detail: "queued", HTTPRequestID: "rid"}, []string{"Approval required (request 12)", "approval_mode", "rid"}},
		{"untracked", &client.ApprovalUntrackedError{Detail: "needs approval"}, []string{"did not return a request id", "needs approval"}},
		{"rejected", &client.ApprovalRejectedError{RequestID: 3, Status: "rejected"}, []string{"request 3", "rejected"}},
		{"task failed", &client.TaskFailedError{CorrelationID: "c1", TaskID: 9, Logs: "bad zone"}, []string{"task 9", "c1", "bad zone"}},
		{"task failed no logs", &client.TaskFailedError{CorrelationID: "c1", TaskID: 9}, []string{"(none)"}},
		{"no tasks", &client.NoTasksError{CorrelationID: "c2"}, []string{"c2", "cannot be confirmed"}},
		{"timeout tasks", &client.TimeoutError{Waiting: "tasks", CorrelationID: "c3", Cause: context.DeadlineExceeded}, []string{"c3", "Agent Tasks"}},
		{"timeout approval", &client.TimeoutError{Waiting: "approval", RequestID: 8}, []string{"request id: 8"}},
		{"missing handle", &client.MissingTaskHandleError{Method: "POST", Path: "/views/create"}, []string{"refusing to treat it as success"}},
		{"unknown tags", &client.UnknownTagsError{Names: []string{"a", "b"}}, []string{"a, b"}},
		{"api", &client.APIError{StatusCode: 409, Method: "POST", Path: "/zones/", Detail: "exists", Code: "DUP", RequestID: "rid-9"}, []string{"HTTP 409", "exists", "DUP", "rid-9"}},
		{"api empty detail", &client.APIError{StatusCode: 500, Method: "GET", Path: "/x"}, []string{"(none)"}},
		{"graphql", &client.GraphQLError{Errors: []client.GraphQLErrorItem{{Message: "denied"}}}, []string{"denied"}},
		{"wrapped", fmt.Errorf("outer: %w", &client.ApprovalRequiredError{RequestID: 4}), []string{"request 4"}},
		{"plain", errors.New("plain failure"), []string{"plain failure"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := framework.ErrorDetail(tc.err)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("detail %q lacks %q", got, want)
				}
			}
		})
	}
}

func TestDiagnosticHelpers(t *testing.T) {
	var diags diag.Diagnostics
	framework.AddClientError(&diags, "Boom", errors.New("x"))
	framework.AddSavedStateError(&diags, "Saved", errors.New("y"))
	if len(diags) != 2 || !diags.HasError() {
		t.Fatalf("diags = %v", diags)
	}
	if !strings.Contains(diags[1].Detail(), "saved to the Terraform state") {
		t.Errorf("saved-state hint missing: %s", diags[1].Detail())
	}
	if framework.AppendIfErr(&diags, "none", nil) {
		t.Error("nil error must not append")
	}
}

func TestDeletionProtectionAndForceDestroy(t *testing.T) {
	for name, tc := range map[string]struct {
		in   types.Bool
		want bool
	}{"true blocks": {types.BoolValue(true), false}, "null blocks (default)": {types.BoolNull(), false}, "unknown blocks": {types.BoolUnknown(), false}, "false allows": {types.BoolValue(false), true}} {
		var diags diag.Diagnostics
		if got := framework.CheckDeletionProtection(&diags, "omniddi_dns_zone", tc.in); got != tc.want || diags.HasError() == tc.want {
			t.Errorf("%s: got %v, diags %v", name, got, diags)
		}
	}

	var diags diag.Diagnostics
	if !framework.CheckForceDestroy(&diags, "zone", types.BoolValue(false), "") {
		t.Error("no blockers must allow deletion")
	}
	if framework.CheckForceDestroy(&diags, "zone", types.BoolValue(false), "3 records") || !diags.HasError() {
		t.Error("blockers without force_destroy must refuse")
	}
	var forced diag.Diagnostics
	if !framework.CheckForceDestroy(&forced, "zone", types.BoolValue(true), "3 records") {
		t.Error("force_destroy must allow deletion")
	}
}

func TestConfigureProviderData(t *testing.T) {
	var diags diag.Diagnostics
	if framework.ConfigureProviderData(nil, &diags) != nil || diags.HasError() {
		t.Error("nil provider data (provider not configured yet) must be silent")
	}
	if framework.ConfigureProviderData("wrong", &diags) != nil || !diags.HasError() {
		t.Error("wrong type must produce an error")
	}
	var empty diag.Diagnostics
	if framework.ConfigureProviderData(&framework.ProviderData{}, &empty) != nil || !empty.HasError() {
		t.Error("provider data without a client must produce an error")
	}
	c, _ := client.New(context.Background(), client.Config{Endpoint: "https://x.example.com", Username: "user@example.com", Password: "synthetic-password"})
	var ok diag.Diagnostics
	pd := &framework.ProviderData{Client: c}
	if framework.ConfigureProviderData(pd, &ok) != pd || ok.HasError() {
		t.Error("valid provider data must pass through")
	}
}

func TestWithTimeout(t *testing.T) {
	var diags diag.Diagnostics
	pd := &framework.ProviderData{DefaultTimeout: 90 * time.Second}

	ctx, cancel := framework.WithTimeout(context.Background(), framework.OpCreate, timeouts.Value{}, pd, &diags)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 90*time.Second || time.Until(deadline) < 80*time.Second {
		t.Errorf("provider default timeout not applied: %v", time.Until(deadline))
	}

	for _, op := range []framework.Operation{framework.OpRead, framework.OpUpdate, framework.OpDelete} {
		c, cancelOp := framework.WithTimeout(context.Background(), op, timeouts.Value{}, nil, &diags)
		if _, has := c.Deadline(); !has {
			t.Errorf("operation %d lacks a deadline", op)
		}
		cancelOp()
	}
	if diags.HasError() {
		t.Errorf("diags = %v", diags)
	}
}

func TestDesiredTagsMergesDefaultsTagsAndList(t *testing.T) {
	ctx := context.Background()
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod", "managed-by": "terraform"}}
	tags := types.MapValueMust(types.StringType, map[string]attr.Value{"env": types.StringValue("dev"), "owner": types.StringValue("netops")})
	list := types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
		"teams": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("a"), types.StringValue("b")}),
	})

	got, diags := pd.DesiredTags(ctx, tags, list)
	if diags.HasError() {
		t.Fatal(diags)
	}
	want := map[string][]string{"env": {"dev"}, "managed-by": {"terraform"}, "owner": {"netops"}, "teams": {"a", "b"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("desired = %v, want %v", got, want)
	}

	clash := types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
		"owner": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("x")}),
	})
	if _, diags := pd.DesiredTags(ctx, tags, clash); !diags.HasError() {
		t.Error("a name in both tags and tags_list must be an error")
	}

	nullDesired, diags := (*framework.ProviderData)(nil).DesiredTags(ctx, types.MapNull(types.StringType), types.MapNull(types.ListType{ElemType: types.StringType}))
	if diags.HasError() || len(nullDesired) != 0 {
		t.Errorf("null maps and nil provider data: %v %v", nullDesired, diags)
	}
}

func TestTagsFromRemoteSplitsDefaultsAndConfiguredTags(t *testing.T) {
	ctx := context.Background()
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	remote := []client.ObjectTag{
		{Name: "env", Values: []string{"prod"}},
		{Name: "owner", Values: []string{"netops"}},
		{Name: "teams", Values: []string{"a", "b"}, MultiValue: true},
		{Name: client.InheritedTagKey, Values: []string{"x"}},
		{Name: "empty", Values: nil},
	}

	t.Run("defaults are hidden from tags unless configured", func(t *testing.T) {
		model, diags := pd.TagsFromRemote(ctx, remote, types.MapNull(types.StringType), types.MapNull(types.ListType{ElemType: types.StringType}))
		if diags.HasError() {
			t.Fatal(diags)
		}
		if fmt.Sprint(model.Tags.Elements()) != `map[owner:"netops"]` {
			t.Errorf("tags = %v", model.Tags)
		}
		if len(model.TagsAll.Elements()) != 2 || model.TagsAll.Elements()["env"] == nil {
			t.Errorf("tags_all = %v", model.TagsAll)
		}
		if len(model.TagsList.Elements()) != 1 {
			t.Errorf("tags_list = %v", model.TagsList)
		}
	})

	t.Run("a configured tag equal to the default stays in tags", func(t *testing.T) {
		prior := types.MapValueMust(types.StringType, map[string]attr.Value{"env": types.StringValue("prod")})
		model, _ := pd.TagsFromRemote(ctx, remote, prior, types.MapNull(types.ListType{ElemType: types.StringType}))
		if model.Tags.Elements()["env"] == nil {
			t.Errorf("configured tag dropped: %v", model.Tags)
		}
	})

	t.Run("empty remote keeps null when config was null and empty map otherwise", func(t *testing.T) {
		nullModel, _ := pd.TagsFromRemote(ctx, nil, types.MapNull(types.StringType), types.MapNull(types.ListType{ElemType: types.StringType}))
		if !nullModel.Tags.IsNull() || !nullModel.TagsList.IsNull() || nullModel.TagsAll.IsNull() {
			t.Errorf("null handling wrong: %+v", nullModel)
		}
		emptyPrior := types.MapValueMust(types.StringType, map[string]attr.Value{})
		model, _ := pd.TagsFromRemote(ctx, nil, emptyPrior, types.MapNull(types.ListType{ElemType: types.StringType}))
		if model.Tags.IsNull() {
			t.Errorf("an explicit empty map must stay an empty map")
		}
	})
}

func TestPlannedTagsAll(t *testing.T) {
	pd := &framework.ProviderData{DefaultTags: map[string]string{"env": "prod"}}
	tags := types.MapValueMust(types.StringType, map[string]attr.Value{"owner": types.StringValue("x")})
	all, diags := pd.PlannedTagsAll(context.Background(), tags)
	if diags.HasError() || len(all.Elements()) != 2 {
		t.Errorf("all = %v, diags = %v", all, diags)
	}
	nullAll, _ := pd.PlannedTagsAll(context.Background(), types.MapNull(types.StringType))
	if len(nullAll.Elements()) != 1 {
		t.Errorf("null tags: %v", nullAll)
	}
}

func TestMergeStringMaps(t *testing.T) {
	base := map[string]string{"a": "1"}
	merged := framework.MergeStringMaps(base, map[string]string{"a": "2", "b": "3"})
	if merged["a"] != "2" || merged["b"] != "3" || base["a"] != "1" {
		t.Errorf("merged = %v, base mutated = %v", merged, base)
	}
	if got := framework.MergeStringMaps(nil, nil); got == nil {
		t.Error("nil inputs must produce an empty map")
	}
}

func TestFQDNSemanticEquality(t *testing.T) {
	ctx := context.Background()
	equal := func(a, b string) bool {
		eq, diags := framework.NewFQDNValue(a).StringSemanticEquals(ctx, framework.NewFQDNValue(b))
		return eq && !diags.HasError()
	}
	for _, pair := range [][2]string{{"Example.COM", "example.com"}, {"example.com.", "example.com"}, {"Host.Example.com.", "host.example.com"}} {
		if !equal(pair[0], pair[1]) {
			t.Errorf("%q and %q must be semantically equal", pair[0], pair[1])
		}
	}
	if equal("a.example.com", "b.example.com") {
		t.Error("different names must differ")
	}
	if _, diags := framework.NewFQDNValue("a").StringSemanticEquals(ctx, types.StringValue("a")); !diags.HasError() {
		t.Error("a non-FQDN value must be a diagnostic")
	}
}

func TestFQDNTypeRoundTrip(t *testing.T) {
	ctx := context.Background()
	typ := framework.FQDNType{}
	val, err := typ.ValueFromTerraform(ctx, tftypes.NewValue(tftypes.String, "Example.com"))
	if err != nil {
		t.Fatal(err)
	}
	fqdn, ok := val.(framework.FQDN)
	if !ok || fqdn.ValueString() != "Example.com" || !fqdn.Type(ctx).Equal(typ) {
		t.Fatalf("val = %#v", val)
	}
	if !fqdn.Equal(framework.NewFQDNValue("Example.com")) || fqdn.Equal(framework.NewFQDNValue("example.com")) || fqdn.Equal(types.StringValue("Example.com")) {
		t.Error("Equal must be exact and type-aware")
	}
	if typ.Equal(types.StringType) || !typ.Equal(framework.FQDNType{}) || typ.String() == "" {
		t.Error("type equality")
	}
	if _, ok := typ.ValueType(ctx).(framework.FQDN); !ok {
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
	if !framework.NewFQDNNull().IsNull() || !framework.NewFQDNUnknown().IsUnknown() {
		t.Error("constructors")
	}
	if framework.NormalizeFQDN(" Example.COM. ") != "example.com" {
		t.Error("NormalizeFQDN")
	}
}
