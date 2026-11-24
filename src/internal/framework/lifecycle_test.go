package framework_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func lifecycleFixture(t *testing.T, mode framework.ApprovalMode) (*fakeserver.Server, framework.Lifecycle) {
	t.Helper()
	srv := fakeserver.New(t)
	cfg := client.Config{
		Endpoint:     srv.URL(),
		Username:     fakeserver.DefaultUsername,
		Password:     fakeserver.DefaultPassword,
		MaxRetries:   1,
		RetryBackoff: client.Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond},
		TaskPoll:     client.PollConfig{InitialInterval: time.Millisecond, MaxInterval: 2 * time.Millisecond, EmptyGrace: 30 * time.Millisecond},
		ApprovalPoll: client.PollConfig{InitialInterval: time.Millisecond, MaxInterval: 2 * time.Millisecond},
	}
	c, err := client.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	pd := &framework.ProviderData{Client: c, ApprovalMode: mode}
	return srv, framework.NewLifecycle(pd, types.StringNull())
}

func matchName(name string) func(map[string]any) bool {
	return func(d map[string]any) bool { return d["name"] == name }
}

var createSpec = framework.WriteSpec{Method: http.MethodPost, Path: "/things/", Body: map[string]any{"name": "x"}, ActionType: "create_thing"}

func TestLifecycleWriteAppliesImmediately(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 4}, fakeserver.StatusCompleted)
	})
	spec := createSpec
	spec.Match = matchName("x")

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ViaApproval() {
		t.Errorf("a direct write is not via approval")
	}
	if id, _ := outcome.Result.Int("thing_id"); id != 4 {
		t.Errorf("id = %d", id)
	}
	if err := lc.Converge(context.Background(), outcome); err != nil {
		t.Errorf("Converge: %v", err)
	}
}

func TestLifecycleFailModeSurfacesApproval(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.ApprovalPending(55, "Thing creation request")
	})

	_, err := lc.Write(context.Background(), createSpec)
	var approval *client.ApprovalRequiredError
	if !errors.As(err, &approval) || approval.RequestID != 55 {
		t.Fatalf("err = %v, want ApprovalRequiredError 55", err)
	}
}

func TestLifecycleWaitModeAwaitsAndReportsViaApproval(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
	id := srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestPending, fakeserver.RequestApplied)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.ApprovalPending(id, "Thing creation request")
	})

	outcome, err := lc.Write(context.Background(), createSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.ViaApproval() || outcome.ApprovalRequestID != id {
		t.Errorf("outcome = %+v", outcome)
	}
	if err := lc.Converge(context.Background(), outcome); err != nil {
		t.Errorf("Converge on an approval outcome is a no-op: %v", err)
	}
}

func TestLifecycleWaitModeRejectedIsAnError(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
	id := srv.StageApproval("create_thing", nil, fakeserver.RequestPending, fakeserver.RequestRejected)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response { return srv.ApprovalPending(id, "x") })

	_, err := lc.Write(context.Background(), createSpec)
	var rejected *client.ApprovalRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want ApprovalRejectedError", err)
	}
}

func TestLifecycleUntrackedApproval(t *testing.T) {
	t.Run("fail mode errors", func(t *testing.T) {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
		srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response { return srv.ApprovalUntracked() })
		_, err := lc.Write(context.Background(), createSpec)
		var untracked *client.ApprovalUntrackedError
		if !errors.As(err, &untracked) {
			t.Fatalf("err = %v, want ApprovalUntrackedError", err)
		}
	})
	t.Run("wait mode finds the request by its details", func(t *testing.T) {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
		var filed int64
		srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
			filed = srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestPending, fakeserver.RequestApplied)
			return srv.ApprovalUntracked()
		})
		spec := createSpec
		spec.Match = matchName("x")

		outcome, err := lc.Write(context.Background(), spec)
		if err != nil || !outcome.ViaApproval() || outcome.ApprovalRequestID != filed {
			t.Fatalf("outcome = %+v, err = %v, want request %d", outcome, err, filed)
		}
	})
	t.Run("wait mode without a matcher cannot track it", func(t *testing.T) {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
		srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response { return srv.ApprovalUntracked() })
		if _, err := lc.Write(context.Background(), createSpec); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestLifecycleDuplicatePreventionRunsBeforeTheWrite(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	pending := srv.StageApproval("create_thing", map[string]any{"name": "x"})
	srv.StageApproval("create_thing", map[string]any{"name": "other"})
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		t.Error("POST must not be sent while an identical request is pending")
		return fakeserver.Detail(500, "unexpected")
	})
	spec := createSpec
	spec.Match = matchName("x")

	_, err := lc.Write(context.Background(), spec)
	var approval *client.ApprovalRequiredError
	if !errors.As(err, &approval) || approval.RequestID != pending {
		t.Fatalf("err = %v, want ApprovalRequiredError %d", err, pending)
	}
}

