package address_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/address"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
)

const addr = "omniddi_ipam_address.test"

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, address.AllQueries...)
}

func config(b *ipamtest.Backend, body string) string {
	return acctest.ProviderConfig(b.Srv.URL()) + fmt.Sprintf("\nresource \"omniddi_ipam_address\" \"test\" {\n%s\n}\n", body)
}

func run(t *testing.T, checkDestroy resource.TestCheckFunc, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ipamtest.Factories(address.Resources(), nil),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		CheckDestroy:             checkDestroy,
		Steps:                    steps,
	})
}

func seedSubnet(b *ipamtest.Backend, cidr string) int64 {
	return b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: cidr})
}

func allocations(b *ipamtest.Backend, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := b.Allocations(); got != want {
			return fmt.Errorf("allocate calls = %d, want %d", got, want)
		}
		return nil
	}
}

func addressKept(b *ipamtest.Backend, ip string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, a := range b.Addresses() {
			if a.IP == ip {
				return nil
			}
		}
		return fmt.Errorf("address %s is no longer in OmniDDI", ip)
	}
}

func TestAddressLifecycleAndImport(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.1", Hostname: "gw"})
	create := config(b, fmt.Sprintf(`
  subnet_id = %d
  name      = "web-01.corp.example.com"
  exclude   = ["192.0.2.2"]
`, subnet))
	update := config(b, fmt.Sprintf(`
  subnet_id = %d
  name      = "web-01.corp.example.com"
`, subnet))

	run(t, addressKept(b, "192.0.2.3"),
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("ip_address"), knownvalue.StringExact("192.0.2.3")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_cidr"), knownvalue.StringExact("192.0.2.0/29")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("status"), knownvalue.StringExact("Used")),
				statecheck.ExpectIdentityValueMatchesState(addr, tfjsonpath.New("id")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				allocations(b, 1),
				func(*terraform.State) error {
					reqs := b.Srv.RequestsFor(http.MethodPost, ipamtest.AllocatePath)
					body := reqs[len(reqs)-1].Map()
					if _, sent := body["create_dns_records"]; sent {
						return fmt.Errorf("create_dns_records must not be sent: %v", body)
					}
					if fmt.Sprint(body["exclude"]) != "[192.0.2.2]" || body["name"] != "web-01.corp.example.com" {
						return fmt.Errorf("allocate body = %v", body)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{
			Config: update,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				allocations(b, 1),
				resource.TestCheckResourceAttr(addr, "ip_address", "192.0.2.3"),
				resource.TestCheckNoResourceAttr(addr, "exclude.#"),
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"status", "timeouts"}},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateId: "192.0.2.3",
			ImportStateVerify: true, ImportStateVerifyIgnore: []string{"status", "timeouts"},
		},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateId: "192.0.2.0/29/192.0.2.3",
			ImportStateVerify: true, ImportStateVerifyIgnore: []string{"status", "timeouts"},
		},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: update},
	)
}

func TestAddressIPv6(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "2001:db8:10::/120")
	run(t, nil, resource.TestStep{
		Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"v6-host\"", subnet)),
		Check:  resource.TestCheckResourceAttr(addr, "ip_address", "2001:db8:10::1"),
	})
}

func TestAddressNameChangeReplaces(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	run(t, nil,
		resource.TestStep{
			Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"app-a\"", subnet)),
			Check:  resource.TestCheckResourceAttr(addr, "ip_address", "192.0.2.1"),
		},
		resource.TestStep{Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"APP-A\"", subnet)), PlanOnly: true},
		resource.TestStep{
			Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"app-b\"", subnet)),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace),
			}},
			Check: allocations(b, 2),
		},
	)
}

func TestAddressGoneOrMovedIsRecreated(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	other := seedSubnet(b, "198.51.100.0/29")
	cfg := config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"db-01\"", subnet))
	lastID := func() int64 { list := b.Addresses(); return list[len(list)-1].ID }
	run(t, nil,
		resource.TestStep{Config: cfg},
		resource.TestStep{PreConfig: func() { b.RemoveAddress(lastID()) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
		resource.TestStep{Config: cfg, Check: allocations(b, 2)},
		resource.TestStep{
			PreConfig: func() { b.SetAddress(lastID(), func(a *ipamtest.Address) { a.NetworkID = other }) },
			Config:    cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	)
}

func TestAddressNoFreeIP(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/30")
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.1", Hostname: "a"})
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.2", Hostname: "b"})
	run(t, nil, resource.TestStep{
		Config:      config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"c\"", subnet)),
		ExpectError: regexp.MustCompile(`(?s)No free address in subnet.*NO_FREE_IP`),
	})
}

