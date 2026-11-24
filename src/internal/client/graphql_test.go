package client_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

func TestGraphQLOperationsMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t,
		client.TasksByCorrelationQuery,
		client.PendingRequestsQuery,
		client.TagDefinitionsForTypeQuery,
		client.TagValuesQuery,
	)
}

func TestGraphQLNotFoundMapsToIsNotFound(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleGraphQL("zoneById", func(map[string]any) (any, error) { return nil, fakeserver.NotFound("Zone not found") })

	err := c.Query(context.Background(), `query { zoneById(id: 1) { id } }`, nil, &struct{}{})
	var gqlErr *client.GraphQLError
	if !errors.As(err, &gqlErr) || !client.IsNotFound(err) {
		t.Fatalf("err = %v, want not-found GraphQLError", err)
	}
	if gqlErr.RequestID == "" || gqlErr.Errors[0].Message != "Zone not found" {
		t.Errorf("unexpected %+v", gqlErr)
	}
}

func TestGraphQLOtherErrorsAreNotNotFound(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleGraphQL("zoneById", func(map[string]any) (any, error) {
		return nil, &fakeserver.GraphQLError{Message: "denied", Code: fakeserver.CodeForbidden}
	})

	err := c.Query(context.Background(), `query { zoneById(id: 1) { id } }`, nil, nil)
	if err == nil || client.IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestGraphQLVariablesAndNullDataAndAliases(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleGraphQL("viewById", func(args map[string]any) (any, error) {
		if id, _ := args["id"].(float64); id == 404 {
			return nil, nil
		}
		return map[string]any{"id": args["id"], "name": "internal"}, nil
	})

	var out struct {
		A *struct {
			Name string `json:"name"`
		} `json:"a"`
		B *struct{ Name string } `json:"b"`
	}
	q := `query Q($id: Int!) { a: viewById(id: $id) { name } b: viewById(id: 404) { name } }`
	if err := c.Query(context.Background(), q, map[string]any{"id": 7}, &out); err != nil {
		t.Fatal(err)
	}
	if out.A == nil || out.A.Name != "internal" || out.B != nil {
		t.Errorf("out = %+v", out)
	}
}

func TestGraphQLSendsBearerForTheConfiguredSpace(t *testing.T) {
	srv := fakeserver.New(t)
	cfg := fastConfig(srv.URL())
	cfg.NetworkSpaceID = 2
	c := mustClient(t, cfg)
	srv.HandleGraphQL("viewById", func(map[string]any) (any, error) { return map[string]any{"id": 1}, nil })
	if err := c.Query(context.Background(), `query { viewById(id: 1) { id } }`, nil, nil); err != nil {
		t.Fatal(err)
	}
	reqs := srv.RequestsFor("POST", "/graphql")
	if len(reqs) != 1 {
		t.Fatalf("expected 1 GraphQL request, got %d", len(reqs))
	}
	if !strings.HasPrefix(reqs[0].Header.Get("Authorization"), "Bearer ") {
		t.Errorf("bearer token not sent on GraphQL")
	}
	if got := srv.RequestSpace(reqs[0]); got != 2 {
		t.Errorf("GraphQL request authenticated in space %d, want 2", got)
	}
}

func TestGraphQLHTTPFailureIsAnAPIError(t *testing.T) {
	c, srv := newClient(t)
	srv.FailNext("POST", "/graphql", http.StatusBadRequest, 1)

	err := c.Query(context.Background(), `query { viewById(id: 1) { id } }`, nil, nil)
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want APIError 400", err)
	}
}
