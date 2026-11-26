package e2e_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func ipv6Config(name string, block int) string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_ipam_node" "root" {
  protocol = "IPv6"
}

resource "omniddi_ipam_container" "test" {
  parent_id           = data.omniddi_ipam_node.root.id
  name                = "%[1]s"
  cidr                = "2001:db8:%[2]x::/48"
  deletion_protection = false
}

resource "omniddi_ipam_subnet" "fixed" {
  parent_id           = omniddi_ipam_container.test.id
  network_addr        = "2001:db8:%[2]x::"
  prefix              = 64
  name                = "%[1]s-fixed"
  deletion_protection = false
}

resource "omniddi_ipam_subnet" "allocated" {
  parent_id           = omniddi_ipam_container.test.id
  prefix              = 64
  name                = "%[1]s-allocated"
  deletion_protection = false
  depends_on          = [omniddi_ipam_subnet.fixed]
}
`, name, block)
}

func TestAccIPAM_IPv6SubnetsWithoutProtocol(t *testing.T) {
	name, block := uniqueName(), 0x1000+octet()
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:           ipv6Config(name, block),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.fixed", "protocol", "IPv6"),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.fixed", "cidr", fmt.Sprintf("2001:db8:%x::/64", block)),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.allocated", "protocol", "IPv6"),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.allocated", "cidr", fmt.Sprintf("2001:db8:%x:1::/64", block)),
				),
			},
			{ResourceName: "omniddi_ipam_subnet.fixed", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: commonImportIgnore},
		},
	})
}
