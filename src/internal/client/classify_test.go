package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func resp(status int, body string) *Response {
	return &Response{StatusCode: status, Body: []byte(body), RequestID: "rid-1", Header: http.Header{}}
}

func TestClassifyWriteSuccess(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, r *WriteResult)
	}{
		{"202 accepted with tasks", 202,
			`{"zone_id": 5, "task_ids": [11, 12], "correlation_id": "c-1", "sync_task_ids": [13], "sync_correlation_id": "c-2"}`,
			func(t *testing.T, r *WriteResult) {
				if r.CorrelationID != "c-1" || r.SyncCorrelationID != "c-2" || len(r.TaskIDs) != 2 || r.SyncTaskIDs[0] != 13 {
					t.Errorf("unexpected result %+v", r)
				}
				if id, ok := r.Int("view_id", "zone_id"); !ok || id != 5 {
					t.Errorf("Int = %d, %v", id, ok)
				}
				if ids := r.CorrelationIDs(); len(ids) != 2 {
					t.Errorf("CorrelationIDs = %v", ids)
				}
				if r.RequireCorrelationID() != nil {
					t.Errorf("correlation id present")
				}
			}},
		{"202 accepted with null correlation id", 202, `{"task_ids": [], "correlation_id": null}`,
			func(t *testing.T, r *WriteResult) {
				var missing *MissingTaskHandleError
				if !errors.As(r.RequireCorrelationID(), &missing) {
					t.Errorf("expected MissingTaskHandleError")
				}
				if len(r.CorrelationIDs()) != 0 {
					t.Errorf("no correlation ids expected")
				}
			}},
		{"201 synchronous object", 201, `{"id": 9, "name": "acl"}`,
			func(t *testing.T, r *WriteResult) {
				var out struct{ Name string }
				if err := r.Decode(&out); err != nil || out.Name != "acl" {
					t.Errorf("Decode = %+v, %v", out, err)
				}
				if id, _ := r.Int("id"); id != 9 {
					t.Errorf("id = %d", id)
				}
			}},
		{"200 subnet deleted detail", 200, `{"detail": "Subnet deleted"}`,
			func(t *testing.T, r *WriteResult) {
				if r.Detail != "Subnet deleted" {
					t.Errorf("Detail = %q", r.Detail)
				}
			}},
		{"204 empty", 204, ``, func(t *testing.T, r *WriteResult) {
			if r.StatusCode != 204 || len(r.Body) != 0 {
				t.Errorf("unexpected %+v", r)
			}
		}},
		{"202 tasks win over approval wording", 202, `{"detail": "submitted for approval (request ID: 4)", "task_ids": [1], "correlation_id": "c"}`,
			func(t *testing.T, r *WriteResult) {
				if r.CorrelationID != "c" {
					t.Errorf("unexpected %+v", r)
				}
			}},
		{"202 non-approval detail", 202, `{"detail": "queued"}`, func(t *testing.T, r *WriteResult) {
			if r.Detail != "queued" {
				t.Errorf("unexpected %+v", r)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := classifyWrite("POST", "/x/", resp(tc.status, tc.body))
			if err != nil {
				t.Fatalf("classifyWrite: %v", err)
			}
			if r.RequestID != "rid-1" || r.Method != "POST" || r.Path != "/x/" {
				t.Errorf("metadata not carried: %+v", r)
			}
			tc.check(t, r)
		})
	}
}

func TestClassifyApproval(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int64
	}{
		{"request id in detail", `{"detail": "View creation request submitted for approval (request ID: 123)"}`, 123},
		{"across servers", `{"detail": "Zone update request submitted for approval across 3 server(s) (request ID: 456)"}`, 456},
		{"structured body", `{"request_id": 77, "status": "pending", "action_type": "create_zone"}`, 77},
		{"scheduled requests list", `{"requests": [{"id": 88, "status": "pending"}]}`, 88},
		{"lowercase wording", `{"detail": "Subnet creation request submitted (Request Id: 9)"}`, 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := classifyWrite("POST", "/x/", resp(202, tc.body))
			var approval *ApprovalRequiredError
			if !errors.As(err, &approval) || approval.RequestID != tc.want {
				t.Fatalf("err = %v, want ApprovalRequiredError %d", err, tc.want)
			}
			if approval.HTTPRequestID != "rid-1" || approval.Method != "POST" {
				t.Errorf("metadata missing: %+v", approval)
			}
		})
	}
}

