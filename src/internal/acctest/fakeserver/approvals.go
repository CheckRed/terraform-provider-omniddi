package fakeserver

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	RequestPending   = "pending"
	RequestApproved  = "approved"
	RequestApplied   = "applied"
	RequestRejected  = "rejected"
	RequestWithdrawn = "withdrawn"
)

type fakeRequest struct {
	id         int64
	actionType string
	details    map[string]any
	script     []string
	gets       int
	appliedOn  time.Time
}

type approvalState struct {
	mu       sync.Mutex
	requests map[int64]*fakeRequest
}

func (s *Server) initApprovals() {
	s.approvals.requests = map[int64]*fakeRequest{}
	s.HandleREST(http.MethodGet, "/api/v1/requests/{id}", func(r *Request) Response {
		id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
		status, req := s.nextRequestStatus(id)
		if req == nil {
			return Detail(http.StatusNotFound, "Request not found")
		}
		return JSON(http.StatusOK, map[string]any{
			"id": req.id, "user_id": 1, "action_type": req.actionType, "status": status,
			"details_json": req.details, "submitted_on": "2026-01-01T00:00:00",
		})
	})
	s.HandleGraphQL("requestsPaginated", func(args map[string]any) (any, error) {
		return s.pendingPage(args), nil
	})
}

func (s *Server) StageApproval(actionType string, details map[string]any, statusScript ...string) int64 {
	if len(statusScript) == 0 {
		statusScript = []string{RequestPending}
	}
	id := s.NextID()
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests[id] = &fakeRequest{id: id, actionType: actionType, details: details, script: statusScript}
	return id
}

func (s *Server) SetRequestAppliedOn(id int64, at time.Time) {
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	if req, ok := a.requests[id]; ok {
		req.appliedOn = at
	}
}

func (s *Server) SetRequestStatus(id int64, statuses ...string) {
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	if req, ok := a.requests[id]; ok {
		req.script, req.gets = statuses, 0
	}
}

func (s *Server) ApprovalPending(id int64, text string) Response {
	return Detail(http.StatusAccepted, fmt.Sprintf("%s submitted for approval (request ID: %d)", text, id))
}

func (s *Server) ApprovalUntracked() Response {
	return Detail(http.StatusAccepted, "Zone creation request has been submitted for approval. You can track it on the Change Management Requests page.")
}

func (s *Server) nextRequestStatus(id int64) (string, *fakeRequest) {
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	req, ok := a.requests[id]
	if !ok {
		return "", nil
	}
	status := req.script[min(req.gets, len(req.script)-1)]
	req.gets++
	return status, req
}

func (s *Server) pendingPage(args map[string]any) map[string]any {
	wantStatus := strings.ToLower(fmt.Sprint(args["status"]))
	pagination, _ := args["pagination"].(map[string]any)
	actionType := ""
	filters, _ := pagination["filters"].([]any)
	for _, raw := range filters {
		if f, _ := raw.(map[string]any); f["field"] == "actionType" && f["operator"] == "EQ" {
			actionType, _ = f["value"].(string)
		}
	}
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	matched := []*fakeRequest{}
	for _, req := range a.requests {
		current := req.script[min(req.gets, len(req.script)-1)]
		if current == wantStatus && (actionType == "" || req.actionType == actionType) {
			matched = append(matched, req)
		}
	}
	descending := pagination["sortOrder"] == "desc"
	sort.Slice(matched, func(i, j int) bool {
		if descending {
			return matched[i].id > matched[j].id
		}
		return matched[i].id < matched[j].id
	})
	offset, limit := intArg(pagination["offset"], 0), intArg(pagination["limit"], len(matched))
	start := min(offset, len(matched))
	end := min(start+limit, len(matched))
	items := []map[string]any{}
	for _, req := range matched[start:end] {
		item := map[string]any{
			"id": req.id, "actionType": req.actionType, "status": req.script[min(req.gets, len(req.script)-1)], "detailsJson": req.details,
			"submittedOn": nil, "appliedOn": nil,
		}
		if !req.appliedOn.IsZero() {
			item["appliedOn"] = req.appliedOn.UTC().Format("2006-01-02T15:04:05.000000")
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "total": len(matched), "hasMore": end < len(matched)}
}

func intArg(v any, fallback int) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return fallback
}
