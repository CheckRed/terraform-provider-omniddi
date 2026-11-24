package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var approvalRequestID = regexp.MustCompile(`(?i)request\s*id:?\s*(\d+)`)

type WriteResult struct {
	StatusCode        int
	Body              json.RawMessage
	Detail            string
	TaskIDs           []int64
	SyncTaskIDs       []int64
	CorrelationID     string
	SyncCorrelationID string
	RequestID         string
	Method            string
	Path              string
	fields            map[string]json.RawMessage
}

func (r *WriteResult) Decode(out any) error {
	if r == nil {
		return nil
	}
	return decodeInto(r.Body, out)
}

func (r *WriteResult) Int(keys ...string) (int64, bool) {
	if r == nil {
		return 0, false
	}
	for _, key := range keys {
		raw, ok := r.fields[key]
		if !ok {
			continue
		}
		var n int64
		if err := json.Unmarshal(raw, &n); err == nil && n != 0 {
			return n, true
		}
	}
	return 0, false
}

func (r *WriteResult) CorrelationIDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, 2)
	for _, id := range []string{r.CorrelationID, r.SyncCorrelationID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *WriteResult) RequireCorrelationID() error {
	if r.CorrelationID == "" {
		return &MissingTaskHandleError{Method: r.Method, Path: r.Path}
	}
	return nil
}

func classifyWrite(method, path string, resp *Response) (*WriteResult, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, apiErrorFrom(method, path, resp)
	}
	result := &WriteResult{
		StatusCode: resp.StatusCode,
		Body:       json.RawMessage(bytes.Clone(resp.Body)),
		RequestID:  resp.RequestID,
		Method:     method,
		Path:       path,
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &fields); err == nil {
		result.fields = fields
		result.Detail = sanitizeServerText(stringField(fields, "detail"))
		result.TaskIDs = int64List(fields["task_ids"])
		result.SyncTaskIDs = int64List(fields["sync_task_ids"])
		result.CorrelationID = stringField(fields, "correlation_id")
		result.SyncCorrelationID = stringField(fields, "sync_correlation_id")
	}
	if resp.StatusCode == http.StatusAccepted {
		if err := approvalFrom(result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func approvalFrom(r *WriteResult) error {
	if len(r.TaskIDs) > 0 || r.CorrelationID != "" {
		return nil
	}
	if id := structuredApprovalID(r); id != 0 {
		return &ApprovalRequiredError{RequestID: id, Detail: r.Detail, Method: r.Method, Path: r.Path, HTTPRequestID: r.RequestID}
	}
	if m := approvalRequestID.FindStringSubmatch(r.Detail); m != nil {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return &ApprovalRequiredError{RequestID: id, Detail: r.Detail, Method: r.Method, Path: r.Path, HTTPRequestID: r.RequestID}
		}
	}
	if strings.Contains(strings.ToLower(r.Detail), "approval") {
		return &ApprovalUntrackedError{Detail: r.Detail, Method: r.Method, Path: r.Path, HTTPRequestID: r.RequestID}
	}
	return nil
}

func structuredApprovalID(r *WriteResult) int64 {
	if id, ok := r.Int("request_id"); ok {
		return id
	}
	var queued []struct {
		ID int64 `json:"id"`
	}
	if raw, ok := r.fields["requests"]; ok && json.Unmarshal(raw, &queued) == nil && len(queued) > 0 {
		return queued[0].ID
	}
	return 0
}

func stringField(fields map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := fields[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func int64List(raw json.RawMessage) []int64 {
	var ids []int64
	if len(raw) == 0 || json.Unmarshal(raw, &ids) != nil {
		return nil
	}
	return ids
}

func apiErrorFrom(method, path string, resp *Response) *APIError {
	apiErr := &APIError{StatusCode: resp.StatusCode, RequestID: resp.RequestID, Method: method, Path: path}
	var body struct {
		Detail json.RawMessage `json:"detail"`
		Code   string          `json:"code"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.Detail == nil && body.Code == "" {
		apiErr.Detail = unstructuredDetail(resp)
		return apiErr
	}
	apiErr.structured = true
	apiErr.Code = sanitizeServerText(body.Code)
	var text string
	var issues []struct {
		Loc  []any  `json:"loc"`
		Msg  string `json:"msg"`
		Type string `json:"type"`
	}
	switch {
	case json.Unmarshal(body.Detail, &text) == nil && text != "":
		apiErr.Detail = sanitizeServerText(text)
		apiErr.structured = text != unknownRouteDetail
	case json.Unmarshal(body.Detail, &issues) == nil && len(issues) > 0:
		parts := make([]string, 0, len(issues))
		for _, issue := range issues {
			vi := ValidationIssue{Msg: sanitizeServerText(issue.Msg), Type: sanitizeServerText(issue.Type)}
			for _, l := range issue.Loc {
				vi.Loc = append(vi.Loc, sanitizeServerText(fmt.Sprint(l)))
			}
			apiErr.ValidationIssues = append(apiErr.ValidationIssues, vi)
			parts = append(parts, vi.String())
		}
		apiErr.Detail = sanitizeServerText(strings.Join(parts, "; "))
	case len(body.Detail) > 0 && string(body.Detail) != "null":
		apiErr.Detail = sanitizeServerText(string(body.Detail))
	default:
		apiErr.Detail = http.StatusText(resp.StatusCode)
	}
	return apiErr
}

// FastAPI answers an unmatched route with this detail; it never means the object is missing.
const unknownRouteDetail = "Not Found"

func unstructuredDetail(resp *Response) string {
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Sprintf("unexpected redirect to %s; check that the endpoint URL is exact", redirectHost(resp.Header.Get("Location")))
	}
	if text := sanitizeServerText(string(resp.Body)); text != "" {
		return text
	}
	return http.StatusText(resp.StatusCode)
}
