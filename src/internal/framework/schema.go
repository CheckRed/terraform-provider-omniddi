package framework

import (
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	AttrID                 = "id"
	AttrApprovalMode       = "approval_mode"
	AttrDeletionProtection = "deletion_protection"
	AttrForceDestroy       = "force_destroy"
	AttrTags               = "tags"
	AttrTagsList           = "tags_list"
	AttrTagsAll            = "tags_all"
	AttrTimeouts           = "timeouts"
)

func IDAttribute() schema.Int64Attribute {
	return schema.Int64Attribute{
		Computed:            true,
		MarkdownDescription: "The OmniDDI numeric identifier.",
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func ApprovalModeAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Optional: true,
		MarkdownDescription: "How to behave when OmniDDI requires change approval: `fail` returns an error carrying the " +
			"request id, `wait` waits until the request is applied. Defaults to the provider `approval_mode`.",
		Validators: []validator.String{stringvalidator.OneOf(string(ApprovalModeFail), string(ApprovalModeWait))},
	}
}

func DeletionProtectionAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(true),
		MarkdownDescription: "Refuse to delete the object while true. Set to false and apply before destroying. Defaults to `true`.",
	}
}

func ForceDestroyAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
		MarkdownDescription: "Allow deletion even when dependent objects would be removed with it. Defaults to `false`.",
	}
}

func TagsAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		AttrTags: schema.MapAttribute{
			Optional:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Single-valued OmniDDI tags. Tag definitions must already exist. Merged with provider `default_tags`.",
		},
		AttrTagsList: schema.MapAttribute{
			Optional:            true,
			ElementType:         types.ListType{ElemType: types.StringType},
			MarkdownDescription: "Multi-valued (list) OmniDDI tags. Tag definitions must already exist.",
		},
		AttrTagsAll: schema.MapAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "All single-valued tags on the object, including those inherited from provider `default_tags`.",
		},
	}
}