func TestAddressSubnetNotFound(t *testing.T) {
	b := ipamtest.New(t)
	run(t, nil, resource.TestStep{
		Config:      config(b, "  subnet_id = 999\n  name = \"c\""),
		ExpectError: regexp.MustCompile(`SUBNET_NOT_FOUND`),
	})
}

func TestAddressBusyIsRetried(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.BusyNext(2)
	run(t, nil, resource.TestStep{
		Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"busy\"", subnet)),
		Check: resource.ComposeAggregateTestCheckFunc(
			allocations(b, 1),
			resource.TestCheckResourceAttr(addr, "ip_address", "192.0.2.1"),
			func(*terraform.State) error {
				if n := len(b.Srv.RequestsFor(http.MethodPost, ipamtest.AllocatePath)); n != 3 {
					return fmt.Errorf("allocate requests = %d, want 3", n)
				}
				return nil
			},
		),
	})
}

func TestAddressLostResponseAdoptsOnlyTheNewRow(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.1", Hostname: "web"})
	b.LoseNextAllocateResponse(1)
	cfg := config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"web.\"", subnet))
	run(t, nil,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				allocations(b, 1),
				resource.TestCheckResourceAttr(addr, "ip_address", "192.0.2.2"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestAddressLostResponseWithoutNewRowFails(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.Srv.FailNext(http.MethodPost, ipamtest.AllocatePath, http.StatusBadGateway, 1)
	run(t, nil, resource.TestStep{
		Config:      config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"web\"", subnet)),
		ExpectError: regexp.MustCompile(`Unable to allocate an IP address`),
	})
}

func TestAddressValidation(t *testing.T) {
	b := ipamtest.New(t)
	run(t, nil,
		resource.TestStep{
			Config:      config(b, "  subnet_id = 1\n  name = \"x\"\n  exclude = [\"not-an-ip\"]"),
			ExpectError: regexp.MustCompile(`(?i)invalid ip`),
		},
		resource.TestStep{
			Config:      config(b, "  subnet_id = 1\n  name = \"\""),
			ExpectError: regexp.MustCompile(`(?i)string length must be`),
		},
	)
}

func TestAddressImportUnknownKey(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.SeedAddress(ipamtest.Address{NetworkID: subnet, IP: "192.0.2.10", Hostname: "x"})
	cfg := config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"x\"", subnet))
	run(t, nil,
		resource.TestStep{Config: cfg, ResourceName: addr, ImportState: true, ImportStateId: "192.0.2.1", ExpectError: regexp.MustCompile(`not found`)},
		resource.TestStep{Config: cfg, ResourceName: addr, ImportState: true, ImportStateId: "10.9.9.0/24/10.9.9.1", ExpectError: regexp.MustCompile(`not found`)},
		resource.TestStep{Config: cfg, ResourceName: addr, ImportState: true, ImportStateId: "not-a-key", ExpectError: regexp.MustCompile(`(?i)import`)},
	)
}

func TestAddressUnreadableResponseIsAdopted(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.CorruptNextAllocateResponse(1)
	run(t, nil, resource.TestStep{
		Config: config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"api\"", subnet)),
		Check: resource.ComposeAggregateTestCheckFunc(
			allocations(b, 1),
			resource.TestCheckResourceAttr(addr, "ip_address", "192.0.2.1"),
			resource.TestCheckResourceAttr(addr, "subnet_cidr", "192.0.2.0/29"),
		),
	})
}

func TestAddressSameNameTwiceInOneApply(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.LoseNextAllocateResponse(1)
	cfg := acctest.ProviderConfig(b.Srv.URL()) + fmt.Sprintf(`
resource "omniddi_ipam_address" "test" {
  count     = 2
  subnet_id = %d
  name      = "worker"
}
`, subnet)
	run(t, nil,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				allocations(b, 2),
				func(s *terraform.State) error {
					a := s.RootModule().Resources["omniddi_ipam_address.test.0"].Primary.Attributes
					c := s.RootModule().Resources["omniddi_ipam_address.test.1"].Primary.Attributes
					if a["id"] == c["id"] || a["ip_address"] == c["ip_address"] {
						return fmt.Errorf("both instances hold record %s / %s", a["id"], a["ip_address"])
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestAddressBusyGivesUp(t *testing.T) {
	b := ipamtest.New(t)
	subnet := seedSubnet(b, "192.0.2.0/29")
	b.BusyNext(10)
	run(t, nil, resource.TestStep{
		Config:      config(b, fmt.Sprintf("  subnet_id = %d\n  name = \"busy\"", subnet)),
		ExpectError: regexp.MustCompile(`ALLOCATION_BUSY`),
	})
}