func TestLifecycleLookupFailureDoesNotBlockWrites(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleGraphQL("requestsPaginated", func(map[string]any) (any, error) {
		return nil, &fakeserver.GraphQLError{Message: "not Authorized!", Code: fakeserver.CodeForbidden}
	})
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 1}, fakeserver.StatusCompleted)
	})
	spec := createSpec
	spec.Match = matchName("x")

	if _, err := lc.Write(context.Background(), spec); err != nil {
		t.Fatalf("a failed duplicate lookup must not block the write: %v", err)
	}
}

func TestLifecycleConvergeReportsTaskFailure(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	h := srv.StageTaskScript("boom", fakeserver.StatusIncomplete)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(202, h.Merge(map[string]any{"thing_id": 2}))
	})
	outcome, err := lc.Write(context.Background(), createSpec)
	if err != nil {
		t.Fatal(err)
	}
	var failed *client.TaskFailedError
	if err := lc.Converge(context.Background(), outcome); !errors.As(err, &failed) {
		t.Fatalf("err = %v, want TaskFailedError", err)
	}
	if err := lc.Converge(context.Background(), nil); err != nil {
		t.Errorf("nil outcome: %v", err)
	}
}

func TestLifecycleModeOverride(t *testing.T) {
	srv := fakeserver.New(t)
	c, _ := client.New(context.Background(), client.Config{Endpoint: srv.URL(), Username: fakeserver.DefaultUsername, Password: fakeserver.DefaultPassword})
	pd := &framework.ProviderData{Client: c, ApprovalMode: framework.ApprovalModeFail}

	if got := framework.NewLifecycle(pd, types.StringValue("wait")).Mode; got != framework.ApprovalModeWait {
		t.Errorf("override ignored: %s", got)
	}
	if got := framework.NewLifecycle(pd, types.StringNull()).Mode; got != framework.ApprovalModeFail {
		t.Errorf("provider default ignored: %s", got)
	}
	if got := framework.NewLifecycle(&framework.ProviderData{Client: c}, types.StringUnknown()).Mode; got != framework.ApprovalModeFail {
		t.Errorf("empty default must be fail, got %s", got)
	}
}

func TestLifecycleAwaitGone(t *testing.T) {
	_, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	calls := 0
	err := lc.AwaitGone(context.Background(), func(context.Context) (bool, error) {
		calls++
		return calls < 3, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}

	boom := errors.New("boom")
	if err := lc.AwaitGone(context.Background(), func(context.Context) (bool, error) { return true, boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = lc.AwaitGone(ctx, func(context.Context) (bool, error) { return true, nil })
	if !client.IsTimeout(err) {
		t.Errorf("err = %v, want a timeout", err)
	}
}

func TestIgnoreNotFound(t *testing.T) {
	if framework.IgnoreNotFound(&client.APIError{StatusCode: 404}) != nil {
		t.Error("404 must be ignored")
	}
	if framework.IgnoreNotFound(&client.APIError{StatusCode: 500}) == nil {
		t.Error("500 must be kept")
	}
	if framework.IgnoreNotFound(nil) != nil {
		t.Error("nil must stay nil")
	}
}
