package framework_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func widgetConfigWithDefaults(b *widgetBackend, defaultTags, body string) string {
	return acctest.ProviderConfigWith(b.srv.URL(), fmt.Sprintf("  default_tags = %s\n", defaultTags)) +
		fmt.Sprintf("\nresource \"omniddi_widget\" \"test\" {\n%s\n}\n", body)
}

func tagsAllCheck(pairs map[string]string) plancheck.PlanCheck {
	checks := map[string]knownvalue.Check{}
	for k, v := range pairs {
		checks[k] = knownvalue.StringExact(v)
	}
	return plancheck.ExpectKnownValue(widgetAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(checks))
}

func TestWidgetInPlaceUpdateNotTouchingTagsLeavesTagsAllUnchangedInThePlan(t *testing.T) {
	b := newWidgetBackend(t)
	before := widgetConfig(b, `
  name = "alpha"
  description = "first"
  deletion_protection = false
  tags = { owner = "netops" }`)
	after := widgetConfig(b, `
  name = "alpha"
  description = "second"
  deletion_protection = false
  tags = { owner = "netops" }`)
	want := map[string]string{"env": "prod", "owner": "netops"}

	widgetCase(t,
		resource.TestStep{Config: before},
		resource.TestStep{
			Config: after,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(widgetAddr, plancheck.ResourceActionUpdate),
				tagsAllCheck(want),
			}},
			Check: resource.TestCheckResourceAttr(widgetAddr, "tags_all.owner", "netops"),
		},
		resource.TestStep{Config: after, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}

func TestWidgetDefaultTagsChangeShowsATagsAllDiffAndIsApplied(t *testing.T) {
	b := newWidgetBackend(t)
	body := `
  name = "alpha"
  deletion_protection = false
  tags = { owner = "netops" }`

	widgetCase(t,
		resource.TestStep{Config: widgetConfigWithDefaults(b, `{ "env" = "prod" }`, body)},
		resource.TestStep{
			Config: widgetConfigWithDefaults(b, `{ "env" = "dev" }`, body),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(widgetAddr, plancheck.ResourceActionUpdate),
				tagsAllCheck(map[string]string{"env": "dev", "owner": "netops"}),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(widgetAddr, "tags_all.env", "dev"),
				resource.TestCheckNoResourceAttr(widgetAddr, "tags.env"),
				func(*terraform.State) error {
					if got := b.srv.TagValues(client.TagObjectDNSView, 1)["env"]; fmt.Sprint(got) != "[dev]" {
						return fmt.Errorf("backend env tag = %v", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config:           widgetConfigWithDefaults(b, `{ "env" = "dev" }`, body),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
		},
	)
}

func TestWidgetCreatePlansTagsAllAsKnown(t *testing.T) {
	b := newWidgetBackend(t)
	widgetCase(t, resource.TestStep{
		Config: widgetConfig(b, `
  name = "alpha"
  deletion_protection = false
  tags = { owner = "netops" }`),
		ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{tagsAllCheck(map[string]string{"env": "prod", "owner": "netops"})}},
	})
}

func TestWidgetFailedCreateIsReplaceableDespiteDeletionProtection(t *testing.T) {
	b := newWidgetBackend(t)
	b.taskFinal, b.logs = []string{fakeserver.StatusInprogress, fakeserver.StatusIncomplete}, "named-checkzone failed"
	protected := widgetConfig(b, `name = "alpha"`)
	unprotected := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t,
		resource.TestStep{
			Config:      protected,
			ExpectError: regexp.MustCompile(`(?s)named-checkzone failed.*tainted.*deletion_protection was set to false`),
		},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{fakeserver.StatusCompleted}, "" },
			Config:    protected,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(widgetAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("DELETE", "/api/v1/widgets/1")); got != 1 {
					return fmt.Errorf("the tainted widget must be replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: unprotected},
	)
}

func TestWidgetApprovedCreateIsAdoptedOnTheNextApply(t *testing.T) {
	b := newWidgetBackend(t)
	var requestID int64
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if requestID != 0 {
			t.Errorf("a second create request was submitted instead of adopting the approved one")
			return fakeserver.Response{}, false
		}
		requestID = b.srv.StageApproval("create_widget", map[string]any{"name": body["name"]})
		return b.srv.ApprovalPending(requestID, "Widget creation request"), true
	}
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)Approve request \d+ in OmniDDI.*provider\s+adopts\s+the\s+approved\s+object`)},
		resource.TestStep{
			PreConfig: func() {
				b.mu.Lock()
				w := &widget{ID: b.srv.NextID()}
				b.apply(w, map[string]any{"name": "alpha"})
				b.widgets[w.ID] = w
				b.mu.Unlock()
				b.srv.SetRequestStatus(requestID, fakeserver.RequestApplied)
				b.srv.SetRequestAppliedOn(requestID, time.Now().Add(-time.Minute))
			},
			Config: cfg,
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("POST", "/api/v1/widgets/")); got != 1 {
					return fmt.Errorf("POST sent %d times; the approved object must be adopted, not re-created", got)
				}
				return nil
			},
		},
	)
	if b.count() != 0 {
		t.Errorf("the adopted widget was not destroyed at the end of the test")
	}
}

func TestWidgetCreateAdoptsTheObjectAfterAGatewayError(t *testing.T) {
	b := newWidgetBackend(t)
	var failed bool
	b.srv.HandleREST(http.MethodPost, "/api/v1/widgets/", func(r *fakeserver.Request) fakeserver.Response {
		if failed {
			return b.create(r)
		}
		failed = true
		b.mu.Lock()
		w := &widget{ID: b.srv.NextID()}
		b.apply(w, r.Map())
		b.widgets[w.ID] = w
		b.mu.Unlock()
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out")
	})
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t, resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(widgetAddr, "name", "alpha")})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/widgets/")); got != 1 {
		t.Errorf("POST sent %d times; the lost response must not trigger a duplicate create", got)
	}
}

func TestWidgetUpdateWithAForeignPendingRequestFails(t *testing.T) {
	b := newWidgetBackend(t)
	cfg := func(description string) string {
		return widgetConfig(b, fmt.Sprintf(`
name = "alpha"
description = %q
deletion_protection = false`, description))
	}

	widgetCase(t,
		resource.TestStep{Config: cfg("first")},
		resource.TestStep{
			PreConfig:   func() { b.srv.StageApproval("update_widget", map[string]any{"widget_id": 1}) },
			Config:      cfg("second"),
			ExpectError: regexp.MustCompile(`Another change to this object is pending approval \(request \d+\)`),
		},
	)
	if got := len(b.srv.RequestsFor("PUT", "/api/v1/widgets/1")); got != 0 {
		t.Errorf("PUT sent %d times while another change was pending", got)
	}
}
