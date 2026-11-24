package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func TestGetApprovalRequest(t *testing.T) {
	c, srv := newClient(t)
	id := srv.StageApproval("create_zone", map[string]any{"zone_data": map[string]any{"name": "example.com"}})

	req, err := c.GetApprovalRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != id || req.Status != "pending" || req.ActionType != "create_zone" {
		t.Errorf("unexpected %+v", req)
	}
	if _, err := c.GetApprovalRequest(context.Background(), 99999); !client.IsNotFound(err) {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestAwaitApprovalUntilApplied(t *testing.T) {
	c, srv := newClient(t)
	id := srv.StageApproval("create_view", nil, fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)

	req, err := c.AwaitApproval(context.Background(), id)
	if err != nil {
		t.Fatalf("AwaitApproval: %v", err)
	}
	if req.Status != "applied" {
		t.Errorf("status = %q", req.Status)
	}
}

func TestAwaitApprovalRejectedAndWithdrawn(t *testing.T) {
	for _, status := range []string{fakeserver.RequestRejected, fakeserver.RequestWithdrawn} {
		t.Run(status, func(t *testing.T) {
			c, srv := newClient(t)
			id := srv.StageApproval("create_view", nil, fakeserver.RequestPending, status)

			_, err := c.AwaitApproval(context.Background(), id)
			var rejected *client.ApprovalRejectedError
			if !errors.As(err, &rejected) || rejected.RequestID != id || rejected.Status != status {
				t.Fatalf("err = %v, want ApprovalRejectedError", err)
			}
		})
	}
}

func TestAwaitApprovalTimeout(t *testing.T) {
	c, srv := newClient(t)
	id := srv.StageApproval("create_view", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := c.AwaitApproval(ctx, id)
	var timeout *client.TimeoutError
	if !errors.As(err, &timeout) || timeout.RequestID != id || timeout.LastStatus != "pending" {
		t.Fatalf("err = %v, want TimeoutError for request %d", err, id)
	}
}

func TestAwaitApprovalUnknownRequest(t *testing.T) {
	c, _ := newClient(t)
	if _, err := c.AwaitApproval(context.Background(), 404404); !client.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestFindPendingRequestsMatchesClientSide(t *testing.T) {
	c, srv := newClient(t)
	wanted := srv.StageApproval("create_zone", map[string]any{"zone_data": map[string]any{"name": "example.com"}})
	srv.StageApproval("create_zone", map[string]any{"zone_data": map[string]any{"name": "other.example.com"}})
	srv.StageApproval("delete_zone", map[string]any{"zone_data": map[string]any{"name": "example.com"}})
	srv.StageApproval("create_zone", map[string]any{"zone_data": map[string]any{"name": "example.com"}}, fakeserver.RequestApplied)

	match := func(details map[string]any) bool {
		zone, _ := details["zone_data"].(map[string]any)
		return zone["name"] == "example.com"
	}
	found, err := c.FindPendingRequests(context.Background(), "create_zone", match)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != wanted {
		t.Fatalf("found = %+v, want only request %d", found, wanted)
	}

	all, err := c.FindPendingRequests(context.Background(), "create_zone", nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %+v, %v", all, err)
	}
}
