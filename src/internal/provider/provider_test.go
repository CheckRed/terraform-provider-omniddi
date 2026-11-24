package provider_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
)

type probeModel struct {
	NetworkSpaceID types.Int64  `tfsdk:"network_space_id"`
	BackendVersion types.String `tfsdk:"backend_version"`
	ApprovalMode   types.String `tfsdk:"approval_mode"`
	TimeoutSeconds types.Int64  `tfsdk:"timeout_seconds"`
	DefaultTags    types.Map    `tfsdk:"default_tags"`
}

type probeDataSource struct{ pd *framework.ProviderData }

func newProbe() datasource.DataSource { return &probeDataSource{} }

func (d *probeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_probe"
}

func (d *probeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"network_space_id": schema.Int64Attribute{Computed: true},
		"backend_version":  schema.StringAttribute{Computed: true},
		"approval_mode":    schema.StringAttribute{Computed: true},
		"timeout_seconds":  schema.Int64Attribute{Computed: true},
		"default_tags":     schema.MapAttribute{Computed: true, ElementType: types.StringType},
	}}
}

func (d *probeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = framework.ConfigureProviderData(req.ProviderData, &resp.Diagnostics)
}

const probePath = "/probe/"

func handleProbe(srv *fakeserver.Server) {
	srv.HandleREST("GET", "/api/v1"+probePath, func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(http.StatusOK, map[string]any{})
	})
}

func probeRequests(t *testing.T, srv *fakeserver.Server) []fakeserver.Request {
	t.Helper()
	reqs := srv.RequestsFor("GET", "/api/v1"+probePath)
	if len(reqs) == 0 {
		t.Fatal("the probe made no authenticated call")
	}
	return reqs
}

func (d *probeDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if err := d.pd.Client.Authenticate(ctx); err != nil {
		framework.AddClientError(&resp.Diagnostics, "authentication failed", err)
		return
	}
	health, err := d.pd.Client.Health(ctx)
	if err != nil {
		framework.AddClientError(&resp.Diagnostics, "health check failed", err)
		return
	}
	if err := d.pd.Client.Get(ctx, probePath, nil, nil); err != nil {
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			framework.AddClientError(&resp.Diagnostics, "authenticated call failed", err)
			return
		}
	}
	tags, diags := types.MapValueFrom(ctx, types.StringType, d.pd.DefaultTags)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &probeModel{
		NetworkSpaceID: types.Int64Value(d.pd.Client.NetworkSpaceID()),
		BackendVersion: types.StringValue(health.Version),
		ApprovalMode:   types.StringValue(string(d.pd.ApprovalMode)),
		TimeoutSeconds: types.Int64Value(int64(d.pd.DefaultTimeout.Seconds())),
		DefaultTags:    tags,
	})...)
}

type probeProvider struct{ *omniprovider.OmniDDIProvider }

func (probeProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newProbe}
}

func probeFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.New("test")().(*omniprovider.OmniDDIProvider)
	var p fwprovider.Provider = probeProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

const probeConfig = `
data "omniddi_probe" "p" {}
`

func providerCase(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: probeFactories(), Steps: steps})
}

func clearOmniEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		omniprovider.EnvEndpoint, omniprovider.EnvUsername, omniprovider.EnvPassword,
		omniprovider.EnvNetworkSpaceID, omniprovider.EnvCACertPEM, omniprovider.EnvClientCertPEM, omniprovider.EnvClientKeyPEM,
		omniprovider.EnvProxyURL, omniprovider.EnvInsecure, omniprovider.EnvTimeout, omniprovider.EnvApprovalMode,
		omniprovider.EnvMaxConcurrency, omniprovider.EnvMaxRetries,
	} {
		t.Setenv(name, "")
	}
}

