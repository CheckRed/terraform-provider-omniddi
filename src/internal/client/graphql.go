package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Path       []any          `json:"path"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encode graphql request: %w", err)
	}
	resp, err := c.send(ctx, http.MethodPost, c.endpoint+graphQLPath, payload, true)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return apiErrorFrom(http.MethodPost, graphQLPath, resp)
	}
	var parsed graphQLResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return fmt.Errorf("decode graphql response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		gqlErr := &GraphQLError{RequestID: resp.RequestID}
		for _, item := range parsed.Errors {
			code, _ := item.Extensions["code"].(string)
			gqlErr.Errors = append(gqlErr.Errors, GraphQLErrorItem{Message: sanitizeServerText(item.Message), Path: item.Path, Code: sanitizeServerText(code)})
		}
		return gqlErr
	}
	if string(parsed.Data) == "null" {
		return nil
	}
	return decodeInto(parsed.Data, out)
}
