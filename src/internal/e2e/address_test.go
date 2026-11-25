package e2e_test

import (
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	addressAddr = "omniddi_ipam_address.web"
	recordAddr  = "omniddi_dns_record.web"
)

type addressFixture struct {
	view  string
	name  string
	block int
}

func (f addressFixture) zone() string { return f.name + ".test" }

func (f addressFixture) cidr() string { return fmt.Sprintf("198.19.%d.0/24", f.block) }

func (f addressFixture) base() string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_dns_view" "test" {
  name = %[1]q
}

data "omniddi_server_group" "dhcp" {
  name = %[2]q
}

resource "omniddi_dns_zone" "test" {
  view_id             = data.omniddi_dns_view.test.id
  name                = %[3]q
  ttl                 = 3600
  email               = "hostmaster@%[3]s"
  deletion_protection = false
}

resource "omniddi_ipam_subnet" "test" {
  network_addr        = "198.19.%[5]d.0"
  prefix              = 24
  name                = "%[4]s"
  deletion_protection = false
}

resource "omniddi_dhcp_subnet" "test" {
  subnet_id           = omniddi_ipam_subnet.test.id
  server_group_id     = data.omniddi_server_group.dhcp.id
  deletion_protection = false
  force_destroy       = true
}
`, f.view, dhcpGroupName(), f.zone(), f.name, f.block)
}

func (f addressFixture) withAddress() string {
	return f.base() + fmt.Sprintf(`
resource "omniddi_ipam_address" "web" {
  subnet_id = omniddi_dhcp_subnet.test.id
  name      = "web.%[1]s"
  exclude   = ["198.19.%[2]d.1"]
}

resource "omniddi_dns_record" "web" {
  zone_id = omniddi_dns_zone.test.id
  name    = "web"
  type    = "A"
  values  = [omniddi_ipam_address.web.ip_address]
}
`, f.zone(), f.block)
}

func allocatedInSubnet(cidr string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ip := s.RootModule().Resources[addressAddr].Primary.Attributes["ip_address"]
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return fmt.Errorf("ip_address %q: %w", ip, err)
		}
		if !netip.MustParsePrefix(cidr).Contains(addr) || addr == netip.MustParsePrefix(cidr).Addr().Next() {
			return fmt.Errorf("allocated %s, want a host in %s other than the excluded first address", addr, cidr)
		}
		return nil
	}
}

func addressKey(cidr string) func(*terraform.State) (string, error) {
	return func(s *terraform.State) (string, error) {
		return cidr + "/" + s.RootModule().Resources[addressAddr].Primary.Attributes["ip_address"], nil
	}
}

func TestAccIPAM_AddressPublishedInDNS(t *testing.T) {
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance tests skipped unless env %q is set", resource.EnvTfAcc)
	}
	f := addressFixture{view: dnsViewName(t), name: uniqueName(), block: octet()}
	settle := zoneSettle()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: f.base(), ConfigPlanChecks: emptyPlanAfterApply()},
			{
				PreConfig:        func() { time.Sleep(settle) },
				Config:           f.withAddress(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					allocatedInSubnet(f.cidr()),
					resource.TestCheckResourceAttr(addressAddr, "subnet_cidr", f.cidr()),
					resource.TestCheckResourceAttr(addressAddr, "status", "Used"),
					resource.TestCheckResourceAttrPair(recordAddr, "values.0", addressAddr, "ip_address"),
				),
			},
			{ResourceName: addressAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"exclude", "status", "timeouts"}},
			{
				ResourceName: addressAddr, ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: addressKey(f.cidr()), ImportStateVerifyIgnore: []string{"exclude", "status", "timeouts"},
			},
		},
	})
}
