package ipamtest

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

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

func Factories(resources []func() resource.Resource, dataSources []func() datasource.DataSource) map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.New("test")().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = testProvider{OmniDDIProvider: base, resources: resources, dataSources: dataSources}
	return acctest.ProviderFactoriesFor(p)
}

func (b *Backend) Client(t testing.TB) *client.Client {
	t.Helper()
	c, err := client.New(context.Background(), client.Config{
		Endpoint:       b.Srv.URL(),
		Username:       fakeserver.DefaultUsername,
		Password:       fakeserver.DefaultPassword,
		RequestTimeout: 5 * time.Second,
		RetryBackoff:   client.Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

func (b *Backend) ApplyTag(t testing.TB, objectType string, id int64, name string, values ...string) {
	t.Helper()
	if err := b.Client(t).ApplyTags(context.Background(), objectType, id, map[string][]string{name: values}); err != nil {
		t.Fatalf("apply tag %s: %v", name, err)
	}
}
