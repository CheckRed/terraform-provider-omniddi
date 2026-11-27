package e2e_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func nestedContainers(name string, block int, leafParent string) string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_ipam_node" "root" {
  protocol = "IPv4"
}

resource "omniddi_ipam_container" "top" {
  parent_id           = data.omniddi_ipam_node.root.id
  name                = "%[1]s-top"
  cidr                = "198.18.%[2]d.0/23"
  deletion_protection = false
}

resource "omniddi_ipam_container" "mid" {
  parent_id           = omniddi_ipam_container.top.id
  name                = "%[1]s-mid"
  cidr                = "198.18.%[2]d.0/24"
  deletion_protection = false
}

resource "omniddi_ipam_container" "leaf" {
  parent_id           = %[3]s
  name                = "%[1]s-leaf"
  cidr                = "198.18.%[2]d.0/25"
  deletion_protection = false
  depends_on          = [omniddi_ipam_container.mid]
}
`, name, block, leafParent)
}

func TestAccIPAM_ContainerNeedsImmediateParent(t *testing.T) {
	name, block := uniqueName(), octet()&^1

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      nestedContainers(name, block, "omniddi_ipam_container.top.id"),
				ExpectError: regexp.MustCompile(`parent_id is not the immediate parent`),
			},
			{
				Config:           nestedContainers(name, block, "omniddi_ipam_container.mid.id"),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check:            resource.TestCheckResourceAttrPair("omniddi_ipam_container.leaf", "parent_id", "omniddi_ipam_container.mid", "id"),
			},
		},
	})
}
