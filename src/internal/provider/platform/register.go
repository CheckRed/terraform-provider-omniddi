package platform

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/platform/networkspace"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/platform/servergroup"
)

func Resources() []func() resource.Resource { return []func() resource.Resource{} }

func DataSources() []func() datasource.DataSource {
	var out []func() datasource.DataSource
	out = append(out, servergroup.DataSources()...)
	out = append(out, networkspace.DataSources()...)
	return out
}

func ListResources() []func() list.ListResource { return []func() list.ListResource{} }
