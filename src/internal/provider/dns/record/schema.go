package record

import (
	"context"

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
)

const (
	maxTTL        = 2147483647
	maxUint16     = 65535
	orderingDesc  = "`default`, `random` or `cyclic`"
	typeListLabel = "A, AAAA, ANAME, AFSDB, CAA, CNAME, DNAME, DNSKEY, DS, EUI48, EUI64, HINFO, HTTPS, ISDN, LOC, MB, MD, MF, MG, MR, MX, NAPTR, NS, PTR, PX, RP, RRSIG, RT, SOA, SPF, SRV, SSHFP, SVCB, TLSA, TXT, URI, X25"
)

var recordSetOrderings = []string{"default", "random", "cyclic"}

func (r *recordResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		framework.AttrID:           idAttribute(),
		framework.AttrApprovalMode: framework.ApprovalModeAttribute(),
		framework.AttrTimeouts:     framework.TimeoutsAttribute(ctx),
		"zone_id": schema.Int64Attribute{
			Required:            true,
			MarkdownDescription: "ID of the zone the record belongs to (`omniddi_dns_zone.id`). Changing it replaces the record.",
			PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		},
		"name": schema.StringAttribute{
			Required:   true,
			CustomType: framework.HostnameType{},
			MarkdownDescription: "Owner name relative to the zone, for example `www`. Use `@` for the zone apex. " +
				"Names are lower-cased by OmniDDI and compared case-insensitively. A trailing dot is significant: " +
				"`www.` is an absolute name, `www` is relative to the zone.",
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"type": schema.StringAttribute{
			Required: true,
			MarkdownDescription: "Record type, in upper case. One of: " + typeListLabel + ". " +
				"The value form depends on the type: `values` for string-shaped types (A, AAAA, NS, PTR, TXT, SPF, CNAME, DNAME, " +
				"MB, MD, MF, MG, MR, SOA, DNSKEY, RRSIG, LOC, X25, EUI48, EUI64), the `mx`, `srv` or `caa` blocks for MX, SRV and CAA, " +
				"and `data_json` for every other type (and optionally for MX, SRV and CAA). Changing it replaces the record.",
			Validators:    []validator.String{stringvalidator.OneOf(supportedTypes...)},
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"ttl": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "Record TTL in seconds. When omitted at creation OmniDDI stores the zone TTL. " +
				"Removing the attribute later keeps the stored value; set it explicitly to change it.",
			Validators:    []validator.Int64{int64validator.Between(1, maxTTL)},
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		},
		"values": schema.ListAttribute{
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Values for string-shaped record types: A, AAAA, NS, PTR, TXT and SPF accept one or more values; " +
				"CNAME, DNAME, MB, MD, MF, MG, MR, SOA, DNSKEY, RRSIG, LOC, X25, EUI48 and EUI64 take exactly one. " +
				"Each TXT/SPF element is one value of the record set. Exactly one of `values`, `mx`, `srv`, `caa` or `data_json` must be set, and it must match `type`.",
		},
		"disabled_values": schema.SetAttribute{
			Optional:    true,
			ElementType: types.StringType,
			MarkdownDescription: "Subset of `values` that stay in OmniDDI but are withheld from DNS answers. " +
				"Only supported for A, AAAA, NS, PTR, TXT and SPF, and at least one value must remain enabled.",
		},
		"data_json": schema.StringAttribute{
			Optional:   true,
			CustomType: normalizedJSONType{},
			MarkdownDescription: "Escape hatch: the record data as a JSON document, sent to OmniDDI as the record `data` payload. " +
				"Required for AFSDB, ANAME, DS, HINFO, HTTPS, ISDN, NAPTR, PX, RP, RT, SSHFP, SVCB, TLSA and URI; optional for MX, SRV and CAA. " +
				"Use `jsonencode`. Key order and whitespace are ignored when comparing. Object-shaped payloads (for example SVCB) are merged " +
				"into the stored object by OmniDDI on update, so a removed key is not deleted. Numeric TLSA/SSHFP/DS fields are stored as integers.",
		},
		"record_set_ordering": schema.StringAttribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "Answer ordering for multi-value record sets: " + orderingDesc + ". " +
				"OmniDDI only persists it for records with a load-balancing health check, which this provider does not manage yet, " +
				"so the configured value is kept in state without being read back.",
			Validators:    []validator.String{stringvalidator.OneOf(recordSetOrderings...)},
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"view_id":         computedInt("ID of the DNS view that contains the zone."),
		"zone_name":       computedString("Name of the zone the record belongs to."),
		"effective_ttl":   schema.Int64Attribute{Computed: true, MarkdownDescription: "TTL in effect: the record TTL, or the zone TTL when the record has none."},
		"is_auto_created": computedBool("True for records created automatically during zone synchronisation. OmniDDI refuses to delete these and to toggle their values."),
		"created_on":      computedString("Creation timestamp."),
		"updated_on":      schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp."},
	}
	for name, attr := range framework.TagsAttributes() {
		attrs[name] = attr
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DNS record set (one owner name and type) in an OmniDDI zone. " +
			"One resource covers every record type OmniDDI accepts; pick the value form that matches `type`.",
		Attributes: attrs,
		Blocks:     typedBlocks(),
	}
}

