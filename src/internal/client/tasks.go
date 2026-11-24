package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	taskStatusCompleted  = "Completed"
	taskStatusIncomplete = "Incomplete"
	taskPageLimit        = 200
	taskMaxPages         = 10
)

const TasksByCorrelationQuery = `query AgentTasksByCorrelation($pagination: PaginationInput) {
  agent_tasks_pagninated(pagination: $pagination) {
    items { id status logs }
    total
    hasMore
  }
}`

type AgentTask struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Logs   string `json:"logs"`
}

func (c *Client) TasksByCorrelation(ctx context.Context, correlationID string) ([]AgentTask, error) {
	vars := map[string]any{paginationVar: map[string]any{
		"filters": []map[string]any{{"field": "correlationId", "operator": "EQ", "value": correlationID}},
	}}
	opts := PageOptions{PageSize: taskPageLimit, MaxPages: taskMaxPages, SortBy: "id", Name: "agent tasks for correlation id " + correlationID}
	tasks, err := PaginateField[AgentTask](ctx, c, TasksByCorrelationQuery, "agent_tasks_pagninated", vars, opts)
	for i := range tasks {
		tasks[i].Logs = sanitizeServerLogs(tasks[i].Logs)
	}
	return tasks, err
}

// The follow-up sync is best effort on the server ("never blocks the create"), so its failure is a *SyncTaskError.
func (c *Client) AwaitWrite(ctx context.Context, result *WriteResult) error {
	if result == nil {
		return nil
	}
	if result.CorrelationID != "" {
		if err := c.awaitCorrelation(ctx, result.CorrelationID); err != nil {
			return err
		}
	}
	if result.SyncCorrelationID != "" {
		if err := c.awaitCorrelation(ctx, result.SyncCorrelationID); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return &SyncTaskError{Err: err}
		}
	}
	return nil
}

func (c *Client) AwaitTasks(ctx context.Context, correlationIDs ...string) error {
	for _, id := range correlationIDs {
		if err := c.awaitCorrelation(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) awaitCorrelation(ctx context.Context, correlationID string) error {
	waiting := "agent tasks for correlation id " + correlationID
	started := time.Now()
	interval := c.taskPoll.InitialInterval
	lastStatus := ""
	for {
		tasks, err := c.TasksByCorrelation(ctx, correlationID)
		switch {
		case err != nil && ctx.Err() != nil:
			return &TimeoutError{Waiting: waiting, CorrelationID: correlationID, LastStatus: lastStatus, Cause: ctx.Err()}
		case err != nil:
			return fmt.Errorf("poll %s: %w", waiting, err)
		}

		verdict, failed := evaluateTasks(tasks)
		switch {
		case failed != nil:
			return &TaskFailedError{CorrelationID: correlationID, TaskID: failed.ID, Logs: failed.Logs}
		case verdict == verdictDone:
			return nil
		case verdict == verdictEmpty && time.Since(started) >= c.taskPoll.EmptyGrace:
			return &NoTasksError{CorrelationID: correlationID}
		}
		lastStatus = summarize(tasks)

		if err := sleep(ctx, interval); err != nil {
			return &TimeoutError{Waiting: waiting, CorrelationID: correlationID, LastStatus: lastStatus, Cause: err}
		}
		interval = min(interval*2, c.taskPoll.MaxInterval)
	}
}

type verdict int

const (
	verdictPending verdict = iota
	verdictEmpty
	verdictDone
)

func evaluateTasks(tasks []AgentTask) (verdict, *AgentTask) {
	if len(tasks) == 0 {
		return verdictEmpty, nil
	}
	allDone := true
	for i := range tasks {
		switch tasks[i].Status {
		case taskStatusIncomplete:
			return verdictPending, &tasks[i]
		case taskStatusCompleted:
		default:
			allDone = false
		}
	}
	if allDone {
		return verdictDone, nil
	}
	return verdictPending, nil
}

func summarize(tasks []AgentTask) string {
	if len(tasks) == 0 {
		return "no tasks visible yet"
	}
	parts := make([]string, 0, len(tasks))
	for _, t := range tasks {
		parts = append(parts, fmt.Sprintf("task %d %s", t.ID, t.Status))
	}
	return strings.Join(parts, ", ")
}

func IsTimeout(err error) bool {
	var te *TimeoutError
	return errors.As(err, &te)
}
