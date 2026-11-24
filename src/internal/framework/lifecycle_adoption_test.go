package framework_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func failOnPost(t *testing.T, srv *fakeserver.Server, method, path string) {
	t.Helper()
	srv.HandleREST(method, path, func(*fakeserver.Request) fakeserver.Response {
		t.Errorf("%s %s must not be sent", method, path)
		return fakeserver.Detail(500, "unexpected")
	})
}

func recoverTo(id int64, found bool, calls *atomic.Int32) func(context.Context) (int64, bool, error) {
	return func(context.Context) (int64, bool, error) {
		calls.Add(1)
		return id, found, nil
	}
}

func TestUpdateNeverWaitsOnOrAdoptsAForeignPendingRequest(t *testing.T) {
	for _, mode := range []framework.ApprovalMode{framework.ApprovalModeFail, framework.ApprovalModeWait} {
		t.Run(string(mode), func(t *testing.T) {
			srv, lc := lifecycleFixture(t, mode)
			pending := srv.StageApproval("update_thing", map[string]any{"name": "x"}, fakeserver.RequestPending, fakeserver.RequestApplied)
			failOnPost(t, srv, "PUT", "/api/v1/things/4")
			spec := framework.WriteSpec{Method: http.MethodPut, Path: "/things/4", ActionType: "update_thing", Match: matchName("x")}

			_, err := lc.Write(context.Background(), spec)
			var conflict *client.PendingChangeError
			if !errors.As(err, &conflict) || conflict.RequestID != pending {
				t.Fatalf("err = %v, want PendingChangeError %d", err, pending)
			}
			if detail := framework.ErrorDetail(err); !strings.Contains(detail, "Another change to this object is pending approval (request") {
				t.Errorf("detail = %s", detail)
			}
			if polls := len(srv.RequestsFor("GET", "/api/v1/requests/1")); polls != 0 {
				t.Errorf("the foreign request must not be polled, got %d polls", polls)
			}
		})
	}
}

func TestExplicitUpdateOpOverridesTheMethod(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
	srv.StageApproval("create_thing", map[string]any{"name": "x"})
	failOnPost(t, srv, "POST", "/api/v1/things/")
	spec := framework.WriteSpec{Method: http.MethodPost, Path: "/things/", ActionType: "create_thing", Match: matchName("x"), Op: framework.WriteOpUpdate}

	var conflict *client.PendingChangeError
	if _, err := lc.Write(context.Background(), spec); !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want PendingChangeError", err)
	}
}

func TestUpdateOwnApprovalIsStillAwaitedInWaitMode(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeWait)
	id := srv.StageApproval("update_thing", map[string]any{"name": "other"}, fakeserver.RequestPending, fakeserver.RequestApplied)
	srv.HandleREST("PUT", "/api/v1/things/4", func(*fakeserver.Request) fakeserver.Response {
		return srv.ApprovalPending(id, "Thing update request")
	})
	spec := framework.WriteSpec{Method: http.MethodPut, Path: "/things/4", ActionType: "update_thing", Match: matchName("x")}

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || !outcome.ViaApproval() || outcome.ApprovalRequestID != id {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	if warnings := outcome.Warnings(); len(warnings) != 1 || warnings.HasError() {
		t.Errorf("an awaited approval must warn that deployment is unconfirmed: %v", warnings)
	}
}

func TestDeleteStillDedupesAgainstAPendingDelete(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	pending := srv.StageApproval("delete_thing", map[string]any{"name": "x"})
	failOnPost(t, srv, "DELETE", "/api/v1/things/4")
	spec := framework.WriteSpec{Method: http.MethodDelete, Path: "/things/4", ActionType: "delete_thing", Match: matchName("x")}

	_, err := lc.Write(context.Background(), spec)
	var approval *client.ApprovalRequiredError
	if !errors.As(err, &approval) || approval.RequestID != pending || approval.Adopts {
		t.Fatalf("err = %v", err)
	}
	if detail := framework.ErrorDetail(err); strings.Contains(detail, "adopts") {
		t.Errorf("only creates adopt: %s", detail)
	}
}

