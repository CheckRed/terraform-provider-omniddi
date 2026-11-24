package e2e_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSources_PlatformReads(t *testing.T) {
	config := providerBlock() + fmt.Sprintf(`
data "omniddi_network_spaces" "all" {}
data "omniddi_server_groups" "all" {}
data "omniddi_dns_views" "all" {}
data "omniddi_ipam_containers" "all" {}

data "omniddi_server_group" "dns" {
  name = %q
}

data "omniddi_server_group" "dhcp" {
  name = %q
}

data "omniddi_ipam_node" "root" {
  protocol = "IPv4"
}
`, dnsGroupName(), dhcpGroupName())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.omniddi_network_spaces.all", "network_spaces.0.id"),
				resource.TestCheckResourceAttrSet("data.omniddi_server_groups.all", "server_groups.0.id"),
				resource.TestCheckResourceAttr("data.omniddi_server_group.dns", "type", "dns"),
				resource.TestCheckResourceAttr("data.omniddi_server_group.dhcp", "type", "dhcp"),
				resource.TestCheckResourceAttrSet("data.omniddi_server_group.dhcp", "servers.0.server_id"),
				resource.TestCheckResourceAttr("data.omniddi_ipam_node.root", "type", "FAMILY"),
			),
		}},
	})
}