func TestProviderConfiguresFromAttributes(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	cfg := acctest.ProviderConfigWith(srv.URL(), `  approval_mode = "wait"
  default_tags  = { Managed-By = "terraform" }
  max_retries   = 2
  max_concurrency = 4
`) + probeConfig

	providerCase(t, resource.TestStep{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.omniddi_probe.p", "network_space_id", "1"),
		resource.TestCheckResourceAttr("data.omniddi_probe.p", "backend_version", fakeserver.DefaultVersion),
		resource.TestCheckResourceAttr("data.omniddi_probe.p", "approval_mode", "wait"),
		resource.TestCheckResourceAttr("data.omniddi_probe.p", "timeout_seconds", "30"),
		resource.TestCheckResourceAttr("data.omniddi_probe.p", "default_tags.Managed-By", "terraform"),
	)})
}

func TestProviderConfiguresFromEnvironment(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	t.Setenv(omniprovider.EnvEndpoint, srv.URL())
	t.Setenv(omniprovider.EnvUsername, fakeserver.DefaultUsername)
	t.Setenv(omniprovider.EnvPassword, fakeserver.DefaultPassword)
	t.Setenv(omniprovider.EnvApprovalMode, "wait")
	t.Setenv(omniprovider.EnvTimeout, "90s")

	providerCase(t, resource.TestStep{
		Config: `provider "omniddi" {}` + probeConfig,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "approval_mode", "wait"),
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "timeout_seconds", "90"),
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "default_tags.%", "0"),
		),
	})
}

func TestProviderAttributeBeatsEnvironment(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	t.Setenv(omniprovider.EnvEndpoint, "http://127.0.0.1:1")
	t.Setenv(omniprovider.EnvApprovalMode, "wait")
	t.Setenv(omniprovider.EnvTimeout, "5s")
	t.Setenv(omniprovider.EnvUsername, "someone-else@example.com")
	t.Setenv(omniprovider.EnvPassword, "wrong-synthetic-password")
	handleProbe(srv)

	providerCase(t, resource.TestStep{
		Config: acctest.ProviderConfigWith(srv.URL(), `  approval_mode = "fail"
`) + probeConfig,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "approval_mode", "fail"),
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "timeout_seconds", "30"),
			resource.TestCheckResourceAttr("data.omniddi_probe.p", "network_space_id", "1"),
		),
	})
	for _, r := range probeRequests(t, srv) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("the configured username and password must win over the environment")
		}
	}
}

func TestProviderCredentialsAndNetworkSpaceFromEnvironment(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	t.Setenv(omniprovider.EnvEndpoint, srv.URL())
	t.Setenv(omniprovider.EnvUsername, fakeserver.DefaultUsername)
	t.Setenv(omniprovider.EnvPassword, fakeserver.DefaultPassword)
	t.Setenv(omniprovider.EnvNetworkSpaceID, "2")
	handleProbe(srv)

	providerCase(t, resource.TestStep{
		Config: `provider "omniddi" {}` + probeConfig,
		Check:  resource.TestCheckResourceAttr("data.omniddi_probe.p", "network_space_id", "2"),
	})
	for _, r := range probeRequests(t, srv) {
		if got := srv.RequestSpace(r); got != 2 {
			t.Errorf("probe authenticated in space %d, want 2", got)
		}
	}
}

