package framework_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func protectedSchema(withProtection bool) schema.Schema {
	attrs := map[string]schema.Attribute{"id": schema.Int64Attribute{Computed: true}}
	if withProtection {
		attrs["deletion_protection"] = schema.BoolAttribute{Optional: true, Computed: true}
	}
	return schema.Schema{Attributes: attrs}
}

func protectedState(t *testing.T, s schema.Schema, withProtection bool) tfsdk.State {
	t.Helper()
	values := map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.Number, 7)}
	if withProtection {
		values["deletion_protection"] = tftypes.NewValue(tftypes.Bool, true)
	}
	return tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(context.Background()), values)}
}

func TestCreateFailedAfterSaveLowersDeletionProtectionSoTheTaintedObjectCanBeReplaced(t *testing.T) {
	ctx := context.Background()
	s := protectedSchema(true)
	resp := &resource.CreateResponse{State: protectedState(t, s, true)}

	framework.CreateFailedAfterSave(ctx, resp, "Zone was created but not confirmed", &client.TaskFailedError{TaskID: 4, CorrelationID: "c", Logs: "bad"})

	var protection types.Bool
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("deletion_protection"), &protection)...)
	if protection.ValueBool() || protection.IsNull() {
		t.Errorf("deletion_protection must be false in the saved state, got %v", protection)
	}
	var id types.Int64
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if id.ValueInt64() != 7 {
		t.Errorf("the rest of the state must be kept, id = %v", id)
	}
	errs := resp.Diagnostics.Errors()
	if len(errs) != 1 || errs[0].Summary() != "Zone was created but not confirmed" {
		t.Fatalf("diagnostics = %v", resp.Diagnostics)
	}
	for _, want := range []string{"agent task 4", "tainted", "destroys and recreates", "terraform untaint", "deletion_protection was set to false"} {
		if !strings.Contains(errs[0].Detail(), want) {
			t.Errorf("detail lacks %q: %s", want, errs[0].Detail())
		}
	}
}

func TestCreateFailedAfterSaveWithoutADeletionProtectionAttribute(t *testing.T) {
	s := protectedSchema(false)
	resp := &resource.CreateResponse{State: protectedState(t, s, false)}
	framework.CreateFailedAfterSave(context.Background(), resp, "Failed", errors.New("boom"))

	errs := resp.Diagnostics.Errors()
	if len(errs) != 1 || strings.Contains(errs[0].Detail(), "deletion_protection") || !strings.Contains(errs[0].Detail(), "tainted") {
		t.Fatalf("diagnostics = %v", resp.Diagnostics)
	}
}

func TestCreateFailedAfterSaveWithNothingSaved(t *testing.T) {
	s := protectedSchema(true)
	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(context.Background()), nil)}}
	framework.CreateFailedAfterSave(context.Background(), resp, "Failed", errors.New("boom"))
	if len(resp.Diagnostics.Errors()) != 1 || !resp.State.Raw.IsNull() {
		t.Fatalf("a null state must stay null: %v", resp.Diagnostics)
	}
}

func TestUpdateFailedAfterWriteKeepsThePriorStateAndExplainsRetry(t *testing.T) {
	ctx := context.Background()
	s := protectedSchema(true)
	prior := protectedState(t, s, true)
	planned := protectedState(t, s, true)
	if err := planned.SetAttribute(ctx, path.Root("deletion_protection"), false); err != nil {
		t.Fatal(err)
	}
	resp := &resource.UpdateResponse{State: planned}

	framework.UpdateFailedAfterWrite(resource.UpdateRequest{State: prior}, resp, "Zone update was not confirmed", errors.New("task failed"))

	if !resp.State.Raw.Equal(prior.Raw) {
		t.Error("the state must be restored to the prior values so the next plan re-detects the change")
	}
	errs := resp.Diagnostics.Errors()
	if len(errs) != 1 {
		t.Fatalf("diagnostics = %v", resp.Diagnostics)
	}
	for _, want := range []string{"task failed", "could not be confirmed", "left at its previous values", "terraform apply"} {
		if !strings.Contains(errs[0].Detail(), want) {
			t.Errorf("detail lacks %q: %s", want, errs[0].Detail())
		}
	}
	if strings.Contains(errs[0].Detail(), "tainted") {
		t.Errorf("updates are never tainted: %s", errs[0].Detail())
	}
}

func TestErrorDetailForNewErrorTypes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"pending change", &client.PendingChangeError{RequestID: 21}, []string{"Another change to this object is pending approval (request 21)", "Approve or reject request 21"}},
		{"create approval", &client.ApprovalRequiredError{RequestID: 5, Detail: "queued", Adopts: true}, []string{"the provider adopts the approved object", "Approve request 5"}},
		{"update approval", &client.ApprovalRequiredError{RequestID: 6, Detail: "queued"}, []string{"Approve request 6 in OmniDDI, then run `terraform apply` again."}},
		{"too large", &client.ResponseTooLargeError{Method: "GET", Path: "/x", Limit: 10}, []string{"response exceeds 10 bytes"}},
		{"overflow", &client.PageOverflowError{Query: "zones", PageSize: 100, MaxPages: 2}, []string{"zones returned more than 200 items"}},
		{"typed not found", client.NewNotFound("zone"), []string{"zone not found"}},
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
	if strings.Contains(framework.ErrorDetail(&client.ApprovalRequiredError{RequestID: 6}), "adopts") {
		t.Error("only creates adopt")
	}
}

func TestHandleReadErrorFollowsIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := protectedSchema(true)

	gone := &resource.ReadResponse{State: protectedState(t, s, true)}
	if !framework.HandleReadError(ctx, gone, client.NewNotFound("zone")) || !gone.State.Raw.IsNull() || gone.Diagnostics.HasError() {
		t.Errorf("a not-found error must drop the resource from state: %v", gone.Diagnostics)
	}

	htmlBody := &client.APIError{StatusCode: 404, Method: "GET", Path: "/zones/1", Detail: "<html>"}
	kept := &resource.ReadResponse{State: protectedState(t, s, true)}
	if !framework.HandleReadError(ctx, kept, htmlBody) || kept.State.Raw.IsNull() || !kept.Diagnostics.HasError() {
		t.Error("a 404 without a JSON detail is an ordinary error and must keep the state")
	}

	if framework.HandleReadError(ctx, &resource.ReadResponse{State: protectedState(t, s, true)}, nil) {
		t.Error("nil is not handled")
	}
	if framework.IgnoreNotFound(client.NewNotFound("x")) != nil || framework.IgnoreNotFound(htmlBody) == nil {
		t.Error("IgnoreNotFound must follow IsNotFound")
	}
}
