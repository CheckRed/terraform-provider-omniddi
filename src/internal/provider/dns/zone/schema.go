package zone

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func soaSeconds(description string, minimum int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		Optional:            true,
		Computed:            true,
		MarkdownDescription: description,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		Validators:          []validator.Int64{int64validator.Between(minimum, 2147483647)},
	}
}

func idReference(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Optional:            true,
		MarkdownDescription: description,
		Validators:          []validator.Int64{int64validator.AtLeast(1)},
	}
}

func featureFlag(description string) schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional:            true,
		Computed:            true,
		MarkdownDescription: description,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}

func (r *zoneResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:                 framework.IDAttribute(),
		framework.AttrApprovalMode:       framework.ApprovalModeAttribute(),
		framework.AttrDeletionProtection: framework.DeletionProtectionAttribute(),
		framework.AttrForceDestroy:       zoneForceDestroy(),
		framework.AttrTimeouts:           framework.TimeoutsAttribute(ctx),
		"view_id": schema.Int64Attribute{
			Required:            true,
			MarkdownDescription: "Id of the view that holds the zone, for example `data.omniddi_dns_view.<name>.id`. Changing it replaces the zone.",
			PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			Validators:          []validator.Int64{int64validator.AtLeast(1)},
		},
		"name": schema.StringAttribute{
			Optional:   true,
			Computed:   true,
			CustomType: framework.FQDNType{},
			MarkdownDescription: "Zone name, for example `corp.example.com`. Compared case-insensitively and without a trailing dot. " +
				"Optional only for a `Reverse` zone with `protocol` and `cidr`, whose name is then derived. Changing it replaces the zone.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), replaceUnlessSameName()},
			Validators:    []validator.String{zoneName()},
		},
		"zone_type": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(typePrimary),
			MarkdownDescription: "Zone type: `Primary` (default), `Secondary`, `Forward` or `Reverse`. Changing it replaces the zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{stringvalidator.OneOf(zoneTypes...)},
		},
		"network_scope": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("Internal"),
			MarkdownDescription: "Network scope: `Internal` (default) or `External`. Changing it replaces the zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{stringvalidator.OneOf(networkScopes...)},
		},
		"protocol": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "IP protocol of a `Reverse` zone: `IPv4` or `IPv6`. Required with `cidr`. Changing it replaces the zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{stringvalidator.OneOf("IPv4", "IPv6")},
		},
		"cidr": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Network of a `Reverse` zone, for example `192.0.2.0/24` or `2001:db8::/32`. IPv4 prefixes must be a multiple of 8 " +
				"from /8 to /32; IPv6 prefixes a multiple of 4. Changing it replaces the zone.",
			PlanModifiers: []planmodifier.String{replaceUnlessSameCIDR()},
			Validators:    []validator.String{cidrBlock()},
		},
		"ttl":     soaSeconds("Default TTL in seconds.", 1),
		"refresh": soaSeconds("SOA refresh interval in seconds; at least 300.", 300),
		"retry":   soaSeconds("SOA retry interval in seconds.", 1),
		"expiry":  soaSeconds("SOA expire time in seconds.", 1),
		"minimum": soaSeconds("SOA minimum (negative caching) TTL in seconds.", 1),
		"email": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "SOA contact address of a `Primary` or `Reverse` zone, in the form `hostmaster@example.com`.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"primary_name_server": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "IP address of the SOA primary name server of a `Primary` or `Reverse` zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:          []validator.String{ipAddress()},
		},
		"primary_servers": schema.ListNestedAttribute{
			Optional: true,
			MarkdownDescription: "Primary servers a `Secondary` zone transfers from; at least one is required for that zone type. " +
				"A server with `tsig_key_id` is contacted with a TSIG-secured transfer.",
			Validators: []validator.List{listvalidator.SizeAtLeast(1)},
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"ip": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "Primary server IP address.",
					Validators:          []validator.String{ipAddress()},
				},
				"port": schema.Int64Attribute{
					Optional:            true,
					Computed:            true,
					Default:             int64default.StaticInt64(defaultDNSPort),
					MarkdownDescription: "Primary server port. Defaults to `53`.",
					Validators:          []validator.Int64{int64validator.Between(1, 65535)},
				},
				"tsig_key_id": idReference("Id of the TSIG key, configured in OmniDDI, that secures the transfer from this server."),
			}},
		},
		"nameservers": schema.ListAttribute{
			Optional:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "IP addresses of the servers a `Forward` zone forwards to, on port 53; at least one is required for that zone type.",
			Validators:          []validator.List{listvalidator.SizeAtLeast(1), listvalidator.ValueStringsAre(ipAddress())},
		},
		"policy": schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Forward policy of a `Forward` zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"enable_dynamic_dns":      featureFlag("Enable dynamic DNS updates. Requires an allow-update ACL on the view, a zone ACL or a TSIG key."),
		"enable_dnssec":           featureFlag("Enable DNSSEC signing. Not possible while the zone holds ANAME records."),
		"auto_create_ptr_records": featureFlag("Create PTR records automatically for A and AAAA records."),
		"enable_zone_transfer":    featureFlag("Allow zone transfers."),
		"secure_sync":             featureFlag("Use TSIG-secured transfers for a `Secondary` zone."),
		"zone_acl_id": idReference("Id of the ACL, configured in OmniDDI, applied to the zone. The API does not return it, so the " +
			"configured value is kept in state and console changes are not detected."),
		"zone_transfer_tsig_key_id": idReference("Id of the TSIG key, configured in OmniDDI, used for zone transfers."),
		"dynamic_dns_tsig_key_id":   idReference("Id of the TSIG key, configured in OmniDDI, used for dynamic DNS."),
		"secondary_sync_tsig_key_id": schema.Int64Attribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Id of the TSIG key, configured in OmniDDI, used by a `Secondary` zone to sync. Set from the `tsig_key_id` of `primary_servers` when not configured.",
			PlanModifiers:       []planmodifier.Int64{useStateUnlessPrimaryServersChange()},
			Validators:          []validator.Int64{int64validator.AtLeast(1)},
		},
		"business_unit_ids": schema.ListAttribute{
			Optional:    true,
			Computed:    true,
			ElementType: types.Int64Type,
			MarkdownDescription: "Ids of the business units the zone belongs to; the first is the primary one. " +
				"Defaults to the primary business unit of the calling identity.",
			PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			Validators:    []validator.List{listvalidator.SizeAtLeast(1), listvalidator.ValueInt64sAre(int64validator.AtLeast(1))},
		},
		"soa_serial": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Current SOA serial reported by OmniDDI.",
		},
		"records_count": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Number of records in the zone.",
		},
		"view_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Name of the view that holds the zone.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"created_on": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Creation timestamp reported by OmniDDI.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"updated_on": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Last update timestamp reported by OmniDDI.",
		},
	}
	for name, attribute := range framework.TagsAttributes() {
		attrs[name] = attribute
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DNS zone inside a view. Deleting a zone in OmniDDI also deletes every record in it, so " +
			"`deletion_protection` defaults to `true` and `force_destroy` is required to delete a zone that still holds records " +
			"you created. The zone `description` is not supported: the OmniDDI API has no field for it yet.",
		Attributes: attrs,
	}
}

