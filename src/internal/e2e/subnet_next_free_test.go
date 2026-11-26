package e2e_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type nextFreeFixture struct {
	name  string
	block int
}

func (f nextFreeFixture) container() string { return fmt.Sprintf("198.18.%d.0/24", f.block) }

func (f nextFreeFixture) config() string {
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

resource "omniddi_ipam_subnet" "first" {
  parent_id           = omniddi_ipam_container.test.id
  prefix              = 26
  name                = "%[1]s-first"
  exclude             = ["198.18.%[2]d.64/26"]
  deletion_protection = false
  depends_on          = [omniddi_ipam_subnet.fixed]
}

resource "omniddi_ipam_subnet" "second" {
  parent_id           = omniddi_ipam_container.test.id
  prefix              = 27
  name                = "%[1]s-second"
  deletion_protection = false
  depends_on          = [omniddi_ipam_subnet.fixed]
}
`, f.name, f.block)
}

func allocatedApart(container string, exclude string, addrs ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		outer, skip := netip.MustParsePrefix(container), netip.MustParsePrefix(exclude)
		var seen []netip.Prefix
		for _, a := range addrs {
			p, err := netip.ParsePrefix(s.RootModule().Resources[a].Primary.Attributes["cidr"])
			if err != nil {
				return fmt.Errorf("%s cidr: %w", a, err)
			}
			if !outer.Contains(p.Addr()) || p.Bits() <= outer.Bits() {
				return fmt.Errorf("%s got %s, outside container %s", a, p, outer)
			}
			if p.Overlaps(skip) && a != "omniddi_ipam_subnet.second" {
				return fmt.Errorf("%s got %s, inside excluded %s", a, p, skip)
			}
			for _, o := range seen {
				if p.Overlaps(o) {
					return fmt.Errorf("%s got %s, overlapping %s", a, p, o)
				}
			}
			seen = append(seen, p)
		}
		return nil
	}
}

func TestAccIPAM_SubnetNextFree(t *testing.T) {
	f := nextFreeFixture{name: uniqueName(), block: octet()}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:           f.config(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.fixed", "cidr", fmt.Sprintf("198.18.%d.0/26", f.block)),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.first", "status", "ACTIVE"),
					resource.TestCheckResourceAttrPair("omniddi_ipam_subnet.first", "parent_id", "omniddi_ipam_container.test", "id"),
					allocatedApart(f.container(), fmt.Sprintf("198.18.%d.64/26", f.block),
						"omniddi_ipam_subnet.fixed", "omniddi_ipam_subnet.first", "omniddi_ipam_subnet.second"),
				),
			},
			{
				ResourceName: "omniddi_ipam_subnet.first", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: append(commonImportIgnore, "exclude"),
			},
		},
	})
}
