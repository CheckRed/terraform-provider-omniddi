package record

import (
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

type mxModel struct {
	Preference types.Int64    `tfsdk:"preference"`
	Exchange   framework.FQDN `tfsdk:"exchange"`
}

type srvModel struct {
	Priority types.Int64    `tfsdk:"priority"`
	Weight   types.Int64    `tfsdk:"weight"`
	Port     types.Int64    `tfsdk:"port"`
	Host     framework.FQDN `tfsdk:"host"`
}

type caaModel struct {
	Flag  types.Int64  `tfsdk:"flag"`
	Tag   types.String `tfsdk:"tag"`
	Value types.String `tfsdk:"value"`
}

type recordModel struct {
	ID                types.Int64        `tfsdk:"id"`
	ZoneID            types.Int64        `tfsdk:"zone_id"`
	Name              framework.Hostname `tfsdk:"name"`
	Type              types.String       `tfsdk:"type"`
	TTL               types.Int64        `tfsdk:"ttl"`
	Values            types.List         `tfsdk:"values"`
	DisabledValues    types.Set          `tfsdk:"disabled_values"`
	MX                []mxModel          `tfsdk:"mx"`
	SRV               []srvModel         `tfsdk:"srv"`
	CAA               []caaModel         `tfsdk:"caa"`
	DataJSON          normalizedJSON     `tfsdk:"data_json"`
	RecordSetOrdering types.String       `tfsdk:"record_set_ordering"`
	ApprovalMode      types.String       `tfsdk:"approval_mode"`
	ViewID            types.Int64        `tfsdk:"view_id"`
	ZoneName          types.String       `tfsdk:"zone_name"`
	EffectiveTTL      types.Int64        `tfsdk:"effective_ttl"`
	IsAutoCreated     types.Bool         `tfsdk:"is_auto_created"`
	CreatedOn         types.String       `tfsdk:"created_on"`
	UpdatedOn         types.String       `tfsdk:"updated_on"`
	Tags              types.Map          `tfsdk:"tags"`
	TagsList          types.Map          `tfsdk:"tags_list"`
	TagsAll           types.Map          `tfsdk:"tags_all"`
	Timeouts          timeouts.Value     `tfsdk:"timeouts"`
}