func typedBlocks() map[string]schema.Block {
	return map[string]schema.Block{
		"mx": schema.ListNestedBlock{
			MarkdownDescription: "MX exchanges. Repeat the block for several exchanges. Only for `type = \"MX\"`.",
			NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
				"preference": uint16Attribute("Preference (0-65535); lower is preferred."),
				"exchange": schema.StringAttribute{
					Required: true, CustomType: framework.FQDNType{},
					MarkdownDescription: "Mail exchanger host name.",
					Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				},
			}},
		},
		"srv": schema.ListNestedBlock{
			MarkdownDescription: "SRV targets. Repeat the block for several targets. Only for `type = \"SRV\"`.",
			NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
				"priority": uint16Attribute("Priority (0-65535); lower is preferred."),
				"weight":   uint16Attribute("Relative weight (0-65535) among targets of equal priority."),
				"port":     uint16Attribute("TCP/UDP port (0-65535)."),
				"host": schema.StringAttribute{
					Required: true, CustomType: framework.FQDNType{},
					MarkdownDescription: "Target host name.",
					Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				},
			}},
		},
		"caa": schema.ListNestedBlock{
			MarkdownDescription: "CAA entries. A list because one name commonly carries both `issue` and `iodef`. Only for `type = \"CAA\"`.",
			NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
				"flag": schema.Int64Attribute{
					Required: true, MarkdownDescription: "Issuer critical flag: `0` or `128`.",
					Validators: []validator.Int64{int64validator.OneOf(0, 128)},
				},
				"tag": schema.StringAttribute{
					Required: true, MarkdownDescription: "Property tag: `issue`, `issuewild` or `iodef`.",
					Validators: []validator.String{stringvalidator.OneOf("issue", "issuewild", "iodef")},
				},
				"value": schema.StringAttribute{
					Required: true, MarkdownDescription: "Property value, for example `letsencrypt.org`.",
					Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				},
			}},
		},
	}
}

func idAttribute() schema.Int64Attribute {
	attr := framework.IDAttribute()
	attr.MarkdownDescription = "The OmniDDI record ID."
	return attr
}

func uint16Attribute(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Required:            true,
		MarkdownDescription: description,
		Validators:          []validator.Int64{int64validator.Between(0, maxUint16)},
	}
}

func computedInt(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Computed:            true,
		MarkdownDescription: description,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: description,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func computedBool(description string) schema.BoolAttribute {
	return schema.BoolAttribute{
		Computed:            true,
		MarkdownDescription: description,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}
