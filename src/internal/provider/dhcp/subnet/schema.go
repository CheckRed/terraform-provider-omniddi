package subnet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

var leaseTimeUnits = []string{"secs", "mins", "hrs", "days"}

func (r *subnetResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Turns DHCP service on for an existing IPAM subnet and manages its DHCP settings. The IPAM subnet itself " +
			"(name, description, VLAN label, owner, tags and placement) belongs to `omniddi_ipam_subnet`; this resource never changes those. " +
			"`id` and `subnet_id` are the same `networks.id`.\n\n" +
			"**Create promotes** the IPAM subnet to a DHCP subnet in the given server group. **Destroy demotes** it: DHCP stops being served " +
			"for the subnet but the IPAM subnet, its addresses, reservations and tags remain. The resource never deletes the IPAM subnet. " +
			"Promoting, changing settings and demoting can each need approval in OmniDDI; see `approval_mode`. If the IPAM subnet already " +
			"serves DHCP, import it instead of creating it.",
		Attributes: map[string]schema.Attribute{
			framework.AttrID:                 idAttribute(),
			framework.AttrApprovalMode:       framework.ApprovalModeAttribute(),
			framework.AttrDeletionProtection: framework.DeletionProtectionAttribute(),
			framework.AttrForceDestroy:       forceDestroyAttribute(),
			framework.AttrTimeouts:           framework.TimeoutsAttribute(ctx),
			"subnet_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "ID of the IPAM subnet to enable DHCP on, usually `omniddi_ipam_subnet.<name>.id`. It must be a `SUBNET` node. " +
					"Changing it forces a new resource.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"server_group_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "ID of the DHCP server group that serves the subnet. The group needs an active primary DHCP server with an agent mapped to it. " +
					"OmniDDI cannot move a DHCP subnet to another group, so changing it forces a new resource (demote, then promote).",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"cidr": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The subnet in CIDR notation.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"pools": poolsAttribute(),
			"option_data": dhcpkit.OptionsAttribute("Subnet-level DHCP options. Routers, DNS servers and the domain name go in here as options 3, 6 and 15. " +
				"OmniDDI fills in the option name and type from the option code, so `name` and `data_type` are optional. " +
				"Removing the argument removes the options from the subnet."),
			"lease_time": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Lease time, in the unit given by `lease_time_unit` (seconds when no unit is set). OmniDDI cannot clear a lease time, " +
					"so removing the argument keeps the current value.",
				Validators:    []validator.Int64{int64validator.AtLeast(0)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"lease_time_unit": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Unit of `lease_time`: " + codes(leaseTimeUnits) + ". Removing the argument keeps the current value.",
				Validators:          []validator.String{stringvalidator.OneOf(leaseTimeUnits...)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enable_dhcp_service": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether the DHCP service is enabled for the subnet. Null until it has been set.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"enable_ddns": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether DHCP clients on the subnet get dynamic DNS updates. Null until it has been set.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"ddns_config_json": schema.StringAttribute{
				Optional:  true,
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "Dynamic DNS settings as a JSON object, for example `jsonencode({ ddns_ns_server = \"10.0.0.2\", secure_updates = true, tsig_key_id = 3 })`. " +
					"Reference a TSIG key by `tsig_key_id`: OmniDDI strips inline key material when it stores the configuration. Removing the argument keeps the current value.",
				Validators:    []validator.String{jsonObject{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vlan_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID of the OmniDDI DHCP VLAN object mapped to the subnet. This is not the VLAN label of the IPAM subnet. " +
					"OmniDDI cannot clear the mapping through the API, so removing the argument keeps the current value.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"view_id": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "ID of the DNS view associated with the subnet, if any. Read only: OmniDDI cannot set it on an existing IPAM subnet, " +
					"so associate a view in the OmniDDI console.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"client_class_ids": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.Int64Type,
				MarkdownDescription: "IDs of client classes, configured in OmniDDI, that are mapped to the subnet. Removing the argument removes the mappings.",
			},
			"kea_subnet_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The id the DHCP server serves the subnet under. It equals `id`.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"shared_network_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "ID of the shared network, configured in OmniDDI, that the subnet belongs to. Null when it is in none.",
			},
			"created_on": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp of the DHCP configuration.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_on": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp of the DHCP configuration.",
			},
		},
	}
}

func idAttribute() schema.Int64Attribute {
	attr := framework.IDAttribute()
	attr.MarkdownDescription = "The OmniDDI network id of the subnet (`networks.id`). It equals `subnet_id`."
	return attr
}

func forceDestroyAttribute() schema.BoolAttribute {
	attr := framework.ForceDestroyAttribute()
	attr.MarkdownDescription = "Allow demoting a subnet that still has host reservations or active leases. Destroying the resource only stops serving DHCP: " +
		"the reservations stay on the IPAM subnet and the leases remain until they expire, but clients lose service. Defaults to `false`."
	return attr
}

func poolsAttribute() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional: true,
		MarkdownDescription: "Address pools served on the subnet. Removing the argument removes all pools. " +
			"Each pool must lie inside the subnet and must not overlap another pool.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"pool": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Address range as `first-last`, for example `10.20.0.100-10.20.0.200`.",
				Validators:          []validator.String{poolRange{}},
			},
			"name": schema.StringAttribute{Optional: true, MarkdownDescription: "Pool name."},
			"client_class_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "ID of a client class that restricts the pool to matching clients.",
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"option_data": dhcpkit.OptionsAttribute("DHCP options sent only to clients served from this pool."),
		}},
	}
}

func codes(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, "`"+v+"`")
	}
	return strings.Join(quoted, ", ")
}

type poolRange struct{}

func (poolRange) Description(context.Context) string {
	return "value must be an address range written as first-last"
}

func (v poolRange) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (poolRange) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := checkPoolRange(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid pool range", err.Error())
	}
}

func checkPoolRange(value string) error {
	first, last, found := strings.Cut(value, "-")
	if !found {
		return fmt.Errorf("%q is not a range; write it as first-last", value)
	}
	start, err := netip.ParseAddr(strings.TrimSpace(first))
	if err != nil {
		return fmt.Errorf("%q is not an IP address", strings.TrimSpace(first))
	}
	end, err := netip.ParseAddr(strings.TrimSpace(last))
	if err != nil {
		return fmt.Errorf("%q is not an IP address", strings.TrimSpace(last))
	}
	if start.Is4() != end.Is4() {
		return fmt.Errorf("the range mixes IPv4 and IPv6 addresses")
	}
	if end.Less(start) {
		return fmt.Errorf("the range ends (%s) before it starts (%s)", end, start)
	}
	return nil
}

type jsonObject struct{}

func (jsonObject) Description(context.Context) string { return "value must be a JSON object" }

func (v jsonObject) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (jsonObject) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &decoded); err != nil || decoded == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON object", "The value must be a JSON object, for example produced by jsonencode({ ... }).")
	}
}
