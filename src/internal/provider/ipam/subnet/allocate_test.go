package subnet_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
)

func seedContainer(b *ipamtest.Backend, cidr string) int64 {
	return b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: cidr})
}

func lookups(b *ipamtest.Backend, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := len(b.Srv.RequestsFor(http.MethodGet, ipamtest.NextFreeSubnetPath)); got != want {
			return fmt.Errorf("next-free-subnet lookups = %d, want %d", got, want)
		}
		return nil
	}
}

func TestSubnetAllocatesNextFreeBlock(t *testing.T) {
	b := ipamtest.New(t)
	container := seedContainer(b, "10.20.0.0/16")
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.20.0.0/24"})
	create := plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id           = %d
  prefix              = 24
  exclude             = ["10.20.1.0/24"]
  deletion_protection = false
}
`, container))
	changedExclude := plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id           = %d
  prefix              = 24
  exclude             = ["10.20.1.0/24", "10.20.2.0/24"]
  deletion_protection = false
}
`, container))

	run(t,
		resource.TestStep{
			Config: create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "cidr", "10.20.2.0/24"),
				resource.TestCheckResourceAttr(addr, "network_addr", "10.20.2.0"),
				resource.TestCheckResourceAttr(addr, "name", "10.20.2.0/24"),
				resource.TestCheckResourceAttr(addr, "protocol", "IPv4"),
				resource.TestCheckResourceAttr(addr, "parent_id", fmt.Sprint(container)),
				lookups(b, 1),
				func(*terraform.State) error {
					q := b.Srv.RequestsFor(http.MethodGet, ipamtest.NextFreeSubnetPath)[0].Query
					if q.Get("container_id") != fmt.Sprint(container) || q.Get("prefix_length") != "24" || !slices.Equal(q["exclude"], []string{"10.20.1.0/24"}) {
						return fmt.Errorf("lookup query = %v", q)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{
			Config: changedExclude,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "cidr", "10.20.2.0/24"),
				lookups(b, 1),
			),
		},
		resource.TestStep{Config: changedExclude, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append([]string{"exclude"}, ignoreOnImport...)},
	)
}

func TestSubnetAllocationIPv6(t *testing.T) {
	b := ipamtest.New(t)
	container := seedContainer(b, "2001:db8::/48")
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "2001:db8::/64"})
	run(t, resource.TestStep{
		Config: plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id           = %d
  prefix              = 64
  deletion_protection = false
}
`, container)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "cidr", "2001:db8:0:1::/64"),
			resource.TestCheckResourceAttr(addr, "protocol", "IPv6"),
		),
	})
}

func TestSubnetAllocationRetriesWhenBlockIsTaken(t *testing.T) {
	b := ipamtest.New(t)
	container := seedContainer(b, "10.30.0.0/16")
	stolen := 0
	b.SubnetBody = func(body map[string]any) (fakeserver.Response, bool) {
		if stolen < 2 {
			stolen++
			b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: fmt.Sprintf("%v/%v", body["network_addr"], body["prefix"])})
		}
		return fakeserver.Response{}, false
	}
	run(t, resource.TestStep{
		Config: plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id           = %d
  prefix              = 24
  deletion_protection = false
}
`, container)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "cidr", "10.30.2.0/24"),
			lookups(b, 3),
		),
	})
}

func TestSubnetAllocationGivesUpWhenAlwaysTaken(t *testing.T) {
	b := ipamtest.New(t)
	container := seedContainer(b, "10.31.0.0/16")
	b.SubnetBody = func(body map[string]any) (fakeserver.Response, bool) {
		b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: fmt.Sprintf("%v/%v", body["network_addr"], body["prefix"])})
		return fakeserver.Response{}, false
	}
	run(t, resource.TestStep{
		Config: plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id = %d
  prefix    = 24
}
`, container)),
		ExpectError: regexp.MustCompile(`(?s)another client took each free /24.*3 attempts`),
	})
}

func TestSubnetAllocationDoesNotRetryOtherErrors(t *testing.T) {
	b := ipamtest.New(t)
	container := seedContainer(b, "10.32.0.0/16")
	b.SubnetBody = func(map[string]any) (fakeserver.Response, bool) {
		return fakeserver.Detail(http.StatusBadRequest, "name is reserved"), true
	}
	run(t, resource.TestStep{
		Config: plain(b, fmt.Sprintf(`
resource "omniddi_ipam_subnet" "test" {
  parent_id = %d
  prefix    = 24
}
`, container)),
		ExpectError: regexp.MustCompile(`name is reserved`),
		Check:       lookups(b, 2),
	})
}

func TestSubnetAllocationErrors(t *testing.T) {
	b := ipamtest.New(t)
	full := seedContainer(b, "10.40.0.0/24")
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.40.0.0/24"})
	cfg := func(body string) string {
		return plain(b, "resource \"omniddi_ipam_subnet\" \"test\" {\n"+body+"\n}\n")
	}
	run(t,
		resource.TestStep{Config: cfg(`prefix = 24`), ExpectError: regexp.MustCompile(`(?s)set network_addr, or set parent_id`)},
		resource.TestStep{Config: cfg(`
  network_addr = "192.0.2.0"
  prefix       = 24
  exclude      = ["192.0.2.0/25"]`), ExpectError: regexp.MustCompile(`(?s)exclude only applies when network_addr is not set`)},
		resource.TestStep{Config: cfg(`
  parent_id = 1
  prefix    = 24`), ExpectError: regexp.MustCompile(`(?s)NOT_A_CONTAINER|not a container`)},
		resource.TestStep{Config: cfg(fmt.Sprintf(`
  parent_id = %d
  prefix    = 26`, full)), ExpectError: regexp.MustCompile(`(?s)No free /26`)},
		resource.TestStep{Config: cfg(fmt.Sprintf(`
  parent_id = %d
  prefix    = 20`, full)), ExpectError: regexp.MustCompile(`(?s)prefix_length must be longer`)},
	)
}
