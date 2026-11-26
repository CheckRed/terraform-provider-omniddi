package provider

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/providervalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/platform"
	"github.com/CheckRed/terraform-provider-omniddi/internal/validators"
)

const (
	EnvEndpoint       = "OMNIDDI_ENDPOINT"
	EnvUsername       = "OMNIDDI_USERNAME"
	EnvPassword       = "OMNIDDI_PASSWORD"
	EnvNetworkSpaceID = "OMNIDDI_NETWORK_SPACE_ID"
	EnvCACertPEM      = "OMNIDDI_CA_CERT_PEM"
	EnvClientCertPEM  = "OMNIDDI_CLIENT_CERT_PEM"
	EnvClientKeyPEM   = "OMNIDDI_CLIENT_KEY_PEM"
	EnvProxyURL       = "OMNIDDI_PROXY_URL"
	EnvInsecure       = "OMNIDDI_INSECURE"

	MinimumTerraformVersion = "1.12.0"
	EnvTimeout              = "OMNIDDI_TIMEOUT"
	EnvApprovalMode         = "OMNIDDI_APPROVAL_MODE"
	EnvMaxConcurrency       = "OMNIDDI_MAX_CONCURRENCY"
	EnvMaxRetries           = "OMNIDDI_MAX_RETRIES"

	defaultTimeout = 10 * time.Minute
)

var _ provider.ProviderWithConfigValidators = (*OmniDDIProvider)(nil)
var _ provider.ProviderWithListResources = (*OmniDDIProvider)(nil)

type OmniDDIProvider struct {
	version string
	tune    func(*client.Config)
}

type providerModel struct {
	Endpoint       types.String `tfsdk:"endpoint"`
	Username       types.String `tfsdk:"username"`
	Password       types.String `tfsdk:"password"`
	NetworkSpaceID types.Int64  `tfsdk:"network_space_id"`
	CACertPEM      types.String `tfsdk:"ca_cert_pem"`
	ClientCertPEM  types.String `tfsdk:"client_cert_pem"`
	ClientKeyPEM   types.String `tfsdk:"client_key_pem"`
	ProxyURL       types.String `tfsdk:"proxy_url"`
	Insecure       types.Bool   `tfsdk:"insecure"`
	Timeout        types.String `tfsdk:"timeout"`
	ApprovalMode   types.String `tfsdk:"approval_mode"`
	MaxConcurrency types.Int64  `tfsdk:"max_concurrency"`
	MaxRetries     types.Int64  `tfsdk:"max_retries"`
	DefaultTags    types.Map    `tfsdk:"default_tags"`
}

func New(version string) func() provider.Provider {
	return NewWithClientTuning(version, nil)
}

func NewWithClientTuning(version string, tune func(*client.Config)) func() provider.Provider {
	return func() provider.Provider { return &OmniDDIProvider{version: version, tune: tune} }
}

func (p *OmniDDIProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "omniddi"
	resp.Version = p.version
}