func TestProviderConfigurationErrors(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	url := srv.URL()
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{"missing endpoint", `provider "omniddi" {
  username = "user@example.com"
  password = "synthetic-password"
}`, `An endpoint is required`},
		{"missing credentials", fmt.Sprintf(`provider "omniddi" { endpoint = %q }`, url), `A username is required`},
		{"missing password", fmt.Sprintf(`provider "omniddi" {
  endpoint = %q
  username = "user@example.com"
}`, url), `A password is required`},
		{"cert without key", fmt.Sprintf(`provider "omniddi" {
  endpoint        = %q
  username = "user@example.com"
  password = "synthetic-password"
  client_cert_pem = "x"
}`, url), `(?s)(Invalid Attribute Combination|client_key_pem)`},
		{"bad approval mode", fmt.Sprintf(`provider "omniddi" {
  endpoint      = %q
  username = "user@example.com"
  password = "synthetic-password"
  approval_mode = "accept"
}`, url), `approval_mode value must be one of`},
		{"bad timeout", fmt.Sprintf(`provider "omniddi" {
  endpoint = %q
  username = "user@example.com"
  password = "synthetic-password"
  timeout  = "soon"
}`, url), `not a valid duration`},
		{"zero timeout", fmt.Sprintf(`provider "omniddi" {
  endpoint = %q
  username = "user@example.com"
  password = "synthetic-password"
  timeout  = "0s"
}`, url), `greater than zero`},
		{"concurrency out of range", fmt.Sprintf(`provider "omniddi" {
  endpoint        = %q
  username = "user@example.com"
  password = "synthetic-password"
  max_concurrency = 500
}`, url), `(?s)(max_concurrency|between)`},
		{"retries out of range", fmt.Sprintf(`provider "omniddi" {
  endpoint    = %q
  username = "user@example.com"
  password = "synthetic-password"
  max_retries = 50
}`, url), `(?s)(max_retries|between)`},
		{"space id zero", fmt.Sprintf(`provider "omniddi" {
  endpoint         = %q
  username = "user@example.com"
  password = "synthetic-password"
  network_space_id = 0
}`, url), `(?s)(network_space_id|at least)`},
		{"endpoint not a url", `provider "omniddi" {
  endpoint = "omniddi.example.com"
  username = "user@example.com"
  password = "synthetic-password"
}`, `absolute http or https URL`},
		{"empty default tag name", fmt.Sprintf(`provider "omniddi" {
  endpoint     = %q
  username = "user@example.com"
  password = "synthetic-password"
  default_tags = { "" = "x" }
}`, url), `non-empty tag names`},
		{"unknown value", `resource "terraform_data" "e" { input = "x" }
provider "omniddi" {
  endpoint = "https://${terraform_data.e.output}.example.com"
  username = "user@example.com"
  password = "synthetic-password"
}`, `Unknown provider configuration value`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			providerCase(t, resource.TestStep{Config: tc.config + probeConfig, ExpectError: regexp.MustCompile(tc.want)})
		})
	}
}

func TestProviderEnvironmentErrors(t *testing.T) {
	for name, tc := range map[string]struct{ env, value, want string }{
		"bad space id":    {omniprovider.EnvNetworkSpaceID, "abc", `network_space_id must be a positive integer`},
		"bad concurrency": {omniprovider.EnvMaxConcurrency, "0", `max_concurrency must be between 1 and 64`},
		"bad retries":     {omniprovider.EnvMaxRetries, "x", `max_retries must be between 0 and 10`},
		"bad approval":    {omniprovider.EnvApprovalMode, "later", `approval_mode must be`},
	} {
		t.Run(name, func(t *testing.T) {
			clearOmniEnv(t)
			srv := fakeserver.New(t)
			t.Setenv(tc.env, tc.value)
			providerCase(t, resource.TestStep{
				Config:      acctest.ProviderConfig(srv.URL()) + probeConfig,
				ExpectError: regexp.MustCompile(tc.want),
			})
		})
	}
}

