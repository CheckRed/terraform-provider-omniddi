package dhcpkit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

const rowsQuery = `query Rows($pagination: PaginationInput, $subnetId: Int) { rows(pagination: $pagination, subnetId: $subnetId) { items hasMore } }`

type row struct {
	ID int64 `json:"id"`
}

type rowLog struct {
	mu    sync.Mutex
	pages []map[string]any
	vars  []map[string]any
}

func serveRows(srv *fakeserver.Server, total int) *rowLog {
	log := &rowLog{}
	srv.HandleGraphQL("rows", func(args map[string]any) (any, error) {
		pagination, _ := args["pagination"].(map[string]any)
		log.mu.Lock()
		log.pages = append(log.pages, pagination)
		log.vars = append(log.vars, args)
		log.mu.Unlock()
		offset, limit := int(pagination["offset"].(float64)), int(pagination["limit"].(float64))
		end := min(offset+limit, total)
		items := []map[string]any{}
		for n := offset; n < end; n++ {
			items = append(items, map[string]any{"id": n + 1})
		}
		return map[string]any{"items": items, "hasMore": end < total}, nil
	})
	return log
}

func newClient(t *testing.T) (*client.Client, *fakeserver.Server) {
	t.Helper()
	srv := fakeserver.New(t, fakeserver.WithoutSchemaValidation())
	c, err := client.New(context.Background(), client.Config{
		Endpoint:       srv.URL(),
		Username:       fakeserver.DefaultUsername,
		Password:       fakeserver.DefaultPassword,
		MaxRetries:     1,
		RetryBackoff:   client.Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond},
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c, srv
}

func TestFetchAllWalksEveryPageWithFiltersAndSort(t *testing.T) {
	c, srv := newClient(t)
	log := serveRows(srv, 250)
	filters := []map[string]any{dhcpkit.EqFilter("name", "a")}

	got, err := dhcpkit.FetchAll[row](context.Background(), c, rowsQuery, "rows", map[string]any{"subnetId": 4}, filters)
	if err != nil || len(got) != 250 || got[249].ID != 250 {
		t.Fatalf("got %d rows, err %v", len(got), err)
	}
	if len(log.pages) != 3 {
		t.Fatalf("pages = %d, want 3", len(log.pages))
	}
	for i, p := range log.pages {
		if p["sortBy"] != "id" || p["filters"] == nil || int(p["offset"].(float64)) != i*client.DefaultPageSize {
			t.Errorf("page %d pagination = %v", i, p)
		}
		if log.vars[i]["subnetId"] != float64(4) {
			t.Errorf("page %d lost the extra variable: %v", i, log.vars[i])
		}
	}
	if len(filters) != 1 {
		t.Error("the caller's filters must not be modified")
	}
}

func TestFetchAllWithoutFiltersSendsNoFilterKey(t *testing.T) {
	c, srv := newClient(t)
	log := serveRows(srv, 3)
	if got, err := dhcpkit.FetchAll[row](context.Background(), c, rowsQuery, "rows", nil, nil); err != nil || len(got) != 3 {
		t.Fatalf("got %d rows, err %v", len(got), err)
	}
	if _, has := log.pages[0]["filters"]; has {
		t.Errorf("filters sent although none were given: %v", log.pages[0])
	}
}

func TestFetchAllErrorsInsteadOfTruncating(t *testing.T) {
	c, srv := newClient(t)
	serveRows(srv, client.DefaultPageSize*client.DefaultMaxPages+1)

	_, err := dhcpkit.FetchAll[row](context.Background(), c, rowsQuery, "rows", nil, nil)
	var overflow *client.PageOverflowError
	if !errors.As(err, &overflow) {
		t.Fatalf("err = %v, want a PageOverflowError", err)
	}
}

func TestRecoverByMapsNotFoundToAbsent(t *testing.T) {
	ctx := context.Background()
	found := dhcpkit.RecoverBy(func(context.Context) (int64, error) { return 7, nil })
	if id, ok, err := found(ctx); id != 7 || !ok || err != nil {
		t.Errorf("found = %d %v %v", id, ok, err)
	}
	missing := dhcpkit.RecoverBy(func(context.Context) (int64, error) { return 0, client.NewNotFound("thing") })
	if id, ok, err := missing(ctx); id != 0 || ok || err != nil {
		t.Errorf("missing = %d %v %v", id, ok, err)
	}
	boom := errors.New("boom")
	failing := dhcpkit.RecoverBy(func(context.Context) (int64, error) { return 0, boom })
	if _, ok, err := failing(ctx); ok || !errors.Is(err, boom) {
		t.Errorf("failing = %v %v", ok, err)
	}
}
