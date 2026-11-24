package subnet_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
)

const ipamAddr = "omniddi_ipam_subnet.net"

func coexistConfig(b *backend, name, description string, withDHCP bool) string {
	hcl := fmt.Sprintf(`
resource "omniddi_ipam_subnet" "net" {
  network_addr        = "10.40.0.0"
  prefix              = 24
  name                = %q
  description         = %q
  owner               = "netops"
  deletion_protection = false
}
`, name, description)
	if withDHCP {
		hcl += `
resource "omniddi_dhcp_subnet" "test" {
  subnet_id           = omniddi_ipam_subnet.net.id
  server_group_id     = 7
  lease_time          = 60
  deletion_protection = false
}
`
	}
	return dhcptest.Config(b.srv, hcl)
}

func TestCoexistsWithTheIPAMSubnetResource(t *testing.T) {
	b := newBackend(t)
	var id int64
	both := coexistConfig(b, "lab", "one", true)
	run(t,
		resource.TestStep{
			Config: both,
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error {
					id, _ = strconv.ParseInt(s.RootModule().Resources[ipamAddr].Primary.ID, 10, 64)
					return nil
				},
				resource.TestCheckResourceAttrPair(addr, "subnet_id", ipamAddr, "id"),
				resource.TestCheckResourceAttrPair(addr, "id", ipamAddr, "id"),
				resource.TestCheckResourceAttr(addr, "cidr", "10.40.0.0/24"),
				func(*terraform.State) error {
					if node, ok := b.ipam.Get(id); !ok || node.SubnetID == nil {
						return fmt.Errorf("the IPAM node does not serve DHCP: %+v", node)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: both, PlanOnly: true},
		resource.TestStep{
			Config: coexistConfig(b, "lab-renamed", "two", true),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(ipamAddr, plancheck.ResourceActionUpdate),
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionNoop),
			}},
			Check: func(*terraform.State) error {
				promotes := 0
				for _, body := range requestBodies(b.srv, http.MethodPut, promotePathFor(id)) {
					if _, sent := body["server_group_id"]; sent {
						promotes++
					}
				}
				if promotes != 1 {
					return fmt.Errorf("server_group_id was sent %d times; only the DHCP resource may send it, once", promotes)
				}
				if cfg, ok := b.config(id); !ok || cfg.Config["lease_time"] != float64(60) {
					return fmt.Errorf("renaming the IPAM subnet disturbed the DHCP settings: %+v", cfg)
				}
				return nil
			},
		},
		resource.TestStep{Config: coexistConfig(b, "lab-renamed", "two", true), PlanOnly: true},
		resource.TestStep{
			Config: coexistConfig(b, "lab-renamed", "two", false),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(ipamAddr, plancheck.ResourceActionNoop),
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroy),
			}},
			Check: func(s *terraform.State) error {
				node, ok := b.ipam.Get(id)
				if !ok || node.SubnetID != nil || node.Label != "lab-renamed" {
					return fmt.Errorf("destroying the DHCP subnet must leave the IPAM subnet planning-only and intact: %+v", node)
				}
				return resource.TestCheckResourceAttr(ipamAddr, "name", "lab-renamed")(s)
			},
		},
		resource.TestStep{Config: coexistConfig(b, "lab-renamed", "two", false), PlanOnly: true},
		resource.TestStep{Config: coexistConfig(b, "lab-renamed", "two", true)},
		resource.TestStep{Config: dhcptest.Config(b.srv, "")},
	)
	if _, ok := b.ipam.Get(id); ok {
		t.Error("the IPAM subnet survived the final destroy")
	}
	if got := len(b.srv.RequestsFor(http.MethodDelete, settingsPathFor(id))); got != 0 {
		t.Errorf("DELETE /dhcp/subnets/{id} was called %d times", got)
	}
}
