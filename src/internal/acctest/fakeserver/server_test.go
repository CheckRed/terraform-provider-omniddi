package fakeserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
)

func do(t *testing.T, srv *fakeserver.Server, method, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL()+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func login(t *testing.T, srv *fakeserver.Server) map[string]string {
	t.Helper()
	body := `{"email":"` + fakeserver.DefaultUsername + `","password":"` + fakeserver.DefaultPassword + `"}`
	status, out := do(t, srv, "POST", "/api/v1/users/token", body, nil)
	token, _ := out["access_token"].(string)
	if status != http.StatusOK || token == "" {
		t.Fatalf("login = %d %v", status, out)
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestRoutingPrecedenceAndPathVariables(t *testing.T) {
	srv := fakeserver.New(t)
	srv.HandleREST("GET", "/api/v1/zones/{id}", func(r *fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(200, map[string]any{"id": r.Vars["id"], "source": "first"})
	})
	srv.HandleREST("GET", "/api/v1/zones/{id}", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(200, map[string]any{"source": "override"})
	})
	srv.HandleREST("GET", "/api/v1/zones/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(200, map[string]any{"source": "collection"})
	})
	srv.HandleREST("DELETE", "/api/v1/zones/{id}", func(*fakeserver.Request) fakeserver.Response { return fakeserver.NoContent() })

	if _, body := do(t, srv, "GET", "/api/v1/zones/7", "", login(t, srv)); body["source"] != "override" {
		t.Errorf("later registration must win: %v", body)
	}
	if _, body := do(t, srv, "GET", "/api/v1/zones/", "", login(t, srv)); body["source"] != "collection" {
		t.Errorf("trailing slash must be significant: %v", body)
	}
	if status, _ := do(t, srv, "DELETE", "/api/v1/zones/7", "", login(t, srv)); status != http.StatusNoContent {
		t.Errorf("status = %d", status)
	}
}

func TestAuthIsEnforcedExceptHealth(t *testing.T) {
	srv := fakeserver.New(t)
	srv.HandleREST("GET", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response { return fakeserver.JSON(200, map[string]any{}) })

	if status, _ := do(t, srv, "GET", "/api/v1/health/", "", nil); status != http.StatusOK {
		t.Errorf("health must be public, got %d", status)
	}
	if status, _ := do(t, srv, "GET", "/api/v1/things/", "", nil); status != http.StatusUnauthorized {
		t.Errorf("anonymous = %d", status)
	}
	if status, _ := do(t, srv, "GET", "/api/v1/things/", "", map[string]string{"Authorization": "Bearer not-a-token"}); status != http.StatusUnauthorized {
		t.Errorf("bad token = %d", status)
	}
	if status, _ := do(t, srv, "POST", "/api/v1/users/token", `{"email":"`+fakeserver.DefaultUsername+`","password":"wrong"}`, nil); status != http.StatusUnauthorized {
		t.Errorf("bad password = %d", status)
	}
	if status, _ := do(t, srv, "GET", "/api/v1/things/", "", login(t, srv)); status != http.StatusOK {
		t.Errorf("good token = %d", status)
	}
}

func TestUnhandledRoutesReturn404AndFailureInjection(t *testing.T) {
	srv := fakeserver.New(t)
	srv.HandleREST("GET", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response { return fakeserver.JSON(200, map[string]any{}) })
	srv.FailNext("GET", "/api/v1/things/", 503, 1)

	if status, _ := do(t, srv, "GET", "/api/v1/missing/", "", login(t, srv)); status != http.StatusNotFound {
		t.Errorf("status = %d", status)
	}
	if status, _ := do(t, srv, "GET", "/api/v1/things/", "", login(t, srv)); status != 503 {
		t.Errorf("first call must hit the injected failure, got %d", status)
	}
	if status, _ := do(t, srv, "GET", "/api/v1/things/", "", login(t, srv)); status != 200 {
		t.Errorf("second call must succeed, got %d", status)
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 2 {
		t.Errorf("recorded %d requests", got)
	}
}

func TestRequestHelpers(t *testing.T) {
	srv := fakeserver.New(t)
	var seen map[string]any
	srv.HandleREST("POST", "/api/v1/things/", func(r *fakeserver.Request) fakeserver.Response {
		seen = r.Map()
		var typed struct{ Name string }
		if err := r.JSON(&typed); err != nil || typed.Name != "x" {
			t.Errorf("JSON helper: %v %+v", err, typed)
		}
		return fakeserver.Detail(409, "exists")
	})
	status, body := do(t, srv, "POST", "/api/v1/things/", `{"name":"x"}`, login(t, srv))
	if status != 409 || body["detail"] != "exists" || seen["name"] != "x" {
		t.Errorf("status=%d body=%v seen=%v", status, body, seen)
	}
}
