package ipam

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/address"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/container"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/node"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/subnet"
)

func Resources() []func() resource.Resource {
	var out []func() resource.Resource
	out = append(out, address.Resources()...)
	out = append(out, container.Resources()...)
	out = append(out, subnet.Resources()...)
	return out
}

func DataSources() []func() datasource.DataSource {
	var out []func() datasource.DataSource
	out = append(out, container.DataSources()...)
	out = append(out, subnet.DataSources()...)
	out = append(out, node.DataSources()...)
	return out
}

func ListResources() []func() list.ListResource { return []func() list.ListResource{} }
