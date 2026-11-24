package client

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
)

const (
	DefaultPageSize = 100
	DefaultMaxPages = 100
	paginationVar   = "pagination"
)

type PageOptions struct {
	PageSize  int
	MaxPages  int
	SortBy    string
	SortOrder string
	Name      string
	Var       string
}

type Page[T any] struct {
	Items   []T  `json:"items"`
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
}

func (o PageOptions) withDefaults() PageOptions {
	if o.PageSize <= 0 {
		o.PageSize = DefaultPageSize
	}
	if o.MaxPages <= 0 {
		o.MaxPages = DefaultMaxPages
	}
	if o.Var == "" {
		o.Var = paginationVar
	}
	if o.SortBy != "" && o.SortOrder == "" {
		o.SortOrder = "asc"
	}
	return o
}

func (o PageOptions) pageVars(vars map[string]any, offset int) map[string]any {
	out := make(map[string]any, len(vars)+1)
	maps.Copy(out, vars)
	pagination := map[string]any{}
	if base, ok := vars[o.Var].(map[string]any); ok {
		maps.Copy(pagination, base)
	}
	pagination["limit"] = o.PageSize
	pagination["offset"] = offset
	if o.SortBy != "" {
		pagination["sortBy"] = o.SortBy
		pagination["sortOrder"] = o.SortOrder
	}
	out[o.Var] = pagination
	return out
}

func Paginate[T any](ctx context.Context, c *Client, query string, vars map[string]any, opts PageOptions, extract func(data json.RawMessage) (Page[T], error)) ([]T, error) {
	opts = opts.withDefaults()
	var all []T
	for page := 0; page < opts.MaxPages; page++ {
		var raw json.RawMessage
		if err := c.Query(ctx, query, opts.pageVars(vars, page*opts.PageSize), &raw); err != nil {
			return nil, err
		}
		got, err := extract(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, got.Items...)
		if len(got.Items) == 0 || (!got.HasMore && got.Total <= len(all)) {
			return all, nil
		}
	}
	name := opts.Name
	if name == "" {
		name = "query"
	}
	return nil, &PageOverflowError{Query: name, PageSize: opts.PageSize, MaxPages: opts.MaxPages}
}

func PaginateField[T any](ctx context.Context, c *Client, query, field string, vars map[string]any, opts PageOptions) ([]T, error) {
	if opts.Name == "" {
		opts.Name = field
	}
	return Paginate(ctx, c, query, vars, opts, func(data json.RawMessage) (Page[T], error) {
		var out map[string]Page[T]
		if len(data) == 0 {
			return Page[T]{}, nil
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return Page[T]{}, fmt.Errorf("decode %s page: %w", field, err)
		}
		return out[field], nil
	})
}
