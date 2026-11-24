package zone_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/zone"
)

func tagsAllCheck(pairs map[string]string) plancheck.PlanCheck {
	checks := map[string]knownvalue.Check{}
	for k, v := range pairs {
		checks[k] = knownvalue.StringExact(v)
	}
	return plancheck.ExpectKnownValue(zoneAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(checks))
}

func TestZoneInPlaceUpdateNotTouchingTagsLeavesTagsAllUnchangedInThePlan(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(ttl int) string {
		return zoneConfig(b, fmt.Sprintf("name = \"a.example.com\"\nttl = %d\ndeletion_protection = false\ntags = { owner = \"netops\" }", ttl))
	}
	zoneCase(t,
		resource.TestStep{Config: cfg(3000)},
		resource.TestStep{
			Config: cfg(4000),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(zoneAddr, plancheck.ResourceActionUpdate),
				tagsAllCheck(map[string]string{"env": "prod", "owner": "netops"}),
				plancheck.ExpectKnownValue(zoneAddr, tfjsonpath.New("created_on"), knownvalue.StringExact("2026-01-01T00:00:00")),
				plancheck.ExpectKnownValue(zoneAddr, tfjsonpath.New("view_name"), knownvalue.StringExact(seededViewName)),
			}},
			Check: resource.TestCheckResourceAttr(zoneAddr, "tags_all.owner", "netops"),
		},
		resource.TestStep{Config: cfg(4000), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}

