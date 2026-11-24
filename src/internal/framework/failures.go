package framework

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

const (
	taintedNote = "The object was created in OmniDDI and saved to the Terraform state, but a later step failed, so Terraform marks it tainted: " +
		"the next `terraform apply` destroys and recreates it. Review that plan before approving it, or run `terraform untaint` on the " +
		"resource to keep the object as it is."
	protectionLoweredNote = " deletion_protection was set to false in the state so the replacement is not refused."
	updateUnconfirmedNote = "The change was sent to OmniDDI but its completion could not be confirmed. The Terraform state was left at its " +
		"previous values, so the next `terraform plan` (which refreshes from OmniDDI) shows whatever still differs; run `terraform apply` again to retry."
)

func CreateFailedAfterSave(ctx context.Context, resp *resource.CreateResponse, summary string, err error) {
	note := taintedNote
	if lowerDeletionProtection(ctx, resp) {
		note += protectionLoweredNote
	}
	resp.Diagnostics.AddError(summary, ErrorDetail(err)+"\n\n"+note)
}

func lowerDeletionProtection(ctx context.Context, resp *resource.CreateResponse) bool {
	if resp.State.Raw.IsNull() || resp.State.Schema == nil {
		return false
	}
	if _, ok := resp.State.Schema.GetAttributes()[AttrDeletionProtection]; !ok {
		return false
	}
	diags := resp.State.SetAttribute(ctx, path.Root(AttrDeletionProtection), false)
	resp.Diagnostics.Append(diags...)
	return !diags.HasError()
}

func UpdateFailedAfterWrite(req resource.UpdateRequest, resp *resource.UpdateResponse, summary string, err error) {
	resp.State.Raw = req.State.Raw.Copy()
	resp.Diagnostics.AddError(summary, ErrorDetail(err)+"\n\n"+updateUnconfirmedNote)
}
