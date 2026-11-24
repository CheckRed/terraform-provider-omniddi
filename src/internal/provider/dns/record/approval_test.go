package record_test

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
)

func approvalConfig(b *recordBackend, extra string) string {
	return recordConfig(b, "name = \"appr\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]\n"+extra)
}

func TestRecordApprovalFailModeReportsRequestID(t *testing.T) {
	b := newRecordBackend(t)
	b.approval = func(map[string]any) (fakeserver.Response, bool) {
		return b.srv.ApprovalPending(321, "Record creation request"), true
	}
	recordCase(t, resource.TestStep{Config: approvalConfig(b, ""), ExpectError: regexp.MustCompile(`Approval required \(request 321\)`)})
	if b.count() != 0 {
		t.Errorf("nothing may be created while approval is pending")
	}
}

func TestRecordPendingDuplicateIsReportedInsteadOfResubmitted(t *testing.T) {
	b := newRecordBackend(t)
	pending := b.srv.StageApproval("create_record", map[string]any{"zone_id": float64(testZoneID), "name": "appr", "type": "A"})
	recordCase(t, resource.TestStep{Config: approvalConfig(b, ""), ExpectError: regexp.MustCompile(fmt.Sprintf(`Approval required \(request %d\)`, pending))})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/records/")); got != 0 {
		t.Errorf("a duplicate request must not be submitted, POST count = %d", got)
	}
}

func TestRecordApprovalWaitModeAppliesAfterApproval(t *testing.T) {
	b := newRecordBackend(t)
	submitted := int64(0)
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if _, isCreate := body["zone_id"]; !isCreate || submitted != 0 {
			return fakeserver.Response{}, false
		}
		submitted = b.srv.StageApproval("create_record",
			map[string]any{"zone_id": body["zone_id"], "name": body["name"], "type": body["type"]},
			fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)
		b.seed(fakeRecord{Name: "appr", Type: "A", Values: map[string]any{"values": []any{"192.0.2.10"}}})
		return b.srv.ApprovalPending(submitted, "Record creation request"), true
	}
	recordCase(t, resource.TestStep{
		Config: approvalConfig(b, "approval_mode = \"wait\""),
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor("POST", "/api/v1/records/")); got != 1 {
				return fmt.Errorf("POST sent %d times", got)
			}
			return nil
		},
	})
}

func TestRecordUpdateUnderApprovalKeepsOldStateInFailMode(t *testing.T) {
	b := newRecordBackend(t)
	updated := recordConfig(b, "name = \"appr\"\ntype = \"A\"\nvalues = [\"192.0.2.99\"]")
	recordCase(t,
		resource.TestStep{Config: approvalConfig(b, "")},
		resource.TestStep{
			PreConfig: func() {
				b.approval = func(map[string]any) (fakeserver.Response, bool) {
					return b.srv.ApprovalPending(55, "Record update request"), true
				}
			},
			Config: updated, ExpectError: regexp.MustCompile(`Approval required \(request 55\)`),
		},
		resource.TestStep{PreConfig: func() { b.approval = nil }, Config: approvalConfig(b, ""), PlanOnly: true},
	)
}

func TestRecordDeleteUnderApprovalKeepsObjectInFailMode(t *testing.T) {
	b := newRecordBackend(t)
	recordCase(t,
		resource.TestStep{Config: approvalConfig(b, "")},
		resource.TestStep{
			PreConfig: func() {
				b.approval = func(map[string]any) (fakeserver.Response, bool) {
					return b.srv.ApprovalPending(56, "Record delete request"), true
				}
			},
			Config:      recordConfigWithoutResource(b),
			ExpectError: regexp.MustCompile(`Approval required \(request 56\)`),
		},
		resource.TestStep{PreConfig: func() { b.approval = nil }, Config: recordConfigWithoutResource(b)},
	)
	if b.count() != 0 {
		t.Errorf("the record must be gone after approval is no longer required")
	}
}

func TestRecordCreateDoesNotAdoptAColleaguesAppliedRecord(t *testing.T) {
	b := newRecordBackend(t)
	applied := b.srv.StageApproval("create_record",
		map[string]any{"zone_id": float64(testZoneID), "name": "appr", "type": "A", "data": "192.0.2.99"}, fakeserver.RequestApplied)
	b.srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
	b.seed(fakeRecord{Name: "appr", Type: "A", Values: map[string]any{"values": []any{"192.0.2.99"}}})
	recordCase(t, resource.TestStep{Config: approvalConfig(b, ""), ExpectError: regexp.MustCompile(`already exists`)})
	if rec := b.find("appr", "A"); rec.Values["values"].([]any)[0] != "192.0.2.99" {
		t.Errorf("the colleague's record was changed: %v", rec.Values)
	}
}

func TestRecordCreateAdoptsItsOwnAppliedRequest(t *testing.T) {
	b := newRecordBackend(t)
	applied := b.srv.StageApproval("create_record",
		map[string]any{"zone_id": float64(testZoneID), "name": "appr", "type": "A", "data": "192.0.2.10"}, fakeserver.RequestApplied)
	b.srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
	b.seed(fakeRecord{Name: "appr", Type: "A", Values: map[string]any{"values": []any{"192.0.2.10"}}})
	recordCase(t, resource.TestStep{
		Config: approvalConfig(b, ""),
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor("POST", "/api/v1/records/")); got != 0 {
				return fmt.Errorf("POST sent %d times", got)
			}
			return nil
		},
	})
}

func TestRecordCreateAdoptsItsOwnAppliedNSRequestAfterBackendNormalisation(t *testing.T) {
	b := newRecordBackend(t)
	applied := b.srv.StageApproval("create_record", map[string]any{
		"zone_id": float64(testZoneID), "name": "lab", "type": "NS",
		"data": []any{"ns1.lab.example.com", "ns2.lab.example.com"}, "record_type_id": float64(7),
		"view": map[string]any{"id": float64(1), "name": testViewName}, "network_scope": map[string]any{"id": float64(1)},
	}, fakeserver.RequestApplied)
	b.srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
	b.seed(fakeRecord{Name: "lab", Type: "NS", Values: map[string]any{"values": []any{"ns1.lab.example.com.", "ns2.lab.example.com."}}})
	recordCase(t, resource.TestStep{
		Config: recordConfig(b, "name = \"lab\"\ntype = \"NS\"\nvalues = [\"NS1.lab.example.com.\", \"ns2.lab.example.com.\"]"),
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor("POST", "/api/v1/records/")); got != 0 {
				return fmt.Errorf("POST sent %d times; the applied NS request must be adopted", got)
			}
			return nil
		},
	})
}
