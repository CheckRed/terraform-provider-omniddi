package framework_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const widgetAddr = "omniddi_widget.test"

func widgetConfig(b *widgetBackend, body string) string {
	return acctest.ProviderConfigWith(b.srv.URL(), `  default_tags = { "env" = "prod" }
`) + fmt.Sprintf("\nresource \"omniddi_widget\" \"test\" {\n%s\n}\n", body)
}

func widgetCase(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: widgetFactories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

func TestWidgetLifecycleTagsIdentityAndImport(t *testing.T) {
	b := newWidgetBackend(t)
	create := widgetConfig(b, `
  name        = "alpha"
  fqdn        = "Host.Example.com."
  description = "first"
  deletion_protection = false
  tags        = { owner = "netops" }
  tags_list   = { teams = ["a", "b"] }
`)
	update := widgetConfig(b, `
  name        = "alpha"
  fqdn        = "host.example.com"
  description = "second"
  deletion_protection = false
  tags        = { env = "dev" }
`)

	importable := widgetConfig(b, `
  name        = "alpha"
  fqdn        = "host.example.com"
  description = "second"
  tags        = { env = "dev" }
`)

	widgetCase(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(widgetAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectKnownValue(widgetAddr, tfjsonpath.New("tags"), knownvalue.MapExact(map[string]knownvalue.Check{
					"owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectKnownValue(widgetAddr, tfjsonpath.New("tags_list"), knownvalue.MapExact(map[string]knownvalue.Check{
					"teams": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("a"), knownvalue.StringExact("b")}),
				})),
				statecheck.ExpectKnownValue(widgetAddr, tfjsonpath.New("fqdn"), knownvalue.StringExact("Host.Example.com.")),
				statecheck.ExpectIdentityValueMatchesState(widgetAddr, tfjsonpath.New("id")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(widgetAddr, "id"),
				resource.TestCheckNoResourceAttr(widgetAddr, "approval_mode"),
				func(*terraform.State) error {
					want := map[string][]string{"env": {"prod"}, "owner": {"netops"}, "teams": {"a", "b"}}
					for name, values := range want {
						got := b.srv.TagValues(client.TagObjectDNSView, 1)[name]
						if fmt.Sprint(got) != fmt.Sprint(values) {
							return fmt.Errorf("backend tag %s = %v, want %v", name, got, values)
						}
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{
			Config: update,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(widgetAddr, "description", "second"),
				resource.TestCheckResourceAttr(widgetAddr, "tags.env", "dev"),
				resource.TestCheckResourceAttr(widgetAddr, "tags_all.env", "dev"),
				resource.TestCheckNoResourceAttr(widgetAddr, "tags.owner"),
				resource.TestCheckNoResourceAttr(widgetAddr, "tags_list.teams.0"),
				func(*terraform.State) error {
					if got := b.srv.TagValues(client.TagObjectDNSView, 1); fmt.Sprint(got) != "map[env:[dev]]" {
						return fmt.Errorf("backend tags = %v", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{ResourceName: widgetAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"deletion_protection", "timeouts", "approval_mode"}},
		resource.TestStep{
			ResourceName: widgetAddr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable,
		},
		resource.TestStep{
			ResourceName: widgetAddr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "alpha", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: []string{"deletion_protection", "timeouts", "approval_mode"},
		},
	)
	if b.count() != 0 {
		t.Errorf("widget was not deleted at the end of the test")
	}
}

func TestWidgetImportUnknownNaturalKey(t *testing.T) {
	b := newWidgetBackend(t)
	cfg := widgetConfig(b, `name = "alpha"
deletion_protection = false`)
	widgetCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: widgetAddr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "missing", nil },
			ExpectError:       regexp.MustCompile(`No object matches "missing"`),
		},
	)
}

func TestWidgetDeletionProtectionBlocksDestroy(t *testing.T) {
	b := newWidgetBackend(t)
	protected := widgetConfig(b, `name = "alpha"`)
	unprotected := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t,
		resource.TestStep{Config: protected, Check: resource.TestCheckResourceAttr(widgetAddr, "deletion_protection", "true")},
		resource.TestStep{Config: acctest.ProviderConfig(b.srv.URL()), ExpectError: regexp.MustCompile(`Deletion protection is enabled`)},
		resource.TestStep{Config: unprotected},
		resource.TestStep{Config: acctest.ProviderConfig(b.srv.URL())},
	)
	if got := len(b.srv.RequestsFor("DELETE", "/api/v1/widgets/1")); got != 1 {
		t.Errorf("DELETE sent %d times; protection must stop the first destroy before any API call", got)
	}
}

func TestWidgetFailedTaskKeepsStateAndIsReplacedNextApply(t *testing.T) {
	b := newWidgetBackend(t)
	b.taskFinal, b.logs = []string{fakeserver.StatusInprogress, fakeserver.StatusIncomplete}, "named-checkzone failed: bad serial"
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)agent task \d+ failed.*named-checkzone failed: bad serial`)},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{fakeserver.StatusCompleted}, "" },
			Config:    cfg,
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("DELETE", "/api/v1/widgets/1")); got != 1 {
					return fmt.Errorf("the tainted widget must have been saved and replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
	)
}

func TestWidgetApprovalFailModeReportsRequestID(t *testing.T) {
	b := newWidgetBackend(t)
	b.approval = func(map[string]any) (fakeserver.Response, bool) {
		return b.srv.ApprovalPending(321, "Widget creation request"), true
	}
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`Approval required \(request 321\)`)})
	if b.count() != 0 {
		t.Errorf("nothing may be created while approval is pending")
	}
}

func TestWidgetApprovalWaitModeAppliesAfterApproval(t *testing.T) {
	b := newWidgetBackend(t)
	b.srv.StageApproval("create_widget", map[string]any{"name": "unrelated"})
	var submitted int64
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if submitted != 0 {
			return fakeserver.Response{}, false
		}
		submitted = b.srv.StageApproval("create_widget", map[string]any{"name": body["name"]}, fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)
		b.mu.Lock()
		w := &widget{ID: b.srv.NextID()}
		b.apply(w, body)
		b.widgets[w.ID] = w
		b.mu.Unlock()
		return b.srv.ApprovalPending(submitted, "Widget creation request"), true
	}
	cfg := widgetConfig(b, `
name = "alpha"
approval_mode = "wait"
deletion_protection = false`)

	widgetCase(t, resource.TestStep{
		Config: cfg,
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor("POST", "/api/v1/widgets/")); got != 1 {
				return fmt.Errorf("POST sent %d times", got)
			}
			return nil
		},
	})
}

func TestWidgetPendingDuplicateIsReportedInsteadOfResubmitted(t *testing.T) {
	b := newWidgetBackend(t)
	pending := b.srv.StageApproval("create_widget", map[string]any{"name": "alpha"})
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)

	widgetCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(fmt.Sprintf(`Approval required \(request %d\)`, pending))})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/widgets/")); got != 0 {
		t.Errorf("a duplicate request must not be submitted, POST count = %d", got)
	}
}

func TestWidgetPendingDuplicateIsAwaitedInWaitMode(t *testing.T) {
	b := newWidgetBackend(t)
	b.srv.StageApproval("create_widget", map[string]any{"name": "alpha"}, fakeserver.RequestPending, fakeserver.RequestPending, fakeserver.RequestApplied)
	b.mu.Lock()
	b.widgets[99] = &widget{ID: 99, Name: "alpha"}
	b.mu.Unlock()
	cfg := widgetConfig(b, `
name = "alpha"
approval_mode = "wait"
deletion_protection = false`)

	widgetCase(t, resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(widgetAddr, "id", idString(99))})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/widgets/")); got != 0 {
		t.Errorf("resumed wait must not file a second request, POST count = %d", got)
	}
}

func TestWidgetUnknownTagFailsAfterSavingState(t *testing.T) {
	b := newWidgetBackend(t)
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false
tags = { nonexistent = "x" }`)

	widgetCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*saved to the Terraform state`)},
		resource.TestStep{Config: widgetConfig(b, `
name = "alpha"
deletion_protection = false`)},
	)
}

func TestWidgetReadRemovesDeletedObject(t *testing.T) {
	b := newWidgetBackend(t)
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)
	widgetCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				b.mu.Lock()
				defer b.mu.Unlock()
				b.widgets = map[int64]*widget{}
			},
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
	)
}

func TestWidgetBackendErrorsCarryDetailAndRequestID(t *testing.T) {
	b := newWidgetBackend(t)
	b.srv.HandleREST(http.MethodPost, "/api/v1/widgets/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.Detail(http.StatusConflict, "Widget name must be unique")
	})
	cfg := widgetConfig(b, `
name = "alpha"
deletion_protection = false`)
	widgetCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)HTTP 409.*Widget name must be unique.*X-Request-ID`)})
}
