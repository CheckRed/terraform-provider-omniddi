package e2e_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func nextFreeLookupConfig(name string, block int) string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_ipam_node" "root" {
  protocol = "IPv4"
}

resource "omniddi_ipam_container" "test" {
  parent_id           = data.omniddi_ipam_node.root.id
  name                = "%[1]s"
  cidr                = "198.18.%[2]d.0/24"
  deletion_protection = false
}

resource "omniddi_ipam_subnet" "fixed" {
  parent_id           = omniddi_ipam_container.test.id
  network_addr        = "198.18.%[2]d.0"
  prefix              = 26
  name                = "%[1]s-fixed"
  deletion_protection = false
}

data "omniddi_ipam_next_free_subnet" "next" {
  container_id  = omniddi_ipam_container.test.id
  prefix_length = 26
  depends_on    = [omniddi_ipam_subnet.fixed]
}

data "omniddi_ipam_next_free_subnet" "excluded" {
  container_cidr = omniddi_ipam_container.test.cidr
  prefix_length  = 26
  exclude        = ["198.18.%[2]d.64/26"]
  depends_on     = [omniddi_ipam_subnet.fixed]
}

data "omniddi_ipam_next_free_subnet" "none" {
  container_id  = omniddi_ipam_container.test.id
  prefix_length = 25
  exclude       = ["198.18.%[2]d.128/25"]
  depends_on    = [omniddi_ipam_subnet.fixed]
}

data "omniddi_ipam_next_free_ip" "ips" {
  subnet_id = omniddi_ipam_subnet.fixed.id
  quantity  = 3
  exclude   = ["198.18.%[2]d.1"]
}

data "omniddi_ipam_next_free_ip" "again" {
  subnet_cidr = omniddi_ipam_subnet.fixed.cidr
  quantity    = 3
  exclude     = ["198.18.%[2]d.1"]
}

data "omniddi_ipam_next_free_ip" "too_many" {
  subnet_id = omniddi_ipam_subnet.fixed.id
  quantity  = 100
}
`, name, block)
}

func freeAddressesIn(cidr, excluded string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs := s.RootModule().Resources["data.omniddi_ipam_next_free_ip.ips"].Primary.Attributes
		subnet, skip := netip.MustParsePrefix(cidr), netip.MustParseAddr(excluded)
		for i := range 3 {
			a, err := netip.ParseAddr(attrs[fmt.Sprintf("addresses.%d", i)])
			if err != nil {
				return fmt.Errorf("addresses.%d: %w", i, err)
			}
			if !subnet.Contains(a) || a == skip {
				return fmt.Errorf("addresses.%d = %s, want a host in %s other than %s", i, a, subnet, skip)
			}
		}
		return nil
	}
}

func TestAccIPAM_NextFreeLookups(t *testing.T) {
	name, block := uniqueName(), octet()
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:           nextFreeLookupConfig(name, block),
			ConfigPlanChecks: emptyPlanAfterApply(),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_subnet.next", "cidr", fmt.Sprintf("198.18.%d.64/26", block)),
				resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_subnet.next", "network_addr", fmt.Sprintf("198.18.%d.64", block)),
				resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_subnet.excluded", "cidr", fmt.Sprintf("198.18.%d.128/26", block)),
				resource.TestCheckResourceAttrPair("data.omniddi_ipam_next_free_subnet.excluded", "container_id", "omniddi_ipam_container.test", "id"),
				resource.TestCheckNoResourceAttr("data.omniddi_ipam_next_free_subnet.none", "cidr"),
				resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_ip.ips", "addresses.#", "3"),
				freeAddressesIn(fmt.Sprintf("198.18.%d.0/26", block), fmt.Sprintf("198.18.%d.1", block)),
				resource.TestCheckResourceAttrPair("data.omniddi_ipam_next_free_ip.ips", "addresses.0", "data.omniddi_ipam_next_free_ip.again", "addresses.0"),
				resource.TestCheckResourceAttrPair("data.omniddi_ipam_next_free_ip.ips", "ip_address", "data.omniddi_ipam_next_free_ip.again", "ip_address"),
				resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_ip.too_many", "addresses.#", "0"),
			),
		}},
	})
}