func TestZoneFailedCreateIsReplaceableDespiteDeletionProtection(t *testing.T) {
	b := newZoneBackend(t)
	b.taskFinal, b.logs = []string{fakeserver.StatusInprogress, fakeserver.StatusIncomplete}, "named-checkzone failed"
	protected := zoneConfig(b, "name = \"a.example.com\"")
	unprotected := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")

	zoneCase(t,
		resource.TestStep{
			Config:      protected,
			ExpectError: regexp.MustCompile(`(?s)named-checkzone failed.*tainted.*deletion_protection was set to false`),
		},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{fakeserver.StatusCompleted}, "" },
			Config:    protected,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(zoneAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("DELETE", fmt.Sprintf("/api/v1/zones/%d", b.viewID+1))); got != 1 {
					return fmt.Errorf("the tainted zone must be replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: unprotected},
	)
}

func TestZoneCreateWithAFailedFollowUpSyncWarnsInsteadOfTainting(t *testing.T) {
	b := newZoneBackend(t)
	b.syncLogs = "zone not found: a.example.com"
	zoneCase(t, resource.TestStep{
		Config:           zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false"),
		ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
		Check:            resource.TestCheckResourceAttr(zoneAddr, "name", "a.example.com"),
	})
}

func TestZoneApprovedCreateIsAdoptedOnTheNextApply(t *testing.T) {
	b := newZoneBackend(t)
	var requestID int64
	b.gate = func(method string, body map[string]any) (fakeserver.Response, bool) {
		if method != http.MethodPost {
			return fakeserver.Response{}, false
		}
		if requestID != 0 {
			t.Errorf("a second create request was submitted instead of adopting the approved one")
			return fakeserver.Response{}, false
		}
		requestID = b.srv.StageApproval("create_zone", map[string]any{"name": body["name"], "view_id": body["view_id"]})
		return b.srv.ApprovalPending(requestID, "Zone creation request"), true
	}
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")

	zoneCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)Approve request \d+ in OmniDDI.*provider\s+adopts\s+the\s+approved\s+object`)},
		resource.TestStep{
			PreConfig: func() {
				b.seed("a.example.com", b.viewID)
				b.srv.SetRequestStatus(requestID, fakeserver.RequestApplied)
				b.srv.SetRequestAppliedOn(requestID, time.Now().Add(-time.Minute))
			},
			Config: cfg,
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 1 {
					return fmt.Errorf("POST sent %d times; the approved zone must be adopted, not re-created", got)
				}
				return nil
			},
		},
	)
	if b.count() != 0 {
		t.Errorf("the adopted zone was not destroyed at the end of the test")
	}
}

func TestZoneCreateAdoptsTheObjectAfterAGatewayError(t *testing.T) {
	b := newZoneBackend(t)
	var failed bool
	b.srv.HandleREST(http.MethodPost, "/api/v1/zones/", func(r *fakeserver.Request) fakeserver.Response {
		if failed {
			return b.create(r)
		}
		failed = true
		b.seed("a.example.com", b.viewID)
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out")
	})
	cfg := zoneConfig(b, "name = \"A.Example.com\"\ndeletion_protection = false")

	zoneCase(t, resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(zoneAddr, "name", "A.Example.com")})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 1 {
		t.Errorf("POST sent %d times; the lost response must not trigger a duplicate create", got)
	}
}

func TestZoneUpdateWithAForeignPendingRequestFails(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(ttl int) string {
		return zoneConfig(b, fmt.Sprintf("name = \"a.example.com\"\nttl = %d\ndeletion_protection = false", ttl))
	}
	var zoneID int64
	zoneCase(t,
		resource.TestStep{Config: cfg(3000)},
		resource.TestStep{
			PreConfig: func() {
				zoneID = b.first().ID
				b.srv.StageApproval("update_zone", map[string]any{"zone_id": zoneID})
			},
			Config:      cfg(4000),
			ExpectError: regexp.MustCompile(`Another change to this object is pending approval \(request \d+\)`),
		},
	)
	if got := len(b.srv.RequestsFor("PUT", fmt.Sprintf("/api/v1/zones/%d", zoneID))); got != 0 {
		t.Errorf("PUT sent %d times while another change was pending", got)
	}
}

func TestZoneSecondarySyncKeyFollowsPrimaryServerKeyChanges(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(key int, ttl int) string {
		return zoneConfig(b, fmt.Sprintf(`
  name      = "sec.example.com"
  zone_type = "Secondary"
  ttl       = %d
  primary_servers = [{ ip = "198.51.100.10", tsig_key_id = %d }]
  deletion_protection = false
`, ttl, key))
	}
	zoneCase(t,
		resource.TestStep{Config: cfg(5, 3000), Check: resource.TestCheckResourceAttr(zoneAddr, "secondary_sync_tsig_key_id", "5")},
		resource.TestStep{
			Config: cfg(5, 4000),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectKnownValue(zoneAddr, tfjsonpath.New("secondary_sync_tsig_key_id"), knownvalue.Int64Exact(5)),
			}},
		},
		resource.TestStep{
			Config: cfg(6, 4000),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectUnknownValue(zoneAddr, tfjsonpath.New("secondary_sync_tsig_key_id")),
			}},
			Check: resource.TestCheckResourceAttr(zoneAddr, "secondary_sync_tsig_key_id", "6"),
		},
		resource.TestStep{Config: cfg(6, 4000), PlanOnly: true},
	)
}

func TestZoneCIDRSpellingChangeDoesNotReplaceTheZone(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(cidr string) string {
		return zoneConfig(b, fmt.Sprintf("zone_type = \"Reverse\"\nprotocol = \"IPv4\"\ncidr = %q\ndeletion_protection = false", cidr))
	}
	zoneCase(t,
		resource.TestStep{Config: cfg("192.0.2.0/24")},
		resource.TestStep{
			Config: cfg("192.0.2.5/24"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(zoneAddr, plancheck.ResourceActionUpdate),
			}},
		},
		resource.TestStep{Config: cfg("192.0.2.5/24"), PlanOnly: true},
		resource.TestStep{
			Config: cfg("198.51.100.0/24"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(zoneAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
		},
	)
	if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 2 {
		t.Errorf("expected 2 creates (create + replacement for a different network), got %d", got)
	}
}

func TestZoneDuplicatePrimaryServerSpellingsAreRejected(t *testing.T) {
	b := newZoneBackend(t)
	zoneCase(t, resource.TestStep{
		Config: zoneConfig(b, `
  name      = "sec.example.com"
  zone_type = "Secondary"
  primary_servers = [{ ip = "2001:db8::1" }, { ip = "2001:0db8::1", port = 53 }]
`),
		PlanOnly:    true,
		ExpectError: regexp.MustCompile(`Duplicate primary server`),
	})
}

func TestZoneImportStopsAfterAFailedKeyResolution(t *testing.T) {
	ctx := context.Background()
	r, _ := zone.NewResource().(fwresource.ResourceWithImportState)
	var schemaResp fwresource.SchemaResponse
	zone.NewResource().Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	resp := fwresource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}}

	r.ImportState(ctx, fwresource.ImportStateRequest{ID: "-5"}, &resp)

	if got := resp.Diagnostics.ErrorsCount(); got != 1 {
		t.Errorf("a failed import must report one error and skip the defaults, got %d: %v", got, resp.Diagnostics)
	}
}

func TestZonesDataSourceFailsInsteadOfTruncatingAtThePageLimit(t *testing.T) {
	b := newZoneBackend(t)
	b.srv.HandleGraphQL("zonesPaginated", func(map[string]any) (any, error) {
		return map[string]any{"items": []any{b.zoneJSON(&zoneRecord{ID: 1, Name: "endless.example.com", Config: map[string]any{}})}, "total": 1000000, "hasMore": true}, nil
	})
	zoneCase(t, resource.TestStep{
		Config:      dataConfig(b, `data "omniddi_dns_zones" "all" {}`),
		ExpectError: regexp.MustCompile(`(?s)returned more than 20000 items.*narrow\s+the\s+query`),
	})
}

func TestZonesDataSourceSortsByIDAndPassesTheView(t *testing.T) {
	b := newZoneBackend(t)
	b.seed("a.example.com", b.viewID)
	zoneCase(t, resource.TestStep{
		Config: dataConfig(b, fmt.Sprintf(`data "omniddi_dns_zones" "x" { view_id = %d }`, b.viewID)),
		Check: func(*terraform.State) error {
			reqs := b.srv.Requests()
			for i := len(reqs) - 1; i >= 0; i-- {
				body := string(reqs[i].Body)
				if regexp.MustCompile(`zonesPaginated`).MatchString(body) {
					if !regexp.MustCompile(`"sortBy":"id"`).MatchString(body) || !regexp.MustCompile(fmt.Sprintf(`"viewId":%d`, b.viewID)).MatchString(body) {
						return fmt.Errorf("list request lacks sortBy id or the view id: %s", body)
					}
					return nil
				}
			}
			return fmt.Errorf("no zonesPaginated request recorded")
		},
	})
}