func (p *OmniDDIProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage DNS, DHCP and IPAM objects in OmniDDI. Every attribute except `default_tags` can also be set through an `OMNIDDI_*` environment variable; the attribute wins when both are present.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Base URL of the OmniDDI management server, for example `https://omniddi.example.com`. It must use `https://`; `http://` is accepted only for `localhost` and loopback addresses. Environment variable: `OMNIDDI_ENDPOINT`.",
			},
			"username": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Login name (email, or LDAP user name) of the OmniDDI account Terraform signs in as. The account's roles and permissions apply. The account must not have MFA enabled. Required. Environment variable: `OMNIDDI_USERNAME`.",
			},
			"password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Password of that account. Set it through `OMNIDDI_PASSWORD` rather than in configuration. Required. Environment variable: `OMNIDDI_PASSWORD`.",
			},
			"network_space_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Network space to work in. After signing in, the provider switches to this space. When unset, the account's landing space is used. Environment variable: `OMNIDDI_NETWORK_SPACE_ID`.",
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"ca_cert_pem": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "PEM-encoded CA certificate(s) to trust for the endpoint. Environment variable: `OMNIDDI_CA_CERT_PEM`.",
			},
			"client_cert_pem": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "PEM-encoded client certificate for mutual TLS. Environment variable: `OMNIDDI_CLIENT_CERT_PEM`.",
			},
			"client_key_pem": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "PEM-encoded private key for the client certificate. Environment variable: `OMNIDDI_CLIENT_KEY_PEM`.",
			},
			"proxy_url": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "HTTP proxy URL. When unset, `HTTPS_PROXY` and `NO_PROXY` are honored. Environment variable: `OMNIDDI_PROXY_URL`.",
			},
			"insecure": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip TLS certificate verification. Development only. Environment variable: `OMNIDDI_INSECURE`.",
			},
			"timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default time to wait for a change to be applied, as a duration string such as `10m`. Defaults to `10m`. Environment variable: `OMNIDDI_TIMEOUT`.",
				Validators:          []validator.String{validators.Duration()},
			},
			"approval_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Behavior when OmniDDI requires approval: `fail` (default) or `wait`. Environment variable: `OMNIDDI_APPROVAL_MODE`.",
				Validators:          []validator.String{stringvalidator.OneOf(string(framework.ApprovalModeFail), string(framework.ApprovalModeWait))},
			},
			"max_concurrency": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum in-flight API calls. Defaults to 8. Environment variable: `OMNIDDI_MAX_CONCURRENCY`.",
				Validators:          []validator.Int64{int64validator.Between(1, 64)},
			},
			"max_retries": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum retries for read-only calls on transient failures. Writes are never retried. Defaults to 3. Environment variable: `OMNIDDI_MAX_RETRIES`.",
				Validators:          []validator.Int64{int64validator.Between(0, 10)},
			},
			"default_tags": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tags applied to every taggable resource, merged with the resource `tags`.",
			},
		},
	}
}

func (p *OmniDDIProvider) ConfigValidators(context.Context) []provider.ConfigValidator {
	return []provider.ConfigValidator{
		providervalidator.RequiredTogether(path.MatchRoot("client_cert_pem"), path.MatchRoot("client_key_pem")),
	}
}

func (p *OmniDDIProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	warnOnOldTerraform(req.TerraformVersion, &resp.Diagnostics)
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if unknownAttrs := unknownAttributes(cfg); len(unknownAttrs) > 0 {
		for _, name := range unknownAttrs {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider configuration value",
				fmt.Sprintf("The provider cannot be configured while %q is unknown. Set it to a known value or use the matching OMNIDDI_* environment variable.", name))
		}
		return
	}

	ctx = tflog.MaskFieldValuesWithFieldKeys(ctx, "password", "client_key_pem", "access_token", "refresh_token", "Authorization", "Cookie")

	settings, diagsOK := resolveSettings(cfg, &resp.Diagnostics)
	if !diagsOK {
		return
	}
	ctx = client.MaskSecrets(ctx, settings.client.Password, settings.client.ClientKeyPEM, settings.client.ProxyURL)
	if insecureTransport(settings.client.Endpoint) {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Endpoint must use HTTPS",
			"The endpoint is an http:// URL on a non-loopback host, so the sign-in would send the username and password unencrypted. "+
				"Nothing was sent. Use an https:// endpoint; http:// is accepted only for localhost and loopback addresses.")
		return
	}
	if settings.client.Insecure && !plainHTTP(settings.client.Endpoint) {
		resp.Diagnostics.AddWarning("TLS verification is disabled",
			"insecure = true disables TLS certificate verification for every OmniDDI call. Use it for development only.")
	}
	settings.client.ProviderVersion = p.version
	settings.client.TerraformVersion = req.TerraformVersion
	if p.tune != nil {
		p.tune(&settings.client)
	}

	c, err := client.New(ctx, settings.client)
	if err != nil {
		framework.AddClientError(&resp.Diagnostics, "Unable to configure the OmniDDI client", err)
		return
	}
	data := &framework.ProviderData{
		Client:         c,
		DefaultTimeout: settings.timeout,
		ApprovalMode:   settings.approvalMode,
		DefaultTags:    settings.defaultTags,
	}
	resp.ResourceData = data
	resp.DataSourceData = data
	resp.ListResourceData = data
}

