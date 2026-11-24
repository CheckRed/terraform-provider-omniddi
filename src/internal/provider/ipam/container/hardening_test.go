package container_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
)

type expectNoReplace struct{ address string }

func (c expectNoReplace) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	for _, change := range req.Plan.ResourceChanges {
		if change.Address == c.address && change.Change.Actions.Delete() {
			resp.Error = fmt.Errorf("%s would be replaced: %v", c.address, change.Change.Actions)
		}
	}
}

const corpBody = `
parent_id           = 1
name                = "corp"
cidr                = "10.0.0.0/16"
deletion_protection = false`

func TestContainerInPlaceUpdateKeepsTagsAllAndStableValuesInThePlan(t *testing.T) {
	b := ipamtest.New(t)
	before := config(b, corpBody+"\nowner = \"netops\"\ntags = { owner = \"netops\" }")
	after := config(b, corpBody+"\nowner = \"secops\"\ntags = { owner = \"netops\" }")

	run(t,
		resource.TestStep{Config: before},
		resource.TestStep{
			Config: after,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("created_on"), knownvalue.StringExact("2026-01-01T00:00:00")),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("protocol"), knownvalue.StringExact("IPv4")),
				plancheck.ExpectUnknownValue(addr, tfjsonpath.New("status")),
			}},
		},
		resource.TestStep{Config: after, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}

func TestContainerIPv6RespellingDoesNotReplace(t *testing.T) {
	b := ipamtest.New(t)
	cfg := func(cidr string) string {
		return config(b, fmt.Sprintf("parent_id = 2\nname = \"v6\"\ncidr = %q\ndeletion_protection = false", cidr))
	}
	run(t,
		resource.TestStep{Config: cfg("2001:db8::/32")},
		resource.TestStep{
			Config: cfg("2001:DB8:0:0::/32"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				expectNoReplace{address: addr},
			}},
		},
	)
	if got := len(b.Srv.RequestsFor("POST", "/api/v1/ipam/containers")); got != 1 {
		t.Errorf("POST sent %d times; a different spelling of the same CIDR must not recreate the container", got)
	}
}

func TestContainerCreateAdoptsTheObjectAfterAGatewayError(t *testing.T) {
	b := ipamtest.New(t)
	b.Srv.HandleREST(http.MethodPost, "/api/v1/ipam/containers", func(*fakeserver.Request) fakeserver.Response {
		b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/16", Label: "corp"})
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out")
	})

	run(t, resource.TestStep{
		Config: config(b, corpBody),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "name", "corp"),
			resource.TestCheckResourceAttr(addr, "cidr", "10.0.0.0/16"),
		),
	})
	if got := len(b.Srv.RequestsFor("POST", "/api/v1/ipam/containers")); got != 1 {
		t.Errorf("POST sent %d times; the lost response must not trigger a duplicate create", got)
	}
}

func TestContainerCreateReportsTheGatewayErrorWhenNothingWasCreated(t *testing.T) {
	b := ipamtest.New(t)
	b.Srv.HandleREST(http.MethodPost, "/api/v1/ipam/containers", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out")
	})

	run(t, resource.TestStep{Config: config(b, corpBody), ExpectError: regexp.MustCompile(`(?s)Unable to create IPAM container.*upstream timed out`)})
	if b.Count() != 0 {
		t.Errorf("nothing may exist after a failed create that left no object behind")
	}
}

func TestContainerFailedTagsAfterCreateTaintsAndLowersDeletionProtection(t *testing.T) {
	b := ipamtest.New(t)
	body := "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\""
	protectedBadTag := config(b, body+"\ntags = { nonexistent = \"x\" }")
	protected := config(b, body)
	unprotected := config(b, body+"\ndeletion_protection = false")

	run(t,
		resource.TestStep{
			Config:      protectedBadTag,
			ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found.*tainted.*deletion_protection was set to false`),
		},
		resource.TestStep{
			Config: protected,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: func(*terraform.State) error {
				if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/containers/3")); got != 1 {
					return fmt.Errorf("the tainted container must be replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: unprotected},
	)
}

func TestContainerFailedTagsAfterUpdateRestoresThePriorState(t *testing.T) {
	b := ipamtest.New(t)
	good := config(b, corpBody+"\nowner = \"netops\"")
	badTag := config(b, corpBody+"\nowner = \"secops\"\ntags = { nonexistent = \"x\" }")
	applied := config(b, corpBody+"\nowner = \"secops\"")

	run(t,
		resource.TestStep{Config: good},
		resource.TestStep{Config: badTag, ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*previous values`)},
		resource.TestStep{Config: applied, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}