func TestClassifyUntrackedApproval(t *testing.T) {
	body := `{"detail": "Zone creation request has been submitted for approval. You can track it on the Change Management Requests page."}`
	_, err := classifyWrite("POST", "/zones/", resp(202, body))
	var untracked *ApprovalUntrackedError
	if !errors.As(err, &untracked) || !strings.Contains(untracked.Detail, "submitted for approval") {
		t.Fatalf("err = %v, want ApprovalUntrackedError", err)
	}
}

func TestClassifyErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		header       http.Header
		wantDetail   string
		wantCode     string
		wantNotFound bool
		wantIssues   int
	}{
		{name: "400 string detail", status: 400, body: `{"detail": "Name must be unique"}`, wantDetail: "Name must be unique"},
		{name: "403", status: 403, body: `{"detail": "Not enough permissions"}`, wantDetail: "Not enough permissions"},
		{name: "404 on a write is an ordinary error", status: 404, body: `{"detail": "Zone not found"}`, wantDetail: "Zone not found"},
		{name: "409", status: 409, body: `{"detail": "Zone already exists"}`, wantDetail: "Zone already exists"},
		{name: "422 with code", status: 422, body: `{"detail": "Unknown filter field", "code": "UNKNOWN_FILTER"}`, wantDetail: "Unknown filter field", wantCode: "UNKNOWN_FILTER"},
		{name: "422 validation list", status: 422,
			body:       `{"detail": [{"loc": ["body", "name"], "msg": "Field required", "type": "missing"}, {"loc": ["body", "ttl", 0], "msg": "bad", "type": "x"}]}`,
			wantDetail: "body.name: Field required; body.ttl.0: bad", wantIssues: 2},
		{name: "500 plain text", status: 500, body: "Internal Server Error", wantDetail: "Internal Server Error"},
		{name: "502 empty body", status: 502, body: "", wantDetail: "Bad Gateway"},
		{name: "detail object", status: 400, body: `{"detail": {"field": "x"}}`, wantDetail: `{"field": "x"}`},
		{name: "redirect", status: 307, body: "", header: http.Header{"Location": {"https://other.example.com/x"}}, wantDetail: "unexpected redirect to other.example.com;"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := resp(tc.status, tc.body)
			if tc.header != nil {
				r.Header = tc.header
			}
			_, err := classifyWrite("PUT", "/zones/1", r)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want APIError", err)
			}
			if !strings.Contains(apiErr.Detail, tc.wantDetail) || apiErr.Code != tc.wantCode {
				t.Errorf("detail=%q code=%q", apiErr.Detail, apiErr.Code)
			}
			if IsNotFound(err) != tc.wantNotFound {
				t.Errorf("IsNotFound = %v", IsNotFound(err))
			}
			if len(apiErr.ValidationIssues) != tc.wantIssues {
				t.Errorf("issues = %d", len(apiErr.ValidationIssues))
			}
			if apiErr.RequestID != "rid-1" || apiErr.Method != "PUT" || apiErr.Path != "/zones/1" || apiErr.StatusCode != tc.status {
				t.Errorf("metadata missing: %+v", apiErr)
			}
			if !strings.Contains(apiErr.Error(), "rid-1") {
				t.Errorf("Error() must include the request id: %s", apiErr.Error())
			}
		})
	}
}

func TestIsNotFoundSemantics(t *testing.T) {
	gql := &GraphQLError{Errors: []GraphQLErrorItem{{Message: "gone", Code: "RESOURCE_NOT_FOUND"}}}
	if !IsNotFound(gql) || !IsNotFound(errors.Join(errors.New("wrap"), gql)) {
		t.Errorf("GraphQL RESOURCE_NOT_FOUND must be not-found")
	}
	if IsNotFound(&GraphQLError{Errors: []GraphQLErrorItem{{Message: "x", Code: "FORBIDDEN"}}}) || IsNotFound(errors.New("x")) || IsNotFound(nil) {
		t.Errorf("only 404s and RESOURCE_NOT_FOUND are not-found")
	}
}

func TestBackoffDelayIsBoundedAndHonorsRetryAfter(t *testing.T) {
	tr := &transport{backoff: Backoff{Min: 10_000_000, Max: 40_000_000}}
	for attempt := range 10 {
		if d := tr.delay(attempt, nil); d <= 0 || d > 40_000_000 {
			t.Fatalf("delay(%d) = %v out of bounds", attempt, d)
		}
	}
	rl := &Response{Header: http.Header{"Retry-After": {"2"}}}
	if d := tr.delay(0, rl); d < 2_000_000_000 {
		t.Errorf("Retry-After ignored: %v", d)
	}
	huge := &Response{Header: http.Header{"Retry-After": {"86400"}}}
	if d := tr.delay(0, huge); d > maxRetryAfter {
		t.Errorf("Retry-After not capped: %v", d)
	}
}