func TestCreateAdoptsARecentlyAppliedApproval(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	applied := srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestApplied)
	srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
	failOnPost(t, srv, "POST", "/api/v1/things/")
	var recovered atomic.Int32
	spec := createSpec
	spec.Match, spec.Recover = matchName("x"), recoverTo(77, true, &recovered)

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || !outcome.Adopted || outcome.RecoveredID != 77 || outcome.ApprovalRequestID != applied || !outcome.ViaApproval() {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	if len(outcome.Warnings()) != 0 {
		t.Errorf("adopting a long-applied request needs no warning: %v", outcome.Warnings())
	}
	if err := lc.Converge(context.Background(), outcome); err != nil {
		t.Errorf("Converge: %v", err)
	}
	id, err := outcome.ResolveID(context.Background(), nil, "thing_id")
	if err != nil || id != 77 {
		t.Errorf("ResolveID = %d, %v", id, err)
	}
}

func TestCreateIgnoresAppliedApprovalsThatAreStaleOrUnrelated(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	stale := srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestApplied)
	srv.SetRequestAppliedOn(stale, time.Now().Add(-30*24*time.Hour))
	srv.StageApproval("create_thing", map[string]any{"name": "other"}, fakeserver.RequestApplied)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 9}, fakeserver.StatusCompleted)
	})
	var recovered atomic.Int32
	spec := createSpec
	spec.Match, spec.Recover = matchName("x"), recoverTo(1, true, &recovered)

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || outcome.Adopted || recovered.Load() != 0 {
		t.Fatalf("outcome = %+v, err = %v, recover calls = %d", outcome, err, recovered.Load())
	}
}

func TestCreateProceedsWhenTheAppliedObjectIsGone(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestApplied)
	var posts atomic.Int32
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		posts.Add(1)
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 9}, fakeserver.StatusCompleted)
	})
	var recovered atomic.Int32
	spec := createSpec
	spec.Match, spec.Recover = matchName("x"), recoverTo(0, false, &recovered)

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || outcome.Adopted || posts.Load() != 1 || recovered.Load() != 1 {
		t.Fatalf("outcome = %+v, err = %v, posts = %d", outcome, err, posts.Load())
	}
}

func TestCreateAdoptionWithoutRecoverLeavesIDResolutionToTheResource(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	applied := srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestApplied)
	failOnPost(t, srv, "POST", "/api/v1/things/")
	spec := createSpec
	spec.Match = matchName("x")

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || !outcome.ViaApproval() || !outcome.Adopted || outcome.ApprovalRequestID != applied || outcome.RecoveredID != 0 {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	id, err := outcome.ResolveID(context.Background(), func(context.Context) (int64, error) { return 31, nil }, "thing_id")
	if err != nil || id != 31 {
		t.Errorf("ResolveID = %d, %v", id, err)
	}
}

func TestAppliedLookupFailureDoesNotBlockCreates(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleGraphQL("requestsPaginated", func(args map[string]any) (any, error) {
		if args["status"] == "APPLIED" {
			return nil, &fakeserver.GraphQLError{Message: "boom", Code: fakeserver.CodeForbidden}
		}
		return map[string]any{"items": []any{}, "total": 0, "hasMore": false}, nil
	})
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 2}, fakeserver.StatusCompleted)
	})
	spec := createSpec
	spec.Match = matchName("x")
	if _, err := lc.Write(context.Background(), spec); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestFailModeCreateErrorTellsTheUserHowTheObjectIsAdopted(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.ApprovalPending(55, "Thing creation request")
	})

	_, err := lc.Write(context.Background(), createSpec)
	var approval *client.ApprovalRequiredError
	if !errors.As(err, &approval) || !approval.Adopts {
		t.Fatalf("err = %v", err)
	}
	detail := framework.ErrorDetail(err)
	for _, want := range []string{"Approve request 55 in OmniDDI, then run `terraform apply` again; the provider adopts the approved object", "approval_mode"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q: %s", want, detail)
		}
	}

	spec := framework.WriteSpec{Method: http.MethodPut, Path: "/things/4", ActionType: "update_thing"}
	srv.HandleREST("PUT", "/api/v1/things/4", func(*fakeserver.Request) fakeserver.Response {
		return srv.ApprovalPending(56, "Thing update request")
	})
	_, err = lc.Write(context.Background(), spec)
	if detail := framework.ErrorDetail(err); strings.Contains(detail, "adopts") || !strings.Contains(detail, "Approve request 56") {
		t.Errorf("update detail = %s", detail)
	}
}

