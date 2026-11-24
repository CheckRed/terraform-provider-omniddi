package zone

import (
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

const (
	defaultDNSPort = 53

	typePrimary   = "Primary"
	typeSecondary = "Secondary"
	typeForward   = "Forward"
	typeReverse   = "Reverse"
)

var (
	zoneTypes     = []string{typePrimary, typeSecondary, typeForward, typeReverse}
	networkScopes = []string{"Internal", "External"}
)

type zoneModel struct {
	ID                     types.Int64    `tfsdk:"id"`
	ViewID                 types.Int64    `tfsdk:"view_id"`
	Name                   framework.FQDN `tfsdk:"name"`
	ZoneType               types.String   `tfsdk:"zone_type"`
	NetworkScope           types.String   `tfsdk:"network_scope"`
	Protocol               types.String   `tfsdk:"protocol"`
	CIDR                   types.String   `tfsdk:"cidr"`
	TTL                    types.Int64    `tfsdk:"ttl"`
	Refresh                types.Int64    `tfsdk:"refresh"`
	Retry                  types.Int64    `tfsdk:"retry"`
	Expiry                 types.Int64    `tfsdk:"expiry"`
	Minimum                types.Int64    `tfsdk:"minimum"`
	Email                  types.String   `tfsdk:"email"`
	PrimaryNameServer      types.String   `tfsdk:"primary_name_server"`
	PrimaryServers         types.List     `tfsdk:"primary_servers"`
	Nameservers            types.List     `tfsdk:"nameservers"`
	Policy                 types.String   `tfsdk:"policy"`
	EnableDynamicDNS       types.Bool     `tfsdk:"enable_dynamic_dns"`
	EnableDNSSEC           types.Bool     `tfsdk:"enable_dnssec"`
	AutoCreatePTRRecords   types.Bool     `tfsdk:"auto_create_ptr_records"`
	EnableZoneTransfer     types.Bool     `tfsdk:"enable_zone_transfer"`
	SecureSync             types.Bool     `tfsdk:"secure_sync"`
	ZoneACLID              types.Int64    `tfsdk:"zone_acl_id"`
	ZoneTransferTsigKeyID  types.Int64    `tfsdk:"zone_transfer_tsig_key_id"`
	DynamicDNSTsigKeyID    types.Int64    `tfsdk:"dynamic_dns_tsig_key_id"`
	SecondarySyncTsigKeyID types.Int64    `tfsdk:"secondary_sync_tsig_key_id"`
	BusinessUnitIDs        types.List     `tfsdk:"business_unit_ids"`
	DeletionProtection     types.Bool     `tfsdk:"deletion_protection"`
	ForceDestroy           types.Bool     `tfsdk:"force_destroy"`
	Tags                   types.Map      `tfsdk:"tags"`
	TagsList               types.Map      `tfsdk:"tags_list"`
	TagsAll                types.Map      `tfsdk:"tags_all"`
	SOASerial              types.Int64    `tfsdk:"soa_serial"`
	RecordsCount           types.Int64    `tfsdk:"records_count"`
	ViewName               types.String   `tfsdk:"view_name"`
	CreatedOn              types.String   `tfsdk:"created_on"`
	UpdatedOn              types.String   `tfsdk:"updated_on"`
	ApprovalMode           types.String   `tfsdk:"approval_mode"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

type primaryServerModel struct {
	IP        types.String `tfsdk:"ip"`
	Port      types.Int64  `tfsdk:"port"`
	TsigKeyID types.Int64  `tfsdk:"tsig_key_id"`
}

var (
	primaryServerAttrTypes = map[string]attr.Type{"ip": types.StringType, "port": types.Int64Type, "tsig_key_id": types.Int64Type}
	primaryServerType      = types.ObjectType{AttrTypes: primaryServerAttrTypes}
)
