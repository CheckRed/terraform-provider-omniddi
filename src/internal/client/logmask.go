package client

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var secretFieldKeys = []string{
	"api_key", "password", "client_key_pem", "proxy_url", "access_token", "refresh_token", "Authorization", "X-API-Key", "Cookie",
}

func MaskSecrets(ctx context.Context, exact ...string) context.Context {
	ctx = tflog.MaskFieldValuesWithFieldKeys(ctx, secretFieldKeys...)
	ctx = tflog.MaskAllFieldValuesRegexes(ctx, apiKeySecret, bearerSecret, jwtSecret)
	if values := slices.DeleteFunc(slices.Clone(exact), func(v string) bool { return v == "" }); len(values) > 0 {
		ctx = tflog.MaskAllFieldValuesStrings(ctx, values...)
	}
	return ctx
}

func (c Config) secretValues() []string {
	return []string{c.Password, c.ClientKeyPEM, c.ProxyURL}
}

func (c *Client) MaskedContext(ctx context.Context) context.Context {
	return MaskSecrets(ctx, c.t.secrets...)
}
