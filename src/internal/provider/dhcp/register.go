package dhcp

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/reservation"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/subnet"
)

func Resources() []func() resource.Resource {
	var out []func() resource.Resource
	out = append(out, subnet.Resources()...)
	out = append(out, reservation.Resources()...)
	return out
}

func DataSources() []func() datasource.DataSource { return []func() datasource.DataSource{} }

func ListResources() []func() list.ListResource { return []func() list.ListResource{} }