func TestConfigureWarnsWhenInsecureAndPublishesProviderData(t *testing.T) {
	clearOmniEnv(t)
	ctx := context.Background()
	p := omniprovider.New("1.2.3")()

	var schemaResp fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &schemaResp)
	objType, _ := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	values["endpoint"] = tftypes.NewValue(tftypes.String, "https://omniddi.example.com")
	values["username"] = tftypes.NewValue(tftypes.String, fakeserver.DefaultUsername)
	values["password"] = tftypes.NewValue(tftypes.String, fakeserver.DefaultPassword)
	values["insecure"] = tftypes.NewValue(tftypes.Bool, true)
	values["timeout"] = tftypes.NewValue(tftypes.String, "2m")

	var resp fwprovider.ConfigureResponse
	p.Configure(ctx, fwprovider.ConfigureRequest{
		TerraformVersion: "1.17.1",
		Config:           tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, values)},
	}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("diags = %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("expected exactly one insecure warning, got %v", resp.Diagnostics)
	}
	pd, ok := resp.ResourceData.(*framework.ProviderData)
	if !ok || pd.DefaultTimeout.Minutes() != 2 || pd.ApprovalMode != framework.ApprovalModeFail || pd.Client == nil {
		t.Fatalf("ResourceData = %#v", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData || resp.ListResourceData != resp.ResourceData {
		t.Errorf("all data kinds must share the provider data")
	}
}

func TestProviderMetadataAndRegistration(t *testing.T) {
	ctx := context.Background()
	p := omniprovider.New("9.9.9")()

	var meta fwprovider.MetadataResponse
	p.Metadata(ctx, fwprovider.MetadataRequest{}, &meta)
	if meta.TypeName != "omniddi" || meta.Version != "9.9.9" {
		t.Errorf("metadata = %+v", meta)
	}
	seen := map[string]bool{}
	checkName := func(kind, name string) {
		if !strings.HasPrefix(name, "omniddi_") {
			t.Errorf("%s %q lacks the omniddi_ prefix", kind, name)
		}
		if seen[kind+name] {
			t.Errorf("%s %q registered twice", kind, name)
		}
		seen[kind+name] = true
	}
	for _, f := range p.Resources(ctx) {
		var r fwresource.MetadataResponse
		f().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "omniddi"}, &r)
		checkName("resource", r.TypeName)
	}
	for _, f := range p.DataSources(ctx) {
		var r datasource.MetadataResponse
		f().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "omniddi"}, &r)
		checkName("data source", r.TypeName)
	}
	if _, ok := p.(fwprovider.ProviderWithListResources); !ok {
		t.Errorf("provider must implement ProviderWithListResources")
	}
	if validators, ok := p.(fwprovider.ProviderWithConfigValidators); !ok || len(validators.ConfigValidators(ctx)) == 0 {
		t.Errorf("provider must declare config validators")
	}
}

func configureWithEndpoint(t *testing.T, endpoint string) fwprovider.ConfigureResponse {
	t.Helper()
	clearOmniEnv(t)
	ctx := context.Background()
	p := omniprovider.New("1.2.3")()
	var schemaResp fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &schemaResp)
	objType, _ := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	values["endpoint"] = tftypes.NewValue(tftypes.String, endpoint)
	values["username"] = tftypes.NewValue(tftypes.String, fakeserver.DefaultUsername)
	values["password"] = tftypes.NewValue(tftypes.String, fakeserver.DefaultPassword)

	var resp fwprovider.ConfigureResponse
	p.Configure(ctx, fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, values)}}, &resp)
	return resp
}

func TestConfigureWarnsAboutPlaintextHTTPOnlyForNonLoopbackHosts(t *testing.T) {
	tests := map[string]bool{
		"http://omniddi.example.com":      true,
		"http://10.0.0.5:8000":            true,
		"HTTP://Omniddi.Example.com":      true,
		"https://omniddi.example.com":     false,
		"http://localhost:8000":           false,
		"http://127.0.0.1:8000":           false,
		"http://127.1.2.3":                false,
		"http://[::1]:8000":               false,
		"http://omniddi.localhost":        false,
		"https://127.0.0.1":               false,
		"http://omniddi.localhost.evil.x": true,
	}
	for endpoint, wantWarning := range tests {
		t.Run(endpoint, func(t *testing.T) {
			resp := configureWithEndpoint(t, endpoint)
			if resp.Diagnostics.HasError() {
				t.Fatalf("diags = %v", resp.Diagnostics)
			}
			warned := false
			for _, d := range resp.Diagnostics.Warnings() {
				warned = warned || strings.Contains(d.Summary(), "plaintext HTTP")
			}
			if warned != wantWarning {
				t.Errorf("warned = %v, want %v (%v)", warned, wantWarning, resp.Diagnostics)
			}
		})
	}
}