func TestRecoverAdoptsAfterAnAmbiguousCreateFailure(t *testing.T) {
	for name, status := range map[string]int{"bad gateway": 502, "internal error": 500, "gateway timeout": 504} {
		t.Run(name, func(t *testing.T) {
			srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
			srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
				return fakeserver.Detail(status, "upstream went away")
			})
			var recovered atomic.Int32
			spec := createSpec
			spec.Recover = recoverTo(41, true, &recovered)

			outcome, err := lc.Write(context.Background(), spec)
			if err != nil || outcome.RecoveredID != 41 || !outcome.Adopted {
				t.Fatalf("outcome = %+v, err = %v", outcome, err)
			}
			warnings := outcome.Warnings()
			if len(warnings) != 1 || !strings.Contains(warnings[0].Detail(), "upstream went away") || !strings.Contains(warnings[0].Detail(), "Agent Tasks") {
				t.Errorf("warnings = %v", warnings)
			}
		})
	}
}

func TestRecoverIsNotUsedForDefinitiveFailures(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.Detail(400, "ttl must be positive")
	})
	var recovered atomic.Int32
	spec := createSpec
	spec.Recover = recoverTo(41, true, &recovered)

	if _, err := lc.Write(context.Background(), spec); err == nil || recovered.Load() != 0 {
		t.Fatalf("err = %v, recover calls = %d", err, recovered.Load())
	}
}

func TestDuplicateAdoptionIsOptIn(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
		srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
			return fakeserver.Detail(409, "Thing already exists")
		})
		var recovered atomic.Int32
		spec := createSpec
		spec.Recover, spec.RecoverOnDuplicate = recoverTo(8, true, &recovered), optIn

		outcome, err := lc.Write(context.Background(), spec)
		switch {
		case optIn && (err != nil || outcome.RecoveredID != 8):
			t.Errorf("opt-in: outcome = %+v, err = %v", outcome, err)
		case !optIn && (err == nil || recovered.Load() != 0 || !client.IsDuplicate(err)):
			t.Errorf("a duplicate must stay an error by default: %v", err)
		}
	}
}

func TestRecoverMissOrFailureKeepsTheOriginalError(t *testing.T) {
	for name, recoverFn := range map[string]func(context.Context) (int64, bool, error){
		"not found": func(context.Context) (int64, bool, error) { return 0, false, nil },
		"lookup error": func(context.Context) (int64, bool, error) {
			return 0, false, errors.New("lookup failed")
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
			srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
				return fakeserver.Detail(503, "unavailable")
			})
			spec := createSpec
			spec.Recover = recoverFn
			_, err := lc.Write(context.Background(), spec)
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 {
				t.Fatalf("err = %v, want the original 503", err)
			}
		})
	}
}

func TestRecoverRunsEvenWhenTheOperationDeadlineExpiredButNotWhenCancelled(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		time.Sleep(150 * time.Millisecond)
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 1}, fakeserver.StatusCompleted)
	})
	var live atomic.Bool
	spec := createSpec
	spec.Recover = func(ctx context.Context) (int64, bool, error) {
		live.Store(ctx.Err() == nil)
		return 5, true, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	outcome, err := lc.Write(ctx, spec)
	if err != nil || outcome.RecoveredID != 5 || !live.Load() {
		t.Fatalf("outcome = %+v, err = %v, recovery context live = %v", outcome, err, live.Load())
	}

	cancelled, stop := context.WithCancel(context.Background())
	var calls atomic.Int32
	spec.Recover = recoverTo(5, true, &calls)
	time.AfterFunc(40*time.Millisecond, stop)
	if _, err := lc.Write(cancelled, spec); err == nil || calls.Load() != 0 {
		t.Fatalf("a cancelled run must not attempt recovery: err = %v, calls = %d", err, calls.Load())
	}
}

