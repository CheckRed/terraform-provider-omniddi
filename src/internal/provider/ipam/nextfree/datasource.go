package nextfree

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

const advisoryNote = "The answer is advisory: nothing is reserved, and it changes as soon as the space is used. " +
	"Use it for capacity checks in `check` blocks or for outputs. Do not pass it to a resource argument: " +
	"the next plan reads a different answer and Terraform would replace the resource."

func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewIPDataSource, NewSubnetDataSource}
}

type base struct {
	pd *framework.ProviderData
}

func (b *base) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

type stringer interface{ ValueString() string }

func Strings(set types.Set) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	out := make([]string, 0, len(set.Elements()))
	for _, e := range set.Elements() {
		if s, ok := e.(stringer); ok {
			out = append(out, s.ValueString())
		}
	}
	return out
}

func int64OrNull(v int64) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(v)
}
