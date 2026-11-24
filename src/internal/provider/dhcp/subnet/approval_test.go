package subnet_test

import (
	"fmt"
	"net/http"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
)

func TestPromoteApprovalFailModeThenAdoptsTheAppliedRequest(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.60.0.0/24")
	var staged atomic.Int64
	b.approval = b.holdForApproval(opPromote, &staged)
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	pending := regexp.MustCompile(`Approval required \(request \d+\)`)
	run(t,
		resource.TestStep{Config: cfg, ExpectError: pending},
		resource.TestStep{Config: cfg, ExpectError: pending},
		resource.TestStep{
			PreConfig: func() {
				b.enable(id, 7)
				b.srv.SetRequestStatus(staged.Load(), fakeserver.RequestApplied)
				b.srv.SetRequestAppliedOn(staged.Load(), time.Now())
			},
			Config: cfg,
			Check: func(s *terraform.State) error {
				if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(id))); got != 1 {
					return fmt.Errorf("promote sent %d times; a repeated apply must not file a duplicate request", got)
				}
				return resource.TestCheckResourceAttr(addr, "server_group_id", "7")(s)
			},
		},
	)
}

func TestPromoteApprovalFailModeIgnoresARequestForAnotherGroup(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.61.0.0/24")
	other := b.srv.StageApproval("create_subnet", map[string]any{
		"server_group_id": 11, "ipam_node": map[string]any{"attach_node_id": id},
	}, fakeserver.RequestPending)
	run(t,
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")},
		resource.TestStep{
			PreConfig: func() { b.srv.SetRequestStatus(other, fakeserver.RequestWithdrawn) },
			Config:    dhcptest.Config(b.srv, ""),
		},
	)
	if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(id))); got != 1 {
		t.Errorf("promote sent %d times, want 1: a pending request for another group is not this resource's", got)
	}
}

func TestApprovalWaitModeCoversPromoteSettingsUpdateAndDemote(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.62.0.0/24")
	b.approval = b.approveImmediately()
	body := func(lease int) string {
		return fmt.Sprintf("server_group_id = 7\napproval_mode = \"wait\"\ndeletion_protection = false\nlease_time = %d", lease)
	}
	run(t,
		resource.TestStep{
			Config: b.hclFor(id, body(60)),
			Check: func(s *terraform.State) error {
				if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(id))); got != 1 {
					return fmt.Errorf("promote sent %d times", got)
				}
				return resource.TestCheckResourceAttr(addr, "lease_time", "60")(s)
			},
		},
		resource.TestStep{Config: b.hclFor(id, body(90)), Check: resource.TestCheckResourceAttr(addr, "lease_time", "90")},
		resource.TestStep{Config: dhcptest.Config(b.srv, "")},
	)
	if _, enabled := b.config(id); enabled {
		t.Error("DHCP is still enabled after an approved demote")
	}
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 1 {
		t.Errorf("demote sent %d times, want 1", got)
	}
}

func TestSettingsUpdateApprovalFailModeAndPendingChange(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.63.0.0/24")
	before := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\nlease_time = 60")
	after := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\nlease_time = 120")
	var staged atomic.Int64
	run(t,
		resource.TestStep{Config: before},
		resource.TestStep{
			PreConfig:   func() { b.approval = b.holdForApproval(opUpdate, &staged) },
			Config:      after,
			ExpectError: regexp.MustCompile(`Approval required \(request \d+\)`),
		},
		resource.TestStep{
			Config:      after,
			ExpectError: regexp.MustCompile(`(?i)another\s+change\s+to\s+this\s+object\s+is\s+pending\s+approval`),
		},
		resource.TestStep{
			PreConfig: func() {
				b.approval = nil
				b.srv.SetRequestStatus(staged.Load(), fakeserver.RequestWithdrawn)
			},
			Config: after,
			Check: func(s *terraform.State) error {
				if got := len(requestBodies(b.srv, http.MethodPut, settingsPathFor(id))); got != 3 {
					return fmt.Errorf("settings PUT sent %d times, want 3 (create, the held update, the final update)", got)
				}
				return resource.TestCheckResourceAttr(addr, "lease_time", "120")(s)
			},
		},
	)
}

func TestSettingsApprovalInFailModeTaintsTheNewSubnet(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.64.0.0/24")
	var staged atomic.Int64
	b.approval = b.holdForApproval(opUpdate, &staged)
	run(t, resource.TestStep{
		Config:      b.hclFor(id, "server_group_id = 7\nlease_time = 60"),
		ExpectError: regexp.MustCompile(`(?s)settings\s+could\s+not\s+be\s+applied.*Approval\s+required.*deletion_protection\s+was\s+set\s+to\s+false`),
	})
	if cfg, ok := b.config(id); ok {
		t.Errorf("the tainted subnet was not demoted by the cleanup destroy: %+v", cfg)
	}
}

func TestDemoteApprovalFailModeDoesNotFileADuplicate(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.65.0.0/24")
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	var staged atomic.Int64
	pending := regexp.MustCompile(`Approval required \(request \d+\)`)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig:   func() { b.approval = b.holdForApproval(opDemote, &staged) },
			Config:      dhcptest.Config(b.srv, ""),
			ExpectError: pending,
		},
		resource.TestStep{Config: dhcptest.Config(b.srv, ""), ExpectError: pending},
		resource.TestStep{
			PreConfig: func() {
				b.approval = nil
				b.srv.SetRequestStatus(staged.Load(), fakeserver.RequestWithdrawn)
			},
			Config: dhcptest.Config(b.srv, ""),
		},
	)
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 2 {
		t.Errorf("demote sent %d times, want 2 (the held request and the final one)", got)
	}
}
