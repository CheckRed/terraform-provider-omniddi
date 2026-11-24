package zone

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func Resources() []func() resource.Resource {
	return []func() resource.Resource{NewResource}
}

func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewDataSource, NewListDataSource}
}
