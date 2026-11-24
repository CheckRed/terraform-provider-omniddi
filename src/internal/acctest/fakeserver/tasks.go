package fakeserver

import (
	"fmt"
	"sync"
)

const (
	StatusCompleted  = "Completed"
	StatusQueued     = "Queued"
	StatusInprogress = "Inprogress"
	StatusIncomplete = "Incomplete"
)

type TaskHandle struct {
	CorrelationID string
	TaskIDs       []int64
}

func (h TaskHandle) Merge(extra map[string]any) map[string]any {
	body := map[string]any{"task_ids": h.TaskIDs, "correlation_id": h.CorrelationID}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

type fakeTask struct {
	id            int64
	correlationID string
	script        []string
	logs          string
	polls         int
	hiddenPolls   int
}

type taskState struct {
	mu    sync.Mutex
	tasks []*fakeTask
	seq   int
}

func (s *Server) initTasks() {
	s.HandleGraphQL("agent_tasks_pagninated", func(args map[string]any) (any, error) {
		return s.taskPage(correlationFilter(args)), nil
	})
}

func (s *Server) StageTasks(statuses ...string) TaskHandle {
	scripts := make([][]string, len(statuses))
	for i, st := range statuses {
		scripts[i] = []string{st}
	}
	return s.stage("", 0, scripts...)
}

func (s *Server) StageTaskScript(logs string, script ...string) TaskHandle {
	return s.stage(logs, 0, script)
}

func (s *Server) StageTasksAfterEmptyPolls(emptyPolls int, statuses ...string) TaskHandle {
	scripts := make([][]string, len(statuses))
	for i, st := range statuses {
		scripts[i] = []string{st}
	}
	return s.stage("", emptyPolls, scripts...)
}

func (s *Server) stage(logs string, emptyPolls int, scripts ...[]string) TaskHandle {
	ts := &s.tasks
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.seq++
	handle := TaskHandle{CorrelationID: fmt.Sprintf("corr-%d", ts.seq)}
	for _, script := range scripts {
		id := s.NextID()
		ts.tasks = append(ts.tasks, &fakeTask{id: id, correlationID: handle.CorrelationID, script: script, logs: logs, hiddenPolls: emptyPolls})
		handle.TaskIDs = append(handle.TaskIDs, id)
	}
	return handle
}

func (s *Server) AcceptedWithTasks(extra map[string]any, statuses ...string) Response {
	return JSON(202, s.StageTasks(statuses...).Merge(extra))
}

func (s *Server) TaskPolls(correlationID string) int {
	ts := &s.tasks
	ts.mu.Lock()
	defer ts.mu.Unlock()
	polls := 0
	for _, t := range ts.tasks {
		if t.correlationID == correlationID {
			polls = max(polls, t.polls)
		}
	}
	return polls
}

func correlationFilter(args map[string]any) string {
	pagination, _ := args["pagination"].(map[string]any)
	filters, _ := pagination["filters"].([]any)
	for _, raw := range filters {
		f, _ := raw.(map[string]any)
		if f["field"] == "correlationId" && f["operator"] == "EQ" {
			value, _ := f["value"].(string)
			return value
		}
	}
	return ""
}

func (s *Server) taskPage(correlationID string) map[string]any {
	ts := &s.tasks
	ts.mu.Lock()
	defer ts.mu.Unlock()
	items := []map[string]any{}
	for _, t := range ts.tasks {
		if t.correlationID != correlationID {
			continue
		}
		t.polls++
		if t.polls <= t.hiddenPolls {
			continue
		}
		step := min(t.polls-t.hiddenPolls-1, len(t.script)-1)
		status := t.script[step]
		item := map[string]any{"id": t.id, "status": status, "logs": nil}
		if status == StatusIncomplete {
			item["logs"] = t.logs
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "total": len(items), "hasMore": false}
}
