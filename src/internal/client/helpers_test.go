package client_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock { return &testClock{now: time.Unix(1_800_000_000, 0)} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func fastConfig(endpoint string) client.Config {
	return client.Config{
		Endpoint:       endpoint,
		Username:       fakeserver.DefaultUsername,
		Password:       fakeserver.DefaultPassword,
		MaxRetries:     3,
		RetryBackoff:   client.Backoff{Min: time.Millisecond, Max: 4 * time.Millisecond},
		TaskPoll:       client.PollConfig{InitialInterval: time.Millisecond, MaxInterval: 4 * time.Millisecond, EmptyGrace: 60 * time.Millisecond},
		ApprovalPoll:   client.PollConfig{InitialInterval: time.Millisecond, MaxInterval: 4 * time.Millisecond},
		RequestTimeout: 5 * time.Second,
	}
}

func mustClient(t *testing.T, cfg client.Config) *client.Client {
	t.Helper()
	c, err := client.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

func newClient(t *testing.T) (*client.Client, *fakeserver.Server) {
	t.Helper()
	srv := fakeserver.New(t)
	return mustClient(t, fastConfig(srv.URL())), srv
}

func echoHandler(srv *fakeserver.Server, method, path string) {
	srv.HandleREST(method, path, func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(200, map[string]any{"ok": true})
	})
}

func syntheticJWT(space int64, exp time.Time) string {
	enc := base64.RawURLEncoding
	payload := `{"sub":"1","ns":` + strconv.FormatInt(space, 10) + `,"exp":` + strconv.FormatInt(exp.Unix(), 10) + `}`
	return enc.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString([]byte("sig"))
}

// withLogin answers the login route so a bare test server only sees API calls.
func withLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/users/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + syntheticJWT(fakeserver.DefaultSpaceID, time.Now().Add(time.Hour)) + `"}`))
			return
		}
		next(w, r)
	}
}
