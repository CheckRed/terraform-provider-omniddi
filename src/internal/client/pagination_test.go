package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const thingsQuery = `query Things($pagination: PaginationInput, $scope: String) { things(pagination: $pagination, scope: $scope) { items total hasMore } }`

type thing struct {
	N int `json:"n"`
}

type pagerLog struct {
	mu    sync.Mutex
	pages []map[string]any
}

func (p *pagerLog) record(pagination map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pages = append(p.pages, pagination)
}

func serveThings(srv *fakeserver.Server, total int, reportHasMore bool) *pagerLog {
	log := &pagerLog{}
	srv.HandleGraphQL("things", func(args map[string]any) (any, error) {
		pagination, _ := args["pagination"].(map[string]any)
		log.record(pagination)
		offset, limit := int(pagination["offset"].(float64)), int(pagination["limit"].(float64))
		end := min(offset+limit, total)
		items := []map[string]any{}
		for n := offset; n < end; n++ {
			items = append(items, map[string]any{"n": n})
		}
		return map[string]any{"items": items, "total": total, "hasMore": reportHasMore && end < total}, nil
	})
	return log
}

func noSchema(t *testing.T) (*client.Client, *fakeserver.Server) {
	t.Helper()
	srv := fakeserver.New(t, fakeserver.WithoutSchemaValidation())
	return mustClient(t, fastConfig(srv.URL())), srv
}

func TestPaginateFieldWalksEveryPage(t *testing.T) {
	c, srv := noSchema(t)
	log := serveThings(srv, 250, true)
	vars := map[string]any{"scope": "x", "pagination": map[string]any{"filters": []map[string]any{{"field": "a", "operator": "EQ", "value": 1}}}}

	got, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", vars, client.PageOptions{PageSize: 100, SortBy: "id"})
	if err != nil || len(got) != 250 || got[249].N != 249 {
		t.Fatalf("got %d items, err %v", len(got), err)
	}
	if len(log.pages) != 3 {
		t.Fatalf("pages = %d, want 3", len(log.pages))
	}
	for i, p := range log.pages {
		if p["sortBy"] != "id" || p["sortOrder"] != "asc" || int(p["offset"].(float64)) != i*100 || p["filters"] == nil {
			t.Errorf("page %d pagination = %v", i, p)
		}
	}
	if _, mutated := vars["pagination"].(map[string]any)["limit"]; mutated {
		t.Error("the caller's variables must not be mutated")
	}
}

func TestPaginateFieldWithoutSortAndExactPageBoundary(t *testing.T) {
	c, srv := noSchema(t)
	log := serveThings(srv, 100, true)

	got, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{PageSize: 100})
	if err != nil || len(got) != 100 || len(log.pages) != 1 {
		t.Fatalf("got %d items in %d pages, err %v", len(got), len(log.pages), err)
	}
	if _, sorted := log.pages[0]["sortBy"]; sorted {
		t.Error("sortBy must be omitted unless requested")
	}
}

func TestPaginateFieldFollowsTotalWhenHasMoreIsUnreliable(t *testing.T) {
	c, srv := noSchema(t)
	serveThings(srv, 130, false)

	got, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{PageSize: 100})
	if err != nil || len(got) != 130 {
		t.Fatalf("got %d items, err %v", len(got), err)
	}
}

func TestPaginateFieldErrorsInsteadOfTruncating(t *testing.T) {
	c, srv := noSchema(t)
	serveThings(srv, 1000, true)

	_, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{PageSize: 10, MaxPages: 3})
	var overflow *client.PageOverflowError
	if !errors.As(err, &overflow) || overflow.MaxPages != 3 || overflow.PageSize != 10 || !strings.Contains(err.Error(), "30 items") {
		t.Fatalf("err = %v, want PageOverflowError", err)
	}
}

func TestPaginatePropagatesQueryAndExtractErrors(t *testing.T) {
	c, srv := noSchema(t)
	srv.HandleGraphQL("things", func(map[string]any) (any, error) { return nil, fakeserver.NotFound("gone") })
	if _, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{}); !client.IsNotFound(err) {
		t.Errorf("err = %v", err)
	}

	srv.HandleGraphQL("things", func(map[string]any) (any, error) { return map[string]any{"items": "nope"}, nil })
	boom := errors.New("extract failed")
	_, err := client.Paginate(context.Background(), c, thingsQuery, nil, client.PageOptions{}, func(json.RawMessage) (client.Page[thing], error) {
		return client.Page[thing]{}, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{}); err == nil {
		t.Error("an undecodable page must be an error")
	}
}