func TestProxyURLIsSensitive(t *testing.T) {
	var resp fwprovider.SchemaResponse
	omniprovider.New("test")().Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)
	for _, gone := range []string{"api_key"} {
		if _, ok := resp.Schema.Attributes[gone]; ok {
			t.Errorf("%s must not be a provider attribute", gone)
		}
	}
	for _, name := range []string{"proxy_url", "password", "client_key_pem"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok || !attr.IsSensitive() {
			t.Errorf("%s must be sensitive", name)
		}
	}
}

func TestProviderMaxRetriesZeroDisablesRetriesAndTheDefaultAbsorbsTransientFailures(t *testing.T) {
	clearOmniEnv(t)

	t.Run("zero", func(t *testing.T) {
		srv := fakeserver.New(t)
		srv.FailNext("GET", "/api/v1/health/", 503, 1)
		providerCase(t, resource.TestStep{
			Config:      acctest.ProviderConfigWith(srv.URL(), "  max_retries = 0\n") + probeConfig,
			ExpectError: regexp.MustCompile(`health check failed`),
		})
		if got := len(srv.RequestsFor("GET", "/api/v1/health/")); got != 1 {
			t.Errorf("health requests = %d, want 1", got)
		}
	})
	t.Run("default", func(t *testing.T) {
		srv := fakeserver.New(t)
		srv.FailNext("GET", "/api/v1/health/", 503, 1)
		providerCase(t, resource.TestStep{
			Config: acctest.ProviderConfig(srv.URL()) + probeConfig,
			Check:  resource.TestCheckResourceAttr("data.omniddi_probe.p", "backend_version", fakeserver.DefaultVersion),
		})
	})
}

func TestProviderExposesExactlyTheV1Surface(t *testing.T) {
	ctx := context.Background()
	p := omniprovider.New("test")()
	var resources, dataSources []string
	for _, f := range p.Resources(ctx) {
		var r fwresource.MetadataResponse
		f().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "omniddi"}, &r)
		resources = append(resources, r.TypeName)
	}
	for _, f := range p.DataSources(ctx) {
		var r datasource.MetadataResponse
		f().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "omniddi"}, &r)
		dataSources = append(dataSources, r.TypeName)
	}
	wantResources := []string{
		"omniddi_dhcp_host_reservation", "omniddi_dhcp_subnet", "omniddi_dns_record",
		"omniddi_dns_zone", "omniddi_ipam_container", "omniddi_ipam_subnet",
	}
	wantDataSources := []string{
		"omniddi_dns_view", "omniddi_dns_views", "omniddi_dns_zone", "omniddi_dns_zones",
		"omniddi_ipam_container", "omniddi_ipam_containers", "omniddi_ipam_node",
		"omniddi_ipam_subnet", "omniddi_ipam_subnets", "omniddi_network_space",
		"omniddi_network_spaces", "omniddi_server_group", "omniddi_server_groups",
	}
	slices.Sort(resources)
	slices.Sort(dataSources)
	if !slices.Equal(resources, wantResources) {
		t.Errorf("resources = %v, want %v", resources, wantResources)
	}
	if !slices.Equal(dataSources, wantDataSources) {
		t.Errorf("data sources = %v, want %v", dataSources, wantDataSources)
	}
}

func TestProviderEmptyCredentialsFallBackToEnvironment(t *testing.T) {
	clearOmniEnv(t)
	srv := fakeserver.New(t)
	handleProbe(srv)
	t.Setenv(omniprovider.EnvUsername, fakeserver.DefaultUsername)
	t.Setenv(omniprovider.EnvPassword, fakeserver.DefaultPassword)

	providerCase(t, resource.TestStep{
		Config: fmt.Sprintf(`provider "omniddi" {
  endpoint = %q
  username = ""
  password = ""
}`, srv.URL()) + probeConfig,
		Check: resource.TestCheckResourceAttr("data.omniddi_probe.p", "network_space_id", "1"),
	})
	if got := srv.AuthCounts().Logins; got == 0 {
		t.Errorf("empty username and password must fall back to OMNIDDI_USERNAME and OMNIDDI_PASSWORD")
	}
}
