package subnet_test

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

const lanBody = `
network_addr        = "192.0.2.0"
prefix              = 24
deletion_protection = false`

func TestSubnetInPlaceUpdateKeepsTagsAllAndStableValuesInThePlan(t *testing.T) {
	b := ipamtest.New(t)
	before := config(b, lanBody+"\nowner = \"netops\"\ntags = { owner = \"netops\" }")
	after := config(b, lanBody+"\nowner = \"secops\"\ntags = { owner = \"netops\" }")

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
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("cidr"), knownvalue.StringExact("192.0.2.0/24")),
				plancheck.ExpectUnknownValue(addr, tfjsonpath.New("status")),
			}},
		},
		resource.TestStep{Config: after, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}

func TestSubnetIPv6RespellingDoesNotReplace(t *testing.T) {
	b := ipamtest.New(t)
	cfg := func(network string) string {
		return config(b, fmt.Sprintf("network_addr = %q\nprefix = 48\ndeletion_protection = false", network))
	}
	run(t,
		resource.TestStep{Config: cfg("2001:db8:1::")},
		resource.TestStep{
			Config:           cfg("2001:DB8:1:0::"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{expectNoReplace{address: addr}}},
		},
	)
	if got := len(b.Srv.RequestsFor("POST", "/api/v1/ipam/subnets")); got != 1 {
		t.Errorf("POST sent %d times; a different spelling of the same address must not recreate the subnet", got)
	}
}

func TestSubnetCreateAdoptsTheObjectAfterAGatewayError(t *testing.T) {
	b := ipamtest.New(t)
	b.SubnetBody = func(map[string]any) (fakeserver.Response, bool) {
		b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "192.0.2.0/24", Label: "192.0.2.0/24"})
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out"), true
	}

	run(t, resource.TestStep{
		Config: config(b, lanBody),
		Check:  resource.TestCheckResourceAttr(addr, "cidr", "192.0.2.0/24"),
	})
	if got := len(b.Srv.RequestsFor("POST", "/api/v1/ipam/subnets")); got != 1 {
		t.Errorf("POST sent %d times; the lost response must not trigger a duplicate create", got)
	}
}

func TestSubnetCreateReportsTheGatewayErrorWhenNothingWasCreated(t *testing.T) {
	b := ipamtest.New(t)
	b.SubnetBody = func(map[string]any) (fakeserver.Response, bool) {
		return fakeserver.Detail(http.StatusBadGateway, "upstream timed out"), true
	}

	run(t, resource.TestStep{Config: config(b, lanBody), ExpectError: regexp.MustCompile(`(?s)Unable to create IPAM subnet.*upstream timed out`)})
	if b.Count() != 0 {
		t.Errorf("nothing may exist after a failed create that left no object behind")
	}
}

func TestSubnetFailedTagsAfterCreateTaintsAndLowersDeletionProtection(t *testing.T) {
	b := ipamtest.New(t)
	body := "network_addr = \"192.0.2.0\"\nprefix = 24"
	badTag := config(b, body+"\ntags = { nonexistent = \"x\" }")
	protected := config(b, body)
	unprotected := config(b, body+"\ndeletion_protection = false")

	run(t,
		resource.TestStep{
			Config:      badTag,
			ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found.*tainted.*deletion_protection was set to false`),
		},
		resource.TestStep{
			Config: protected,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: func(*terraform.State) error {
				if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/subnets/3")); got != 1 {
					return fmt.Errorf("the tainted subnet must be replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: unprotected},
	)
}

func TestSubnetFailedTagsAfterUpdateRestoresThePriorState(t *testing.T) {
	b := ipamtest.New(t)
	good := config(b, lanBody+"\nowner = \"netops\"")
	badTag := config(b, lanBody+"\nowner = \"secops\"\ntags = { nonexistent = \"x\" }")
	applied := config(b, lanBody+"\nowner = \"secops\"")

	run(t,
		resource.TestStep{Config: good},
		resource.TestStep{Config: badTag, ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*previous values`)},
		resource.TestStep{Config: applied, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	)
}
