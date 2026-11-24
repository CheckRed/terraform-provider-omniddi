package client_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func TestLoginSendsBearerUserAgentAndFreshRequestIDs(t *testing.T) {
	cfg := fastConfig("")
	cfg.ProviderVersion, cfg.TerraformVersion = "9.9.9", "1.17.1"
	srv := fakeserver.New(t)
	cfg.Endpoint = srv.URL()
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")

	for range 2 {
		if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}

	reqs := srv.RequestsFor("GET", "/api/v1/things/")
	if len(reqs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(reqs))
	}
	for _, r := range reqs {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing bearer token")
		}
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("X-API-Key = %q, want none", got)
		}
		if got, want := r.Header.Get("User-Agent"), "terraform-provider-omniddi/9.9.9 terraform/1.17.1"; got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
	}
	if reqs[0].Header.Get("X-Request-ID") == reqs[1].Header.Get("X-Request-ID") || reqs[0].Header.Get("X-Request-ID") == "" {
		t.Errorf("X-Request-ID must be fresh per request")
	}
	if counts := srv.AuthCounts(); counts.Logins != 1 || counts.Switches != 0 || counts.Refreshes != 0 {
		t.Errorf("counts = %+v, want exactly 1 login", counts)
	}
	if c.NetworkSpaceID() != fakeserver.DefaultSpaceID {
		t.Errorf("NetworkSpaceID = %d, want the landing space %d", c.NetworkSpaceID(), fakeserver.DefaultSpaceID)
	}
}

func TestLoginSwitchesToConfiguredNetworkSpace(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.NetworkSpaceID = 2
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")

	for range 3 {
		if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if counts := srv.AuthCounts(); counts.Logins != 1 || counts.Switches != 1 || counts.Refreshes != 0 {
		t.Errorf("counts = %+v, want 1 login, 1 switch, 0 refreshes", counts)
	}
	if c.NetworkSpaceID() != 2 {
		t.Errorf("NetworkSpaceID = %d, want 2", c.NetworkSpaceID())
	}
}

func TestSwitchToForbiddenSpaceFails(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.NetworkSpaceID = 99
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")

	err := c.Get(context.Background(), "/things/", nil, nil)
	var authErr *client.AuthError
	if !errors.As(err, &authErr) || authErr.StatusCode != http.StatusForbidden {
		t.Fatalf("err = %v, want AuthError 403", err)
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 0 {
		t.Errorf("no API call may be made after a failed switch, got %d", got)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*client.Config)
		want   string
	}{
		{"missing endpoint", func(c *client.Config) { c.Endpoint = "" }, "endpoint"},
		{"bad scheme", func(c *client.Config) { c.Endpoint = "ftp://example.com" }, "endpoint"},
		{"no username", func(c *client.Config) { c.Username = "" }, "username"},
		{"no password", func(c *client.Config) { c.Password = "" }, "password"},
		{"username with newline", func(c *client.Config) { c.Username = fakeserver.DefaultUsername + "\n" }, "username"},
		{"negative network space", func(c *client.Config) { c.NetworkSpaceID = -1 }, "network_space_id"},
		{"cert without key", func(c *client.Config) { c.ClientCertPEM = "x" }, "together"},
		{"bad CA", func(c *client.Config) { c.CACertPEM = "not pem" }, "ca_cert_pem"},
		{"bad proxy", func(c *client.Config) { c.ProxyURL = "::bad" }, "proxy_url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fastConfig("https://omniddi.example.com")
			tc.mutate(&cfg)
			_, err := client.New(context.Background(), cfg)
			var cfgErr *client.ConfigError
			if !errors.As(err, &cfgErr) || !strings.Contains(cfgErr.Message, tc.want) {
				t.Fatalf("err = %v, want ConfigError containing %q", err, tc.want)
			}
			if cfg.Password != "" && strings.Contains(err.Error(), cfg.Password) {
				t.Errorf("error must not echo the password")
			}
		})
	}
}

func TestConfigStringHidesSecrets(t *testing.T) {
	cfg := fastConfig("https://omniddi.example.com")
	cfg.ClientKeyPEM = "synthetic-client-key"
	text := cfg.String() + cfg.GoString()
	for _, secret := range []string{fakeserver.DefaultPassword, cfg.ClientKeyPEM} {
		if strings.Contains(text, secret) {
			t.Errorf("Config string leaked a secret: %s", text)
		}
	}
}