func TestResolveIDAndWarnings(t *testing.T) {
	ctx := context.Background()
	direct := &framework.WriteOutcome{Result: mustResult(t, `{"thing_id": 12}`)}
	if id, err := direct.ResolveID(ctx, nil, "thing_id"); err != nil || id != 12 {
		t.Errorf("direct = %d, %v", id, err)
	}
	if _, err := direct.ResolveID(ctx, nil, "missing"); err == nil {
		t.Error("a missing id without a resolver must be an error")
	}
	boom := errors.New("lookup failed")
	if _, err := direct.ResolveID(ctx, func(context.Context) (int64, error) { return 0, boom }, "missing"); !errors.Is(err, boom) {
		t.Errorf("resolver error must be returned: %v", err)
	}
	if _, err := direct.ResolveID(ctx, func(context.Context) (int64, error) { return 0, nil }, "missing"); err == nil {
		t.Error("a zero id is not an id")
	}
	viaApproval := &framework.WriteOutcome{ApprovalRequestID: 3}
	if id, err := viaApproval.ResolveID(ctx, func(context.Context) (int64, error) { return 8, nil }); err != nil || id != 8 {
		t.Errorf("via approval = %d, %v", id, err)
	}
	if got := (*framework.WriteOutcome)(nil).Warnings(); len(got) != 0 {
		t.Errorf("nil outcome warnings: %v", got)
	}
	if got := direct.Warnings(); len(got) != 0 {
		t.Errorf("a direct write needs no warnings: %v", got)
	}
	if got := viaApproval.Warnings(); len(got) != 1 || !strings.Contains(got[0].Detail(), "Request 3") {
		t.Errorf("approval warnings: %v", got)
	}
}

func mustResult(t *testing.T, body string) *client.WriteResult {
	t.Helper()
	srv := fakeserver.New(t)
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	srv.HandleREST("POST", "/api/v1/r/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(200, payload)
	})
	c, err := client.New(context.Background(), client.Config{Endpoint: srv.URL(), Username: fakeserver.DefaultUsername, Password: fakeserver.DefaultPassword})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Write(context.Background(), "POST", "/r/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAwaitGoneWrapsCancellationConsistently(t *testing.T) {
	_, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	ctx, cancel := context.WithCancel(context.Background())
	err := lc.AwaitGone(ctx, func(ctx context.Context) (bool, error) {
		cancel()
		return true, ctx.Err()
	})
	var timeout *client.TimeoutError
	if !errors.As(err, &timeout) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a TimeoutError wrapping the cancellation", err)
	}
}

