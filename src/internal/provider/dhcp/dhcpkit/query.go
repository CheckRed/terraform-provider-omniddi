package dhcpkit

import (
	"context"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func EqFilter(field string, value any) map[string]any {
	return map[string]any{"field": field, "operator": "EQ", "value": value}
}

func paginationVars(vars map[string]any, filters []map[string]any) map[string]any {
	out := make(map[string]any, len(vars)+1)
	for k, v := range vars {
		out[k] = v
	}
	if len(filters) > 0 {
		out["pagination"] = map[string]any{"filters": filters}
	}
	return out
}

func FetchAll[T any](ctx context.Context, c *client.Client, query, field string, vars map[string]any, filters []map[string]any) ([]T, error) {
	return client.PaginateField[T](ctx, c, query, field, paginationVars(vars, filters), client.PageOptions{SortBy: "id"})
}
