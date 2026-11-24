package e2e_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

type ipamFixture struct {
	name   string
	block  int
	label  string
	vlanID int
}

func (f ipamFixture) config() string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_ipam_node" "root" {
  protocol = "IPv4"
}

resource "omniddi_ipam_container" "test" {
  parent_id           = data.omniddi_ipam_node.root.id
  name                = "%[1]s"
  cidr                = "198.18.%[2]d.0/24"
  description         = %[3]q
  owner               = "terraform-e2e"
  deletion_protection = false
}

resource "omniddi_ipam_subnet" "test" {
  parent_id           = omniddi_ipam_container.test.id
  network_addr        = "198.18.%[2]d.64"
  prefix              = 26
  name                = "%[1]s-subnet"
  description         = %[3]q
  vlan_id             = %[4]d
  deletion_protection = false
}
`, f.name, f.block, f.label, f.vlanID)
}

func TestAccIPAM_ContainerSubnetLifecycle(t *testing.T) {
	f := ipamFixture{name: uniqueName(), block: octet(), label: "created by terraform e2e", vlanID: 110}
	updated := f
	updated.label, updated.vlanID = "updated by terraform e2e", 120

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:           f.config(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_ipam_container.test", "cidr", fmt.Sprintf("198.18.%d.0/24", f.block)),
					resource.TestCheckResourceAttr("omniddi_ipam_container.test", "protocol", "IPv4"),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.test", "cidr", fmt.Sprintf("198.18.%d.64/26", f.block)),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.test", "status", "ACTIVE"),
				),
			},
			{
				Config:           updated.config(),
				ConfigPlanChecks: updatesInPlace("omniddi_ipam_container.test", "omniddi_ipam_subnet.test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.test", "description", updated.label),
					resource.TestCheckResourceAttr("omniddi_ipam_subnet.test", "vlan_id", "120"),
				),
			},
			{ResourceName: "omniddi_ipam_container.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(commonImportIgnore, "reparent_children_on_destroy")},
			{ResourceName: "omniddi_ipam_subnet.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(commonImportIgnore, "vlan", "vlan_id")},
		},
	})
}
