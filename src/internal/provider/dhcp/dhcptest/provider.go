package dhcptest

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
)

type testProvider struct {
	*omniprovider.OmniDDIProvider
	resources   []func() resource.Resource
	dataSources []func() datasource.DataSource
}

func (p testProvider) Resources(context.Context) []func() resource.Resource { return p.resources }

func (p testProvider) DataSources(context.Context) []func() datasource.DataSource {
	return p.dataSources
}

func fastPolling(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func Factories(resources []func() resource.Resource, dataSources []func() datasource.DataSource) map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastPolling)().(*omniprovider.OmniDDIProvider)
	return acctest.ProviderFactoriesFor(testProvider{OmniDDIProvider: base, resources: resources, dataSources: dataSources})
}

func NewServer(t *testing.T) *fakeserver.Server {
	t.Helper()
	srv := fakeserver.New(t)
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})
	return srv
}

func Config(srv *fakeserver.Server, hcl string) string {
	return acctest.ProviderConfigWith(srv.URL(), "  default_tags = { \"env\" = \"prod\" }\n") + "\n" + hcl + "\n"
}

func Case(t *testing.T, factories map[string]func() (tfprotov6.ProviderServer, error), steps ...tfresource.TestStep) {
	t.Helper()
	t.Parallel()
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: factories,
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}
