package framework

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type NaturalKeyResolver func(ctx context.Context, key string) (int64, error)

func IdentitySchema() identityschema.Schema {
	return identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			AttrID: identityschema.Int64Attribute{RequiredForImport: true},
		},
	}
}

func SetIdentity(ctx context.Context, identity *tfsdk.ResourceIdentity, id int64) diag.Diagnostics {
	if identity == nil {
		return nil
	}
	return identity.SetAttribute(ctx, path.Root(AttrID), id)
}

func SetIDAndIdentity(ctx context.Context, state *tfsdk.State, identity *tfsdk.ResourceIdentity, id int64) diag.Diagnostics {
	diags := state.SetAttribute(ctx, path.Root(AttrID), id)
	diags.Append(SetIdentity(ctx, identity, id)...)
	return diags
}

func ImportByIDOrKey(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, resolve NaturalKeyResolver) {
	id, ok := importID(ctx, req, resp, resolve)
	if !ok {
		return
	}
	resp.Diagnostics.Append(SetIDAndIdentity(ctx, &resp.State, resp.Identity, id)...)
}

func importID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, resolve NaturalKeyResolver) (int64, bool) {
	if req.ID == "" && req.Identity != nil {
		var id int64
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root(AttrID), &id)...)
		return id, !resp.Diagnostics.HasError()
	}
	key := strings.TrimSpace(req.ID)
	if id, err := strconv.ParseInt(key, 10, 64); err == nil {
		if id <= 0 {
			resp.Diagnostics.AddError("Invalid import id", "The numeric id must be a positive integer.")
			return 0, false
		}
		return id, true
	}
	if resolve == nil || key == "" {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("Expected a numeric id, got %q.", req.ID))
		return 0, false
	}
	id, err := resolve(ctx, key)
	switch {
	case client.IsNotFound(err):
		resp.Diagnostics.AddError("Import target not found", fmt.Sprintf("No object matches %q.", key))
		return 0, false
	case err != nil:
		AddClientError(&resp.Diagnostics, "Unable to resolve import key", err)
		return 0, false
	}
	return id, true
}

func ImportDefaults(ctx context.Context, state *tfsdk.State, attributes ...string) diag.Diagnostics {
	defaults := map[string]bool{AttrDeletionProtection: true, AttrForceDestroy: false}
	var diags diag.Diagnostics
	for _, name := range attributes {
		value, known := defaults[name]
		if !known {
			diags.AddError("Unknown import default", fmt.Sprintf("No import default is defined for attribute %q.", name))
			continue
		}
		diags.Append(state.SetAttribute(ctx, path.Root(name), value)...)
	}
	return diags
}