func replaceUnlessSameName() planmodifier.String {
	const description = "Replaces the zone when the name changes; a different spelling of the same name is not a change."
	return stringplanmodifier.RequiresReplaceIf(func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = framework.NormalizeFQDN(req.StateValue.ValueString()) != framework.NormalizeFQDN(req.PlanValue.ValueString())
	}, description, description)
}

func replaceUnlessSameCIDR() planmodifier.String {
	const description = "Replaces the zone when the network changes; a different spelling of the same network is not a change."
	return stringplanmodifier.RequiresReplaceIf(func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = req.StateValue.ValueString() != req.PlanValue.ValueString() && !sameCIDR(req.StateValue.ValueString(), req.PlanValue.ValueString())
	}, description, description)
}

type syncKeyModifier struct{}

func useStateUnlessPrimaryServersChange() planmodifier.Int64 { return syncKeyModifier{} }

func (syncKeyModifier) Description(context.Context) string {
	return "Keeps the prior value unless primary_servers change, because OmniDDI derives this key from them."
}

func (m syncKeyModifier) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (syncKeyModifier) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if !req.PlanValue.IsUnknown() || req.State.Raw.IsNull() {
		return
	}
	var planned, prior types.List
	diags := req.Plan.GetAttribute(ctx, path.Root("primary_servers"), &planned)
	diags.Append(req.State.GetAttribute(ctx, path.Root("primary_servers"), &prior)...)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() && planned.Equal(prior) {
		resp.PlanValue = req.StateValue
	}
}

func zoneForceDestroy() schema.BoolAttribute {
	attribute := framework.ForceDestroyAttribute()
	return schema.BoolAttribute{
		Optional: true,
		Computed: true,
		Default:  attribute.Default,
		MarkdownDescription: "Allow deleting the zone while it still holds records that were not created automatically; OmniDDI " +
			"deletes them together with the zone. Defaults to `false`.",
	}
}
