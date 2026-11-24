package view

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type forwarderModel struct {
	IP   types.String `tfsdk:"ip"`
	Port types.Int64  `tfsdk:"port"`
}

var (
	forwarderAttrTypes = map[string]attr.Type{"ip": types.StringType, "port": types.Int64Type}
	forwarderType      = types.ObjectType{AttrTypes: forwarderAttrTypes}
)
