package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	RequestStatusPending   = "pending"
	RequestStatusApproved  = "approved"
	RequestStatusScheduled = "scheduled"
	RequestStatusApplied   = "applied"
	RequestStatusRejected  = "rejected"
	RequestStatusWithdrawn = "withdrawn"

	approvalPageLimit = 100
	approvalMaxPages  = 50
	appliedMaxPages   = 5
)

const RequestsQuery = `query Requests($pagination: PaginationInput, $status: RequestStatus) {
  requestsPaginated(pagination: $pagination, status: $status) {
    items { id actionType status detailsJson submittedOn appliedOn }
    total
    hasMore
  }
}`

const PendingRequestsQuery = RequestsQuery

type ApprovalRequest struct {
	ID          int64          `json:"id"`
	ActionType  string         `json:"action_type"`
	Status      string         `json:"status"`
	DetailsJSON map[string]any `json:"details_json"`
}

func (c *Client) GetApprovalRequest(ctx context.Context, id int64) (*ApprovalRequest, error) {
	var out ApprovalRequest
	if err := c.Get(ctx, "/requests/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AwaitApproval(ctx context.Context, id int64) (*ApprovalRequest, error) {
	waiting := fmt.Sprintf("approval request %d", id)
	interval := c.apprPoll.InitialInterval
	lastStatus := ""
	for {
		req, err := c.GetApprovalRequest(ctx, id)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, &TimeoutError{Waiting: waiting, RequestID: id, LastStatus: lastStatus, Cause: ctx.Err()}
		case err != nil:
			return nil, fmt.Errorf("poll %s: %w", waiting, err)
		}
		switch strings.ToLower(req.Status) {
		case RequestStatusApplied:
			return req, nil
		case RequestStatusRejected, RequestStatusWithdrawn:
			return nil, &ApprovalRejectedError{RequestID: id, Status: req.Status}
		}
		lastStatus = req.Status
		if err := sleep(ctx, interval); err != nil {
			return nil, &TimeoutError{Waiting: waiting, RequestID: id, LastStatus: lastStatus, Cause: err}
		}
		interval = min(interval*2, c.apprPoll.MaxInterval)
	}
}

type requestRow struct {
	ID          int64           `json:"id"`
	ActionType  string          `json:"actionType"`
	Status      string          `json:"status"`
	DetailsJSON json.RawMessage `json:"detailsJson"`
	SubmittedOn string          `json:"submittedOn"`
	AppliedOn   string          `json:"appliedOn"`
}

func (r requestRow) approval() ApprovalRequest {
	details := map[string]any{}
	_ = json.Unmarshal(r.DetailsJSON, &details)
	return ApprovalRequest{ID: r.ID, ActionType: r.ActionType, Status: r.Status, DetailsJSON: details}
}

func requestVars(status, actionType string) map[string]any {
	return map[string]any{
		"status": status,
		paginationVar: map[string]any{
			"filters": []map[string]any{{"field": "actionType", "operator": "EQ", "value": actionType}},
		},
	}
}

func (c *Client) FindPendingRequests(ctx context.Context, actionType string, match func(details map[string]any) bool) ([]ApprovalRequest, error) {
	opts := PageOptions{PageSize: approvalPageLimit, MaxPages: approvalMaxPages, SortBy: "id", Name: "pending approval requests"}
	rows, err := PaginateField[requestRow](ctx, c, RequestsQuery, "requestsPaginated", requestVars("PENDING", actionType), opts)
	if err != nil {
		return nil, err
	}
	return filterRequests(rows, match), nil
}

func filterRequests(rows []requestRow, match func(map[string]any) bool) []ApprovalRequest {
	var found []ApprovalRequest
	for _, row := range rows {
		if req := row.approval(); match == nil || match(req.DetailsJSON) {
			found = append(found, req)
		}
	}
	return found
}

func (c *Client) FindRecentlyAppliedRequests(ctx context.Context, actionType string, within time.Duration, match func(details map[string]any) bool) ([]ApprovalRequest, error) {
	cutoff := c.now().Add(-within)
	opts := PageOptions{PageSize: approvalPageLimit, SortBy: "id", SortOrder: "desc"}.withDefaults()
	vars := requestVars("APPLIED", actionType)
	var found []ApprovalRequest
	for page := 0; page < appliedMaxPages; page++ {
		var out struct {
			Requests Page[requestRow] `json:"requestsPaginated"`
		}
		if err := c.Query(ctx, RequestsQuery, opts.pageVars(vars, page*opts.PageSize), &out); err != nil {
			return nil, err
		}
		for _, row := range out.Requests.Items {
			if appliedBefore(row, cutoff) {
				return found, nil
			}
			found = append(found, filterRequests([]requestRow{row}, match)...)
		}
		if !out.Requests.HasMore || len(out.Requests.Items) == 0 {
			break
		}
	}
	return found, nil
}

func appliedBefore(row requestRow, cutoff time.Time) bool {
	for _, raw := range []string{row.AppliedOn, row.SubmittedOn} {
		if at, ok := parseServerTime(raw); ok {
			return at.Before(cutoff)
		}
	}
	return false
}

func parseServerTime(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}
