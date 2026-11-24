package framework

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func CheckDeletionProtection(diags *diag.Diagnostics, resourceType string, protected types.Bool) bool {
	if protected.IsNull() || protected.IsUnknown() || protected.ValueBool() {
		diags.AddError("Deletion protection is enabled", fmt.Sprintf(
			"%s has deletion_protection = true. Set deletion_protection = false, run `terraform apply`, then destroy it.", resourceType))
		return false
	}
	return true
}

func CheckForceDestroy(diags *diag.Diagnostics, resourceType string, force types.Bool, blockers string) bool {
	if blockers == "" || force.ValueBool() {
		return true
	}
	diags.AddError("Refusing to delete with dependent objects", fmt.Sprintf(
		"%s still has %s that would be deleted with it. Remove them first, or set force_destroy = true and apply before destroying.",
		resourceType, blockers))
	return false
}