func TestRefreshesAtEightyPercentOfLifetime(t *testing.T) {
	clock := newTestClock()
	srv := fakeserver.New(t, fakeserver.WithClock(clock.Now), fakeserver.WithTokenTTL(100*time.Second))
	cfg := fastConfig(srv.URL())
	cfg.NetworkSpaceID = 2
	cfg.Now = clock.Now
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")
	get := func() {
		t.Helper()
		if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}

	get()
	clock.Advance(79 * time.Second)
	get()
	if got := srv.AuthCounts().Refreshes; got != 0 {
		t.Fatalf("refreshed before 80%% of the lifetime: %d", got)
	}

	clock.Advance(2 * time.Second)
	get()
	counts := srv.AuthCounts()
	if counts.Refreshes != 1 || counts.Logins != 1 {
		t.Errorf("counts = %+v, want 1 refresh and still 1 login", counts)
	}
	if c.NetworkSpaceID() != 2 {
		t.Errorf("refresh must stay in the configured space, got %d", c.NetworkSpaceID())
	}

	clock.Advance(81 * time.Second)
	get()
	if got := srv.AuthCounts().Refreshes; got != 2 {
		t.Errorf("rotated refresh token must be reusable once per rotation, refreshes = %d", got)
	}
}

func TestUnauthorizedTriggersOneRefreshAndOneRetry(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	echoHandler(srv, "GET", "/api/v1/things/")
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}

	srv.ExpireAccessTokens()
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get after expiry: %v", err)
	}
	if counts := srv.AuthCounts(); counts.Refreshes != 1 || counts.Logins != 1 {
		t.Errorf("counts = %+v, want 1 refresh, 1 login", counts)
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 3 {
		t.Errorf("expected 3 GETs (ok, 401, retry), got %d", got)
	}
}

func TestUnauthorizedWritesRetryOnceAfterRefresh(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	srv.HandleREST("POST", "/api/v1/zones/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"zone_id": 5}, fakeserver.StatusCompleted)
	})
	if _, err := c.Write(context.Background(), "POST", "/zones/", map[string]any{"name": "example.com"}); err != nil {
		t.Fatal(err)
	}
	srv.ExpireAccessTokens()
	res, err := c.Write(context.Background(), "POST", "/zones/", map[string]any{"name": "example.com"})
	if err != nil {
		t.Fatalf("Write after expiry: %v", err)
	}
	if id, _ := res.Int("zone_id"); id != 5 {
		t.Errorf("zone_id = %d", id)
	}
	if got := len(srv.RequestsFor("POST", "/api/v1/zones/")); got != 3 {
		t.Errorf("expected 3 POSTs (ok, 401, retry), got %d", got)
	}
}

func TestRefreshForbiddenFallsBackToOneFreshLogin(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	echoHandler(srv, "GET", "/api/v1/things/")
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}

	srv.ExpireAccessTokens()
	srv.RevokeRefreshTokens()
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if counts := srv.AuthCounts(); counts.Logins != 2 || counts.Refreshes != 1 {
		t.Errorf("counts = %+v, want 2 logins and 1 refresh", counts)
	}
}

func TestConcurrentCallsShareOneRefresh(t *testing.T) {
	clock := newTestClock()
	srv := fakeserver.New(t, fakeserver.WithClock(clock.Now), fakeserver.WithTokenTTL(100*time.Second))
	cfg := fastConfig(srv.URL())
	cfg.Now = clock.Now
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}

	clock.Advance(90 * time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Get(context.Background(), "/things/", nil, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Get: %v", err)
		}
	}
	if got := srv.AuthCounts().Refreshes; got != 1 {
		t.Errorf("refreshes = %d, want exactly 1 (token rotation must be serialized)", got)
	}
}

func TestLoginFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeserver.Server, *client.Config)
		want  string
	}{
		{"bad password", func(_ *fakeserver.Server, c *client.Config) { c.Password = "wrong-synthetic-password" }, "login failed with HTTP 401"},
		{"mfa required", func(s *fakeserver.Server, c *client.Config) {
			s.AddUser(fakeserver.User{Email: "mfa@example.com", Password: "synthetic-pw", MFARequired: true})
			c.Username, c.Password = "mfa@example.com", "synthetic-pw"
		}, "MFA"},
		{"password change required", func(s *fakeserver.Server, c *client.Config) {
			s.AddUser(fakeserver.User{Email: "new@example.com", Password: "synthetic-pw", RequiresPasswordChange: true})
			c.Username, c.Password = "new@example.com", "synthetic-pw"
		}, "password change or onboarding"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			cfg := fastConfig(srv.URL())
			tc.setup(srv, &cfg)
			c := mustClient(t, cfg)
			echoHandler(srv, "GET", "/api/v1/things/")

			err := c.Get(context.Background(), "/things/", nil, nil)
			var authErr *client.AuthError
			if !errors.As(err, &authErr) || !strings.Contains(authErr.Message, tc.want) {
				t.Fatalf("err = %v, want AuthError containing %q", err, tc.want)
			}
			if authErr.RequestID == "" {
				t.Errorf("AuthError must carry the request id")
			}
			if strings.Contains(err.Error(), cfg.Password) {
				t.Errorf("error must not echo the password")
			}
			if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 0 {
				t.Errorf("no API call may be made after a failed login, got %d", got)
			}
		})
	}
}

func TestLoginRetriesOnRateLimit(t *testing.T) {
	srv := fakeserver.New(t)
	srv.FailNext("POST", "/api/v1/users/token", http.StatusTooManyRequests, 2)
	c := mustClient(t, fastConfig(srv.URL()))
	echoHandler(srv, "GET", "/api/v1/things/")

	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(srv.RequestsFor("POST", "/api/v1/users/token")); got != 3 {
		t.Errorf("login attempts = %d, want 3", got)
	}
}

func TestLoginSendsCredentialsOnlyToTheLoginRoute(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	echoHandler(srv, "GET", "/api/v1/things/")
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if r.Path == "/api/v1/users/token" {
			continue
		}
		if strings.Contains(string(r.Body), fakeserver.DefaultPassword) {
			t.Errorf("password sent to %s %s", r.Method, r.Path)
		}
	}
}

func TestAuthenticateForcesLoginEagerly(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.AuthCounts().Logins != 1 {
		t.Errorf("counts = %+v", srv.AuthCounts())
	}

	bad := fastConfig(srv.URL())
	bad.Password = "wrong-synthetic-password"
	var authErr *client.AuthError
	if err := mustClient(t, bad).Authenticate(context.Background()); !errors.As(err, &authErr) {
		t.Errorf("err = %v, want AuthError", err)
	}
}

func TestRejectedCredentialsAreTriedOnlyOnce(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.Password = "wrong-synthetic-password"
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var authErr *client.AuthError
			if err := c.Get(context.Background(), "/things/", nil, nil); !errors.As(err, &authErr) {
				t.Errorf("err = %v, want AuthError", err)
			}
		}()
	}
	wg.Wait()
	if got := srv.AuthCounts().Logins; got != 1 {
		t.Errorf("logins = %d, want 1 (a rejected password must not be retried)", got)
	}
}

func TestForbiddenSpaceIsTriedOnlyOnce(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.NetworkSpaceID = 99
	c := mustClient(t, cfg)
	for range 3 {
		_ = c.Get(context.Background(), "/things/", nil, nil)
	}
	if counts := srv.AuthCounts(); counts.Logins != 1 || counts.Switches != 1 {
		t.Errorf("counts = %+v, want 1 login and 1 switch", counts)
	}
}

func TestRefreshUnauthorizedFallsBackToLogin(t *testing.T) {
	srv := fakeserver.New(t)
	c := mustClient(t, fastConfig(srv.URL()))
	echoHandler(srv, "GET", "/api/v1/things/")
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}

	srv.ExpireAccessTokens()
	srv.FailNext("POST", "/api/v1/users/refresh", http.StatusUnauthorized, 1)
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := srv.AuthCounts().Logins; got != 2 {
		t.Errorf("logins = %d, want 2 (refresh 401 must fall back to login)", got)
	}
}
