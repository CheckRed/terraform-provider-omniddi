package dns

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/record"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/view"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/zone"
)

func Resources() []func() resource.Resource {
	return concat(zone.Resources(), record.Resources())
}

func DataSources() []func() datasource.DataSource {
	return concat(view.DataSources(), zone.DataSources())
}

func ListResources() []func() list.ListResource { return []func() list.ListResource{} }

func concat[T any](groups ...[]T) []T {
	var out []T
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}