func TestPaginateFieldToleratesNullData(t *testing.T) {
	c, srv := noSchema(t)
	srv.HandleGraphQL("things", func(map[string]any) (any, error) { return nil, nil })
	got, err := client.PaginateField[thing](context.Background(), c, thingsQuery, "things", nil, client.PageOptions{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, err %v", got, err)
	}
}

func TestTaskPollingPagesUntilHasMoreIsFalse(t *testing.T) {
	c, srv := noSchema(t)
	const total = 450
	var failedID int64 = 430
	srv.HandleGraphQL("agent_tasks_pagninated", func(args map[string]any) (any, error) {
		pagination := args["pagination"].(map[string]any)
		if pagination["sortBy"] != "id" {
			t.Errorf("task paging must be stable: %v", pagination)
		}
		offset, limit := int(pagination["offset"].(float64)), int(pagination["limit"].(float64))
		end := min(offset+limit, total)
		items := []map[string]any{}
		for id := offset + 1; id <= end; id++ {
			status, logs := "Completed", any(nil)
			if int64(id) == failedID {
				status, logs = "Incomplete", "disk full \x1b[31mERR\x1b[0m"
			}
			items = append(items, map[string]any{"id": id, "status": status, "logs": logs})
		}
		return map[string]any{"items": items, "total": total, "hasMore": end < total}, nil
	})

	err := c.AwaitTasks(context.Background(), "corr-big")
	var failed *client.TaskFailedError
	if !errors.As(err, &failed) || failed.TaskID != failedID || failed.Logs != "disk full ERR" {
		t.Fatalf("err = %v, want the failure on the third page, sanitised", err)
	}
}

func TestTasksByCorrelationOverflowIsAnError(t *testing.T) {
	c, srv := noSchema(t)
	srv.HandleGraphQL("agent_tasks_pagninated", func(args map[string]any) (any, error) {
		pagination := args["pagination"].(map[string]any)
		limit := int(pagination["limit"].(float64))
		items := make([]map[string]any, limit)
		for i := range items {
			items[i] = map[string]any{"id": i, "status": "Completed"}
		}
		return map[string]any{"items": items, "total": 1 << 20, "hasMore": true}, nil
	})
	err := c.AwaitTasks(context.Background(), "corr-endless")
	var overflow *client.PageOverflowError
	if !errors.As(err, &overflow) {
		t.Fatalf("err = %v, want PageOverflowError", err)
	}
}

func TestFindPendingRequestsOverflowIsAnExplicitError(t *testing.T) {
	c, srv := noSchema(t)
	srv.HandleGraphQL("requestsPaginated", func(args map[string]any) (any, error) {
		limit := int(args["pagination"].(map[string]any)["limit"].(float64))
		items := make([]map[string]any, limit)
		for i := range items {
			items[i] = map[string]any{"id": i + 1, "actionType": "create_zone", "status": "pending", "detailsJson": map[string]any{}}
		}
		return map[string]any{"items": items, "total": 1 << 20, "hasMore": true}, nil
	})
	_, err := c.FindPendingRequests(context.Background(), "create_zone", nil)
	var overflow *client.PageOverflowError
	if !errors.As(err, &overflow) {
		t.Fatalf("err = %v, want PageOverflowError", err)
	}
}

func TestFindPendingRequestsPagesAndSorts(t *testing.T) {
	c, srv := newClient(t)
	for i := range 130 {
		srv.StageApproval("create_zone", map[string]any{"zone_data": map[string]any{"name": fmt.Sprintf("z%d.example.com", i)}})
	}
	found, err := c.FindPendingRequests(context.Background(), "create_zone", nil)
	if err != nil || len(found) != 130 {
		t.Fatalf("found %d, err %v", len(found), err)
	}
	for i := 1; i < len(found); i++ {
		if found[i].ID < found[i-1].ID {
			t.Fatal("results must be ordered by id")
		}
	}
}

func TestFindRecentlyAppliedRequestsHonoursTheWindow(t *testing.T) {
	clock := newTestClock()
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.Now = clock.Now
	c := mustClient(t, cfg)

	details := func(name string) map[string]any { return map[string]any{"zone_data": map[string]any{"name": name}} }
	old := srv.StageApproval("create_zone", details("old.example.com"), fakeserver.RequestApplied)
	srv.SetRequestAppliedOn(old, clock.Now().Add(-30*24*time.Hour))
	recent := srv.StageApproval("create_zone", details("new.example.com"), fakeserver.RequestApplied)
	srv.SetRequestAppliedOn(recent, clock.Now().Add(-2*time.Hour))
	srv.StageApproval("create_zone", details("pending.example.com"))
	srv.StageApproval("create_view", details("other-type"), fakeserver.RequestApplied)

	got, err := c.FindRecentlyAppliedRequests(context.Background(), "create_zone", 7*24*time.Hour, nil)
	if err != nil || len(got) != 1 || got[0].ID != recent {
		t.Fatalf("got %+v, err %v; want only request %d", got, err, recent)
	}

	matched, err := c.FindRecentlyAppliedRequests(context.Background(), "create_zone", 365*24*time.Hour, func(d map[string]any) bool {
		return d["zone_data"].(map[string]any)["name"] == "old.example.com"
	})
	if err != nil || len(matched) != 1 || matched[0].ID != old {
		t.Fatalf("matched %+v, err %v", matched, err)
	}
}

func TestOversizedResponseIsAnExplicitError(t *testing.T) {
	hs := httptest.NewServer(withLogin(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pad": "` + strings.Repeat("x", 2048) + `"}`))
	}))
	t.Cleanup(hs.Close)
	cfg := fastConfig(hs.URL)
	cfg.MaxResponseBytes = 1024

	err := mustClient(t, cfg).Get(context.Background(), "/things/", nil, nil)
	var tooLarge *client.ResponseTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Limit != 1024 || !strings.Contains(err.Error(), "response exceeds 1024 bytes") {
		t.Fatalf("err = %v, want ResponseTooLargeError", err)
	}
}