func TestCreateAdoptsOnlyRequestsWhosePayloadMatches(t *testing.T) {
	cases := []struct {
		name    string
		details map[string]any
		adopt   bool
	}{
		{"same payload", map[string]any{"name": "x", "ttl": float64(300), "data": []any{"192.0.2.10"}}, true},
		{"case and trailing dot", map[string]any{"name": "x", "host": "Mail.Example.com."}, true},
		{"fields the request does not store", map[string]any{"name": "x"}, true},
		{"different values", map[string]any{"name": "x", "ttl": float64(300), "data": []any{"192.0.2.99"}}, false},
		{"different ttl", map[string]any{"name": "x", "ttl": float64(60)}, false},
		{"backend-normalised forms", map[string]any{
			"name": "x", "data": "192.0.2.10", "mac": "aa:bb:cc:dd:ee:01", "ip": "2001:db8::10",
			"ns": []any{"ns1.example.com"}, "srv": []any{map[string]any{"host": "sip.example.com", "port": "5060"}},
			"hinfo": []any{"x86", "linux"},
		}, true},
		{"different mac", map[string]any{"name": "x", "mac": "aa:bb:cc:dd:ee:02"}, false},
		{"different ip", map[string]any{"name": "x", "ip": "2001:db8::11"}, false},
		{"different ns target", map[string]any{"name": "x", "ns": []any{"ns2.example.com"}}, false},
		{"one value against two", map[string]any{"name": "x", "data": []any{"192.0.2.10", "192.0.2.11"}}, false},
		{"one srv entry against two", map[string]any{"name": "x", "srv": []any{
			map[string]any{"host": "sip.example.com", "port": "5060"}, map[string]any{"host": "sip2.example.com", "port": "5060"},
		}}, false},
		{"numeric-looking text is not a number", map[string]any{"name": "x", "txt": "1000"}, false},
		{"different srv port", map[string]any{"name": "x", "srv": []any{map[string]any{"port": float64(5061)}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
			applied := srv.StageApproval("create_thing", tc.details, fakeserver.RequestApplied)
			srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
			srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
				return srv.AcceptedWithTasks(map[string]any{"thing_id": 9}, fakeserver.StatusCompleted)
			})
			var recovered atomic.Int32
			spec := createSpec
			spec.Body = map[string]any{
				"name": "x", "ttl": 300, "data": []string{"192.0.2.10"}, "host": "mail.example.com",
				"mac": "AA-BB-CC-DD-EE-01", "ip": "2001:DB8:0:0::10", "ns": "NS1.Example.com.",
				"srv":   map[string]any{"target": "sip.example.com.", "port": 5060},
				"hinfo": map[string]any{"cpu": "x86", "os": "linux"},
				"txt":   "1e3",
			}
			spec.Match, spec.Recover = matchName("x"), recoverTo(77, true, &recovered)

			outcome, err := lc.Write(context.Background(), spec)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if outcome.Adopted != tc.adopt {
				t.Errorf("adopted = %v, want %v", outcome.Adopted, tc.adopt)
			}
			if posts := len(srv.RequestsFor("POST", "/api/v1/things/")); (posts == 0) != tc.adopt {
				t.Errorf("POST count = %d", posts)
			}
		})
	}
}

func TestCreateDoesNotResumeAPendingRequestWithADifferentPayload(t *testing.T) {
	srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
	srv.StageApproval("create_thing", map[string]any{"name": "x", "ttl": float64(60)})
	srv.HandleREST("POST", "/api/v1/things/", func(*fakeserver.Request) fakeserver.Response {
		return srv.AcceptedWithTasks(map[string]any{"thing_id": 9}, fakeserver.StatusCompleted)
	})
	spec := createSpec
	spec.Body = map[string]any{"name": "x", "ttl": 300}
	spec.Match = matchName("x")

	outcome, err := lc.Write(context.Background(), spec)
	if err != nil || outcome.ViaApproval() {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
}

func TestPrecheckRunsOnlyBeforeANewWrite(t *testing.T) {
	t.Run("skipped when adopting", func(t *testing.T) {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
		applied := srv.StageApproval("create_thing", map[string]any{"name": "x"}, fakeserver.RequestApplied)
		srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
		var recovered atomic.Int32
		spec := createSpec
		spec.Match, spec.Recover = matchName("x"), recoverTo(77, true, &recovered)
		spec.Precheck = func(context.Context) error { return errors.New("precheck must not run") }

		if outcome, err := lc.Write(context.Background(), spec); err != nil || !outcome.Adopted {
			t.Fatalf("outcome = %+v, err = %v", outcome, err)
		}
	})
	t.Run("stops the write", func(t *testing.T) {
		srv, lc := lifecycleFixture(t, framework.ApprovalModeFail)
		failOnPost(t, srv, "POST", "/api/v1/things/")
		refusal := errors.New("address taken")
		spec := createSpec
		spec.Match = matchName("x")
		spec.Precheck = func(context.Context) error { return refusal }

		if _, err := lc.Write(context.Background(), spec); !errors.Is(err, refusal) {
			t.Fatalf("err = %v, want the precheck refusal", err)
		}
	})
}
