package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/record"
)

type recordProvider struct {
	*omniprovider.OmniDDIProvider
}

func (recordProvider) Resources(context.Context) []func() fwresource.Resource {
	return record.Resources()
}

func (recordProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }

func fastTuning(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func recordFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastTuning)().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = recordProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

func recordCase(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: recordFactories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}