func TestResponseAtTheLimitIsAccepted(t *testing.T) {
	body := `{"ok":true,"pad":"` + strings.Repeat("x", 512) + `"}`
	hs := httptest.NewServer(withLogin(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(hs.Close)
	cfg := fastConfig(hs.URL)
	cfg.MaxResponseBytes = int64(len(body))
	var out struct {
		OK bool `json:"ok"`
	}
	if err := mustClient(t, cfg).Get(context.Background(), "/things/", nil, &out); err != nil || !out.OK {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestNotImplementedIsNotRetried(t *testing.T) {
	c, srv := newClient(t)
	echoHandler(srv, "GET", "/api/v1/things/")
	srv.FailNext("GET", "/api/v1/things/", http.StatusNotImplemented, 5)
	if err := c.Get(context.Background(), "/things/", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 1 {
		t.Errorf("501 must not be retried, got %d requests", got)
	}
}

func TestRetryAfterHTTPDateDelaysTheRetry(t *testing.T) {
	var calls []time.Time
	hs := httptest.NewServer(withLogin(func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, time.Now())
		if len(calls) == 1 {
			w.Header().Set("Retry-After", time.Now().Add(1500*time.Millisecond).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(hs.Close)

	if err := mustClient(t, fastConfig(hs.URL)).Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].Sub(calls[0]) < 400*time.Millisecond {
		t.Errorf("the HTTP-date Retry-After was not honoured: %v", calls)
	}
}

func TestNoRetriesDisablesReadRetries(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.MaxRetries, cfg.NoRetries = 0, true
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")
	srv.FailNext("GET", "/api/v1/things/", http.StatusServiceUnavailable, 5)
	if err := c.Get(context.Background(), "/things/", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if got := len(srv.RequestsFor("GET", "/api/v1/things/")); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestZeroMaxRetriesMeansTheDefault(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.MaxRetries = 0
	c := mustClient(t, cfg)
	echoHandler(srv, "GET", "/api/v1/things/")
	srv.FailNext("GET", "/api/v1/things/", http.StatusServiceUnavailable, 2)
	if err := c.Get(context.Background(), "/things/", nil, nil); err != nil {
		t.Fatalf("the default retries should absorb two failures: %v", err)
	}
}

func TestMaskedContextIsSafeWithoutALogger(t *testing.T) {
	c, _ := newClient(t)
	if c.MaskedContext(context.Background()) == nil || client.MaskSecrets(context.Background(), "", "secret") == nil {
		t.Fatal("masking must always return a usable context")
	}
}

func TestPaginateHonoursACustomVariableName(t *testing.T) {
	c, srv := noSchema(t)
	var seen map[string]any
	srv.HandleGraphQL("things", func(args map[string]any) (any, error) {
		seen, _ = args["page"].(map[string]any)
		return map[string]any{"items": []any{}, "total": 0, "hasMore": false}, nil
	})
	query := `query Things($page: PaginationInput) { things(page: $page) { items total hasMore } }`
	vars := map[string]any{"page": map[string]any{"search": "x"}}
	if _, err := client.PaginateField[thing](context.Background(), c, query, "things", vars, client.PageOptions{Var: "page", PageSize: 7}); err != nil {
		t.Fatal(err)
	}
	if seen["search"] != "x" || seen["limit"] != float64(7) || seen["offset"] != float64(0) {
		t.Errorf("pagination = %v", seen)
	}
}
