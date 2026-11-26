package nextfree_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nextfree"
)

const (
	ipDS     = "data.omniddi_ipam_next_free_ip.test"
	subnetDS = "data.omniddi_ipam_next_free_subnet.test"
)

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ipamtest.Factories(nil, nextfree.DataSources()),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

func hcl(b *ipamtest.Backend, body string) string { return acctest.ProviderConfig(b.Srv.URL()) + body }

func nothingAllocated(b *ipamtest.Backend, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := len(b.Addresses()); got != want {
			return fmt.Errorf("address rows = %d, want %d: a lookup must never reserve", got, want)
		}
		if got := b.Allocations(); got != 0 {
			return fmt.Errorf("allocate calls = %d, want 0", got)
		}
		return nil
	}
}

func TestNextFreeIP(t *testing.T) {
	b := ipamtest.New(t)
	subnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "192.0.2.0/29"})
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.1", Hostname: "gw"})
	run(t, resource.TestStep{
		Config: hcl(b, fmt.Sprintf(`
data "omniddi_ipam_next_free_ip" "test" {
  subnet_id = %d
  quantity  = 2
  exclude   = ["192.0.2.2"]
}

data "omniddi_ipam_next_free_ip" "by_cidr" {
  subnet_cidr = "192.0.2.0/29"
}
`, subnet)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(ipDS, "addresses.#", "2"),
			resource.TestCheckResourceAttr(ipDS, "addresses.0", "192.0.2.3"),
			resource.TestCheckResourceAttr(ipDS, "addresses.1", "192.0.2.4"),
			resource.TestCheckResourceAttr(ipDS, "ip_address", "192.0.2.3"),
			resource.TestCheckResourceAttr(ipDS, "subnet_cidr", "192.0.2.0/29"),
			resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_ip.by_cidr", "subnet_id", fmt.Sprint(subnet)),
			resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_ip.by_cidr", "ip_address", "192.0.2.2"),
			nothingAllocated(b, 1),
			func(*terraform.State) error {
				q := b.Srv.RequestsFor(http.MethodGet, ipamtest.NextFreeIPPath)
				for _, r := range q {
					if r.Query.Get("subnet_id") == fmt.Sprint(subnet) && r.Query.Get("count") == "2" && r.Query.Get("exclude") == "192.0.2.2" {
						return nil
					}
				}
				return fmt.Errorf("no lookup with subnet_id, count and exclude in %d requests", len(q))
			},
		),
	})
}

func TestNextFreeIPExhaustedIsEmpty(t *testing.T) {
	b := ipamtest.New(t)
	subnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "192.0.2.0/30"})
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.1"})
	run(t, resource.TestStep{
		Config: hcl(b, fmt.Sprintf(`
data "omniddi_ipam_next_free_ip" "test" {
  subnet_id = %d
  quantity  = 2
}

data "omniddi_ipam_next_free_ip" "full" {
  subnet_id = %d
  exclude   = ["192.0.2.2"]
}
`, subnet, subnet)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(ipDS, "addresses.#", "0"),
			resource.TestCheckNoResourceAttr(ipDS, "ip_address"),
			resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_ip.full", "addresses.#", "0"),
		),
	})
}

func TestNextFreeSubnet(t *testing.T) {
	b := ipamtest.New(t)
	container := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.20.0.0/16"})
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.20.0.0/24"})
	before := b.Count()
	run(t, resource.TestStep{
		Config: hcl(b, fmt.Sprintf(`
data "omniddi_ipam_next_free_subnet" "test" {
  container_id  = %d
  prefix_length = 24
  exclude       = ["10.20.1.0/24"]
}

data "omniddi_ipam_next_free_subnet" "by_cidr" {
  container_cidr = "10.20.0.0/16"
  prefix_length  = 20
}
`, container)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(subnetDS, "cidr", "10.20.2.0/24"),
			resource.TestCheckResourceAttr(subnetDS, "network_addr", "10.20.2.0"),
			resource.TestCheckResourceAttr(subnetDS, "container_cidr", "10.20.0.0/16"),
			resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_subnet.by_cidr", "container_id", fmt.Sprint(container)),
			resource.TestCheckResourceAttr("data.omniddi_ipam_next_free_subnet.by_cidr", "cidr", "10.20.16.0/20"),
			func(*terraform.State) error {
				if b.Count() != before {
					return fmt.Errorf("IP space nodes = %d, want %d: a lookup must never create", b.Count(), before)
				}
				return nil
			},
		),
	})
}

func TestNextFreeSubnetExhaustedIsEmpty(t *testing.T) {
	b := ipamtest.New(t)
	container := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.40.0.0/24"})
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.40.0.0/25"})
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.40.0.128/25"})
	run(t, resource.TestStep{
		Config: hcl(b, fmt.Sprintf(`
data "omniddi_ipam_next_free_subnet" "test" {
  container_id  = %d
  prefix_length = 26
}
`, container)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckNoResourceAttr(subnetDS, "cidr"),
			resource.TestCheckNoResourceAttr(subnetDS, "network_addr"),
		),
	})
}

func TestNextFreeErrors(t *testing.T) {
	b := ipamtest.New(t)
	container := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.50.0.0/24"})
	run(t,
		resource.TestStep{Config: hcl(b, `data "omniddi_ipam_next_free_ip" "test" {}`), ExpectError: regexp.MustCompile(`(?s)subnet_id.*subnet_cidr`)},
		resource.TestStep{Config: hcl(b, `data "omniddi_ipam_next_free_ip" "test" { subnet_id = 9999 }`), ExpectError: regexp.MustCompile(`(?s)Subnet was not found`)},
		resource.TestStep{Config: hcl(b, fmt.Sprintf(`data "omniddi_ipam_next_free_ip" "test" { subnet_id = %d }`, container)), ExpectError: regexp.MustCompile(`(?s)not a subnet`)},
		resource.TestStep{Config: hcl(b, `data "omniddi_ipam_next_free_ip" "test" {
  subnet_id = 1
  quantity  = 101
}`), ExpectError: regexp.MustCompile(`(?s)quantity.*between 1 and 100`)},
		resource.TestStep{Config: hcl(b, `data "omniddi_ipam_next_free_subnet" "test" { prefix_length = 24 }`), ExpectError: regexp.MustCompile(`(?s)container_id.*container_cidr`)},
		resource.TestStep{Config: hcl(b, fmt.Sprintf(`data "omniddi_ipam_next_free_subnet" "test" {
  container_id  = %d
  prefix_length = 16
}`, container)), ExpectError: regexp.MustCompile(`(?s)prefix_length must be longer`)},
	)
}
