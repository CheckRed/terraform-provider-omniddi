package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultMaxConcurrency = 8
	DefaultMaxRetries     = 3
	DefaultRequestTimeout = 60 * time.Second
	DefaultMaxResponse    = 32 << 20
	apiPrefix             = "/api/v1"
	graphQLPath           = "/graphql"
)

type Backoff struct {
	Min time.Duration
	Max time.Duration
}

type PollConfig struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	EmptyGrace      time.Duration
}

type Config struct {
	Endpoint         string
	Username         string
	Password         string
	NetworkSpaceID   int64
	CACertPEM        string
	ClientCertPEM    string
	ClientKeyPEM     string
	ProxyURL         string
	Insecure         bool
	RequestTimeout   time.Duration
	MaxConcurrency   int
	MaxRetries       int
	NoRetries        bool
	MaxResponseBytes int64
	RetryBackoff     Backoff
	TaskPoll         PollConfig
	ApprovalPoll     PollConfig
	ProviderVersion  string
	TerraformVersion string
	Transport        http.RoundTripper
	Now              func() time.Time
}

func (c Config) String() string {
	return fmt.Sprintf("client.Config{endpoint=%s auth=password network_space_id=%d}", c.Endpoint, c.NetworkSpaceID)
}

func (c Config) GoString() string { return c.String() }

func (c Config) withDefaults() Config {
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	switch {
	case c.NoRetries:
		c.MaxRetries = 0
	case c.MaxRetries <= 0:
		c.MaxRetries = DefaultMaxRetries
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = DefaultMaxResponse
	}
	if c.RetryBackoff.Min <= 0 {
		c.RetryBackoff.Min = time.Second
	}
	if c.RetryBackoff.Max < c.RetryBackoff.Min {
		c.RetryBackoff.Max = 10 * time.Second
		if c.RetryBackoff.Max < c.RetryBackoff.Min {
			c.RetryBackoff.Max = c.RetryBackoff.Min
		}
	}
	c.TaskPoll = c.TaskPoll.withDefaults(time.Second, 10*time.Second, 30*time.Second)
	c.ApprovalPoll = c.ApprovalPoll.withDefaults(5*time.Second, 30*time.Second, 0)
	if c.Now == nil {
		c.Now = time.Now
	}
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	return c
}

func (p PollConfig) withDefaults(initial, maxInterval, grace time.Duration) PollConfig {
	if p.InitialInterval <= 0 {
		p.InitialInterval = initial
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = maxInterval
	}
	if p.MaxInterval < p.InitialInterval {
		p.MaxInterval = p.InitialInterval
	}
	if p.EmptyGrace <= 0 {
		p.EmptyGrace = grace
	}
	return p
}

func (c Config) validate() (*url.URL, error) {
	base, err := url.Parse(c.Endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, &ConfigError{Message: "endpoint must be an absolute http or https URL"}
	}
	if base.RawQuery != "" || base.Fragment != "" {
		return nil, &ConfigError{Message: "endpoint must not contain a query string or fragment"}
	}
	if err := validateCredentials(c.Username, c.Password); err != nil {
		return nil, err
	}
	if c.NetworkSpaceID < 0 {
		return nil, &ConfigError{Message: "network_space_id must be a positive integer"}
	}
	if (c.ClientCertPEM == "") != (c.ClientKeyPEM == "") {
		return nil, &ConfigError{Message: "client_cert_pem and client_key_pem must be set together"}
	}
	return base, nil
}

func (c Config) httpTransport() (http.RoundTripper, error) {
	if c.Transport != nil {
		return c.Transport, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.Insecure} //nolint:gosec // opt-in dev flag
	if c.CACertPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(c.CACertPEM)) {
			return nil, &ConfigError{Message: "ca_cert_pem contains no valid PEM certificate"}
		}
		tlsConfig.RootCAs = pool
	}
	if c.ClientCertPEM != "" {
		pair, err := tls.X509KeyPair([]byte(c.ClientCertPEM), []byte(c.ClientKeyPEM))
		if err != nil {
			return nil, &ConfigError{Message: "client_cert_pem and client_key_pem do not form a valid key pair"}
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	proxy := http.ProxyFromEnvironment
	if c.ProxyURL != "" {
		proxyURL, err := url.Parse(c.ProxyURL)
		if err != nil || proxyURL.Host == "" {
			return nil, &ConfigError{Message: "proxy_url must be an absolute URL"}
		}
		proxy = http.ProxyURL(proxyURL)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, &ConfigError{Message: "default HTTP transport is unavailable"}
	}
	transport := base.Clone()
	transport.TLSClientConfig = tlsConfig
	transport.Proxy = proxy
	transport.MaxIdleConnsPerHost = c.MaxConcurrency
	return transport, nil
}
