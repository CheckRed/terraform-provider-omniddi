package client_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func TestAwaitTasksSucceedsAfterProgress(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTaskScript("", fakeserver.StatusQueued, fakeserver.StatusInprogress, fakeserver.StatusCompleted)

	if err := c.AwaitTasks(context.Background(), h.CorrelationID); err != nil {
		t.Fatalf("AwaitTasks: %v", err)
	}
	if polls := srv.TaskPolls(h.CorrelationID); polls != 3 {
		t.Errorf("polls = %d, want 3", polls)
	}
}

func TestAwaitTasksWaitsForEveryTask(t *testing.T) {
	c, srv := newClient(t)
	done := srv.StageTasks(fakeserver.StatusCompleted)
	slow := srv.StageTaskScript("", fakeserver.StatusQueued, fakeserver.StatusCompleted)

	if err := c.AwaitTasks(context.Background(), done.CorrelationID, slow.CorrelationID); err != nil {
		t.Fatalf("AwaitTasks: %v", err)
	}
	if srv.TaskPolls(slow.CorrelationID) != 2 {
		t.Errorf("second correlation id was not awaited to completion")
	}
}

func TestAwaitTasksReportsFailureWithTaskIDAndLogs(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTaskScript("zone file syntax error on line 4", fakeserver.StatusInprogress, fakeserver.StatusIncomplete)

	err := c.AwaitTasks(context.Background(), h.CorrelationID)
	var failed *client.TaskFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want TaskFailedError", err)
	}
	if failed.TaskID != h.TaskIDs[0] || failed.CorrelationID != h.CorrelationID || failed.Logs != "zone file syntax error on line 4" {
		t.Errorf("unexpected %+v", failed)
	}
}

func TestAwaitWriteReportsAFailedFollowUpSyncSeparately(t *testing.T) {
	c, srv := newClient(t)
	main := srv.StageTasks(fakeserver.StatusCompleted)
	sync := srv.StageTaskScript("zone not found", fakeserver.StatusIncomplete)

	err := c.AwaitWrite(context.Background(), &client.WriteResult{CorrelationID: main.CorrelationID, SyncCorrelationID: sync.CorrelationID})
	var syncErr *client.SyncTaskError
	var failed *client.TaskFailedError
	if !errors.As(err, &syncErr) || !errors.As(err, &failed) || failed.CorrelationID != sync.CorrelationID {
		t.Fatalf("err = %v, want SyncTaskError wrapping the sync task failure", err)
	}

	broken := srv.StageTaskScript("bad zone", fakeserver.StatusIncomplete)
	err = c.AwaitWrite(context.Background(), &client.WriteResult{CorrelationID: broken.CorrelationID, SyncCorrelationID: sync.CorrelationID})
	if errors.As(err, &syncErr) || !errors.As(err, &failed) {
		t.Fatalf("a failure of the write's own tasks must not be a SyncTaskError, got %v", err)
	}
}

func TestAwaitTasksFailsFastWhenOneOfManyFails(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTasks(fakeserver.StatusQueued, fakeserver.StatusIncomplete)

	var failed *client.TaskFailedError
	if err := c.AwaitTasks(context.Background(), h.CorrelationID); !errors.As(err, &failed) {
		t.Fatalf("err = %v, want TaskFailedError", err)
	}
}

func TestAwaitTasksTimeoutNamesCorrelationID(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTasks(fakeserver.StatusInprogress)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := c.AwaitTasks(ctx, h.CorrelationID)
	var timeout *client.TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
	if timeout.CorrelationID != h.CorrelationID || !strings.Contains(timeout.Error(), h.CorrelationID) || !strings.Contains(timeout.LastStatus, "Inprogress") {
		t.Errorf("unexpected %+v", timeout)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !client.IsTimeout(err) {
		t.Errorf("timeout must unwrap to the context error")
	}
}

func TestAwaitTasksCancellationIsATimeoutNotSuccess(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTasks(fakeserver.StatusQueued)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.AwaitTasks(ctx, h.CorrelationID); !client.IsTimeout(err) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
}

func TestAwaitTasksToleratesEmptyResultsDuringGrace(t *testing.T) {
	c, srv := newClient(t)
	h := srv.StageTasksAfterEmptyPolls(3, fakeserver.StatusCompleted)

	if err := c.AwaitTasks(context.Background(), h.CorrelationID); err != nil {
		t.Fatalf("AwaitTasks: %v", err)
	}
}

func TestAwaitTasksEmptyAfterGraceIsAnError(t *testing.T) {
	c, _ := newClient(t)

	err := c.AwaitTasks(context.Background(), "corr-that-never-appears")
	var none *client.NoTasksError
	if !errors.As(err, &none) || none.CorrelationID != "corr-that-never-appears" {
		t.Fatalf("err = %v, want NoTasksError", err)
	}
}

func TestAwaitTasksWithoutIDsIsANoOp(t *testing.T) {
	c, srv := newClient(t)
	if err := c.AwaitTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := &client.WriteResult{}
	if err := c.AwaitWrite(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	if len(srv.Requests()) != 0 {
		t.Errorf("no requests expected, got %d", len(srv.Requests()))
	}
}

func TestAwaitWriteAwaitsBothCorrelationIDs(t *testing.T) {
	c, srv := newClient(t)
	main := srv.StageTasks(fakeserver.StatusCompleted)
	sync := srv.StageTasks(fakeserver.StatusCompleted)
	srv.HandleREST("POST", "/api/v1/zones/", func(*fakeserver.Request) fakeserver.Response {
		body := main.Merge(map[string]any{"zone_id": 3, "sync_task_ids": sync.TaskIDs, "sync_correlation_id": sync.CorrelationID})
		return fakeserver.JSON(202, body)
	})
	res, err := c.Write(context.Background(), "POST", "/zones/", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AwaitWrite(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	if srv.TaskPolls(main.CorrelationID) == 0 || srv.TaskPolls(sync.CorrelationID) == 0 {
		t.Errorf("both correlation ids must be polled")
	}
}

func TestPollingPermissionErrorIsSurfaced(t *testing.T) {
	c, srv := newClient(t)
	srv.HandleGraphQL("agent_tasks_pagninated", func(map[string]any) (any, error) {
		return nil, &fakeserver.GraphQLError{Message: "Permission denied: read_servers", Code: fakeserver.CodeForbidden}
	})

	err := c.AwaitTasks(context.Background(), "c-1")
	var gqlErr *client.GraphQLError
	if !errors.As(err, &gqlErr) || !strings.Contains(err.Error(), "read_servers") {
		t.Fatalf("err = %v, want a GraphQLError mentioning read_servers", err)
	}
}