func (p *OmniDDIProvider) Resources(context.Context) []func() resource.Resource {
	var all []func() resource.Resource
	for _, group := range [][]func() resource.Resource{dns.Resources(), ipam.Resources(), dhcp.Resources(), platform.Resources()} {
		all = append(all, group...)
	}
	return all
}

func (p *OmniDDIProvider) DataSources(context.Context) []func() datasource.DataSource {
	var all []func() datasource.DataSource
	for _, group := range [][]func() datasource.DataSource{dns.DataSources(), ipam.DataSources(), dhcp.DataSources(), platform.DataSources()} {
		all = append(all, group...)
	}
	return all
}

func (p *OmniDDIProvider) ListResources(context.Context) []func() list.ListResource {
	var all []func() list.ListResource
	for _, group := range [][]func() list.ListResource{dns.ListResources(), ipam.ListResources(), dhcp.ListResources(), platform.ListResources()} {
		all = append(all, group...)
	}
	return all
}

var minimumTerraform = version.Must(version.NewVersion(MinimumTerraformVersion))

func warnOnOldTerraform(running string, diags *diag.Diagnostics) {
	v, err := version.NewVersion(running)
	if err != nil || !v.LessThan(minimumTerraform) {
		return
	}
	diags.AddWarning("Terraform version is below the supported minimum",
		fmt.Sprintf("Terraform %s is running, but this provider supports Terraform %s or later. Reads and writes may work, "+
			"but import by resource identity (import blocks with identity) needs %s. Upgrade Terraform.",
			v, MinimumTerraformVersion, MinimumTerraformVersion))
}

func plainHTTP(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && strings.EqualFold(u.Scheme, "http")
}

