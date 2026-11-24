package e2e_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

type dhcpFixture struct {
	name      string
	block     int
	poolEnd   int
	leaseTime int
	label     string
}

func (f dhcpFixture) config() string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_server_group" "dhcp" {
  name = %[1]q
}

resource "omniddi_ipam_subnet" "test" {
  network_addr        = "198.19.%[3]d.0"
  prefix              = 24
  name                = "%[2]s"
  deletion_protection = false
}

resource "omniddi_dhcp_subnet" "test" {
  subnet_id           = omniddi_ipam_subnet.test.id
  server_group_id     = data.omniddi_server_group.dhcp.id
  lease_time          = %[5]d
  lease_time_unit     = "hrs"
  deletion_protection = false
  force_destroy       = true

  option_data = [
    { code = 3, data = "198.19.%[3]d.1" },
    { code = 15, data = "e2e.test" },
  ]

  pools = [
    { pool = "198.19.%[3]d.100-198.19.%[3]d.%[4]d", name = "dynamic" },
  ]
}

resource "omniddi_dhcp_host_reservation" "test" {
  name            = "%[2]s-host"
  subnet_id       = omniddi_dhcp_subnet.test.id
  ip_address      = "198.19.%[3]d.50"
  identifier_type = "Mac Address"
  identifier      = "02:00:00:00:%02[3]x:50"
  description     = %[6]q
}
`, dhcpGroupName(), f.name, f.block, f.poolEnd, f.leaseTime, f.label)
}

func TestAccDHCP_SubnetReservationLifecycle(t *testing.T) {
	f := dhcpFixture{name: uniqueName(), block: octet(), poolEnd: 150, leaseTime: 8, label: "created by terraform e2e"}
	updated := f
	updated.poolEnd, updated.leaseTime, updated.label = 180, 4, "updated by terraform e2e"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:           f.config(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_dhcp_subnet.test", "cidr", fmt.Sprintf("198.19.%d.0/24", f.block)),
					resource.TestCheckResourceAttr("omniddi_dhcp_subnet.test", "pools.#", "1"),
					resource.TestCheckResourceAttrSet("omniddi_dhcp_subnet.test", "kea_subnet_id"),
					resource.TestCheckResourceAttrPair("omniddi_dhcp_subnet.test", "id", "omniddi_ipam_subnet.test", "id"),
					resource.TestCheckResourceAttr("omniddi_dhcp_host_reservation.test", "ip_address", fmt.Sprintf("198.19.%d.50", f.block)),
				),
			},
			{
				Config:           updated.config(),
				ConfigPlanChecks: updatesInPlace("omniddi_dhcp_subnet.test", "omniddi_dhcp_host_reservation.test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_dhcp_subnet.test", "lease_time", "4"),
					resource.TestCheckResourceAttr("omniddi_dhcp_subnet.test", "pools.0.pool", fmt.Sprintf("198.19.%d.100-198.19.%d.180", f.block, f.block)),
					resource.TestCheckResourceAttr("omniddi_dhcp_host_reservation.test", "description", updated.label),
				),
			},
			{ResourceName: "omniddi_dhcp_subnet.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: commonImportIgnore},
			{ResourceName: "omniddi_dhcp_host_reservation.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append(commonImportIgnore, "options")},
		},
	})
}
