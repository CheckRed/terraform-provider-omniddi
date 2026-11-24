package client_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func TestIdempotentReadsRetryTransientFailures(t *testing.T) {
	c, srv := newClient(t)
	echoHandler(srv, "GET", "/api/v1/things/")
	srv.FailNext("GET", "/api/v1/things/", http.StatusServiceUnavailable, 2)

	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
}

func TestReadRetriesAreBounded(t *testing.T) {
	c, srv := newClient(t)
	echoHandler(srv, "GET", "/api/v1/things/")
	srv.FailNext("GET", "/api/v1/things/", http.StatusBadGateway, 100)

	err := c.Get(context.Background(), "/things/", nil, nil)
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("err = %v, want APIError 502", err)
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 4 {
		t.Errorf("requests = %d, want 1 + 3 retries", got)
	}
}

func TestWritesAreNeverRetried(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
			t.Run(method, func(t *testing.T) {
				c, srv := newClient(t)
				echoHandler(srv, method, "/api/v1/zones/1")
				srv.FailNext(method, "/api/v1/zones/1", status, 5)

				_, err := c.Write(context.Background(), method, "/zones/1", map[string]any{"a": 1})
				var apiErr *client.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("err = %v, want APIError %d", err, status)
				}
				if got := len(srv.RequestsFor(method, "/api/v1/zones/1")); got != 1 {
					t.Errorf("%s was sent %d times, must be exactly once", method, got)
				}
			})
		}
	}
}

func TestNetworkErrorsOnWritesAreNotRetried(t *testing.T) {
	var hits atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(hs.Close)
	c := mustClient(t, fastConfig(hs.URL))

	if _, err := c.Write(context.Background(), "POST", "/zones/", map[string]any{}); err == nil {
		t.Fatal("expected a network error")
	}
	if hits.Load() != 1 {
		t.Errorf("POST attempted %d times", hits.Load())
	}

	hits.Store(0)
	if err := c.Get(context.Background(), "/things/", nil, nil); err == nil {
		t.Fatal("expected a network error")
	}
	if hits.Load() != 4 {
		t.Errorf("GET attempted %d times, want 1 + 3 retries", hits.Load())
	}
}

func TestGraphQLQueriesAreRetried(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleGraphQL("viewById", func(map[string]any) (any, error) { return map[string]any{"id": 7}, nil })
	srv.FailNext("POST", "/graphql", http.StatusServiceUnavailable, 2)

	var out struct {
		View struct{ ID int64 } `json:"viewById"`
	}
	if err := c.Query(context.Background(), `query { viewById(id: 7) { id } }`, nil, &out); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if out.View.ID != 7 {
		t.Errorf("decoded %+v", out)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	t.Cleanup(target.Close)
	hs := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/zones", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hs.Close)
	c := mustClient(t, fastConfig(hs.URL))

	_, err := c.Write(context.Background(), "POST", "/zones", map[string]any{})
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want APIError 307", err)
	}
	if targetHits.Load() != 0 {
		t.Errorf("redirect was followed")
	}
}

func TestTrailingSlashesArePreserved(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleREST("POST", "/api/v1/zones/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(201, map[string]any{"id": 1})
	})
	if _, err := c.Write(context.Background(), "POST", "/zones/", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(srv.RequestsFor("POST", "/api/v1/zones/")) != 1 {
		t.Errorf("trailing slash was altered: %+v", srv.Requests())
	}
}

func TestMaxConcurrencyBoundsInFlightCalls(t *testing.T) {
	var inFlight, peak atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(hs.Close)
	cfg := fastConfig(hs.URL)
	cfg.MaxConcurrency = 2
	c := mustClient(t, cfg)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Get(context.Background(), "/things/", nil, nil)
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Errorf("peak concurrency %d exceeded the limit of 2", peak.Load())
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	c, srv := newClient(t)
	srv.FailNext("GET", "/api/v1/things/", http.StatusServiceUnavailable, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Get(ctx, "/things/", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got > 1 {
		t.Errorf("retried after cancellation: %d requests", got)
	}
}

func tlsServer(t *testing.T) *httptest.Server {
	t.Helper()
	hs := httptest.NewTLSServer(withLogin(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	t.Cleanup(hs.Close)
	return hs
}

func TestTLSVerification(t *testing.T) {
	hs := tlsServer(t)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}))

	t.Run("untrusted certificate is rejected", func(t *testing.T) {
		c := mustClient(t, fastConfig(hs.URL))
		if err := c.Get(context.Background(), "/things/", nil, nil); err == nil {
			t.Fatal("expected a certificate error")
		}
	})
	t.Run("custom CA is trusted", func(t *testing.T) {
		cfg := fastConfig(hs.URL)
		cfg.CACertPEM = caPEM
		if err := mustClient(t, cfg).Get(context.Background(), "/things/", nil, nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	})
	t.Run("insecure skips verification", func(t *testing.T) {
		cfg := fastConfig(hs.URL)
		cfg.Insecure = true
		if err := mustClient(t, cfg).Get(context.Background(), "/things/", nil, nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	})
	t.Run("mismatched client key pair is a config error", func(t *testing.T) {
		cfg := fastConfig(hs.URL)
		cfg.ClientCertPEM, cfg.ClientKeyPEM = caPEM, caPEM
		_, err := client.New(context.Background(), cfg)
		var cfgErr *client.ConfigError
		if !errors.As(err, &cfgErr) {
			t.Fatalf("err = %v, want ConfigError", err)
		}
	})
}

func TestExplicitProxyIsUsed(t *testing.T) {
	var viaProxy atomic.Int32
	proxy := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Add(1)
		if r.URL.Host != "omniddi.invalid" {
			t.Errorf("proxy saw host %q", r.URL.Host)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(proxy.Close)
	cfg := fastConfig("http://omniddi.invalid")
	cfg.ProxyURL = proxy.URL

	if err := mustClient(t, cfg).Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if viaProxy.Load() != 1 {
		t.Errorf("request did not go through the proxy")
	}
}

func TestHealth(t *testing.T) {
	c, srv := newClient(t)
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" || h.Version != fakeserver.DefaultVersion {
		t.Errorf("health = %+v", h)
	}
	if h := srv.RequestsFor("GET", "/api/v1/health/")[0].Header; h.Get("Authorization") != "" || h.Get("X-API-Key") != "" {
		t.Errorf("health must not send credentials")
	}
}

func TestRequestTimeout(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(200 * time.Millisecond) }))
	t.Cleanup(hs.Close)
	cfg := fastConfig(hs.URL)
	cfg.RequestTimeout = 20 * time.Millisecond
	cfg.NoRetries = true

	if _, err := mustClient(t, cfg).Write(context.Background(), "POST", "/zones/", nil); err == nil {
		t.Fatal("expected a timeout")
	}
}