func insecureTransport(endpoint string) bool {
	if !plainHTTP(endpoint) {
		return false
	}
	u, _ := url.Parse(endpoint)
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

type resolved struct {
	client       client.Config
	timeout      time.Duration
	approvalMode framework.ApprovalMode
	defaultTags  map[string]string
}

func unknownAttributes(cfg providerModel) []string {
	checks := map[string]bool{
		"endpoint": cfg.Endpoint.IsUnknown(), "username": cfg.Username.IsUnknown(),
		"password": cfg.Password.IsUnknown(), "network_space_id": cfg.NetworkSpaceID.IsUnknown(),
		"ca_cert_pem": cfg.CACertPEM.IsUnknown(), "client_cert_pem": cfg.ClientCertPEM.IsUnknown(),
		"client_key_pem": cfg.ClientKeyPEM.IsUnknown(), "proxy_url": cfg.ProxyURL.IsUnknown(),
		"insecure": cfg.Insecure.IsUnknown(), "timeout": cfg.Timeout.IsUnknown(), "approval_mode": cfg.ApprovalMode.IsUnknown(),
		"max_concurrency": cfg.MaxConcurrency.IsUnknown(), "max_retries": cfg.MaxRetries.IsUnknown(),
		"default_tags": cfg.DefaultTags.IsUnknown(),
	}
	var names []string
	for name, unknown := range checks {
		if unknown {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func resolveSettings(cfg providerModel, diags *diag.Diagnostics) (resolved, bool) {
	ok := true
	fail := func(attr, msg string) {
		diags.AddAttributeError(path.Root(attr), "Invalid provider configuration", msg)
		ok = false
	}

	out := resolved{approvalMode: framework.ApprovalMode(stringSetting(cfg.ApprovalMode, EnvApprovalMode)), timeout: defaultTimeout}
	if out.approvalMode == "" {
		out.approvalMode = framework.ApprovalModeFail
	}
	if out.approvalMode != framework.ApprovalModeFail && out.approvalMode != framework.ApprovalModeWait {
		fail("approval_mode", fmt.Sprintf("approval_mode must be %q or %q.", framework.ApprovalModeFail, framework.ApprovalModeWait))
	}
	if raw := stringSetting(cfg.Timeout, EnvTimeout); raw != "" {
		d, err := validators.ParseDuration(raw)
		if err != nil {
			fail("timeout", err.Error())
		}
		out.timeout = d
	}

	c := client.Config{
		Endpoint:      stringSetting(cfg.Endpoint, EnvEndpoint),
		Username:      nonEmptySetting(cfg.Username, EnvUsername),
		Password:      nonEmptySetting(cfg.Password, EnvPassword),
		CACertPEM:     stringSetting(cfg.CACertPEM, EnvCACertPEM),
		ClientCertPEM: stringSetting(cfg.ClientCertPEM, EnvClientCertPEM),
		ClientKeyPEM:  stringSetting(cfg.ClientKeyPEM, EnvClientKeyPEM),
		ProxyURL:      stringSetting(cfg.ProxyURL, EnvProxyURL),
		Insecure:      boolSetting(cfg.Insecure, EnvInsecure),
		MaxRetries:    client.DefaultMaxRetries,
	}
	if n, set, err := int64Setting(cfg.NetworkSpaceID, EnvNetworkSpaceID); err != nil || (set && n < 1) {
		fail("network_space_id", "network_space_id must be a positive integer.")
	} else {
		c.NetworkSpaceID = n
	}
	if n, set, err := int64Setting(cfg.MaxConcurrency, EnvMaxConcurrency); err != nil || (set && (n < 1 || n > 64)) {
		fail("max_concurrency", "max_concurrency must be between 1 and 64.")
	} else if set {
		c.MaxConcurrency = int(n)
	}
	if n, set, err := int64Setting(cfg.MaxRetries, EnvMaxRetries); err != nil || (set && (n < 0 || n > 10)) {
		fail("max_retries", "max_retries must be between 0 and 10.")
	} else if set {
		c.MaxRetries, c.NoRetries = int(n), n == 0
	}

	if c.Endpoint == "" {
		fail("endpoint", "An endpoint is required: set endpoint or the OMNIDDI_ENDPOINT environment variable.")
	}
	if c.Username == "" {
		fail("username", "A username is required: set username or the OMNIDDI_USERNAME environment variable.")
	}
	if c.Password == "" {
		fail("password", "A password is required: set the OMNIDDI_PASSWORD environment variable, or password.")
	}

	if !cfg.DefaultTags.IsNull() {
		tags := map[string]string{}
		for name, v := range cfg.DefaultTags.Elements() {
			sv, isString := v.(types.String)
			if !isString || sv.IsNull() || sv.IsUnknown() || name == "" {
				fail("default_tags", "default_tags must map non-empty tag names to string values.")
				break
			}
			tags[name] = sv.ValueString()
		}
		out.defaultTags = tags
	}
	out.client = c
	return out, ok
}

func nonEmptySetting(v types.String, env string) string {
	if value := v.ValueString(); value != "" {
		return value
	}
	return os.Getenv(env)
}

func stringSetting(v types.String, env string) string {
	if !v.IsNull() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func boolSetting(v types.Bool, env string) bool {
	if !v.IsNull() {
		return v.ValueBool()
	}
	parsed, _ := strconv.ParseBool(os.Getenv(env))
	return parsed
}

func int64Setting(v types.Int64, env string) (value int64, set bool, err error) {
	if !v.IsNull() {
		return v.ValueInt64(), true, nil
	}
	raw := os.Getenv(env)
	if raw == "" {
		return 0, false, nil
	}
	value, err = strconv.ParseInt(raw, 10, 64)
	return value, true, err
}
