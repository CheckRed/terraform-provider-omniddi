package subnet_test

import (
	"fmt"
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
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/subnet"
)

const addr = "omniddi_ipam_subnet.test"

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, nodeapi.AllQueries...)
}

func ptr[T any](v T) *T { return &v }

func config(b *ipamtest.Backend, body string) string {
	return acctest.ProviderConfigWith(b.Srv.URL(), `  default_tags = { "env" = "prod" }
`) + fmt.Sprintf("\nresource \"omniddi_ipam_subnet\" \"test\" {\n%s\n}\n", body)
}

func plain(b *ipamtest.Backend, hcl string) string { return acctest.ProviderConfig(b.Srv.URL()) + hcl }

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ipamtest.Factories(subnet.Resources(), subnet.DataSources()),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

var ignoreOnImport = []string{"deletion_protection", "force_destroy", "timeouts"}

func TestSubnetLifecycleTagsAndImport(t *testing.T) {
	b := ipamtest.New(t)
	create := config(b, `
  parent_id           = 1
  network_addr        = "192.0.2.0"
  prefix              = 24
  name                = "lab"
  description         = "first"
  owner               = "netops"
  vlan_id             = 100
  deletion_protection = false
  tags                = { owner = "netops" }
  tags_list           = { teams = ["a", "b"] }
`)
	update := config(b, `
  parent_id           = 1
  network_addr        = "192.0.2.0"
  prefix              = 24
  name                = "lab-renamed"
  owner               = "secops"
  vlan                = "edge"
  deletion_protection = false
  tags                = { env = "dev" }
`)
	importable := config(b, `
  parent_id    = 1
  network_addr = "192.0.2.0"
  prefix       = 24
  name         = "lab-renamed"
  owner        = "secops"
  vlan         = "edge"
  tags         = { env = "dev" }
`)

	run(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("cidr"), knownvalue.StringExact("192.0.2.0/24")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("protocol"), knownvalue.StringExact("IPv4")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("status"), knownvalue.StringExact("ACTIVE")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("vlan_id"), knownvalue.Int64Exact(100)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("vlan"), knownvalue.Null()),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("business_unit_id"), knownvalue.Int64Exact(7)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectIdentityValueMatchesState(addr, tfjsonpath.New("id")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "name", "lab"),
				resource.TestCheckResourceAttr(addr, "prefix", "24"),
				func(*terraform.State) error {
					body := b.LastCreate()
					if _, sent := body["server_group_id"]; sent {
						return fmt.Errorf("server_group_id must never be sent: %v", body)
					}
					if body["vlan"] != "100" {
						return fmt.Errorf("vlan_id must be sent as the vlan label, body = %v", body)
					}
					got := b.Srv.TagValues(client.TagObjectSubnet, 3)
					if fmt.Sprint(got["owner"]) != "[netops]" || fmt.Sprint(got["teams"]) != "[a b]" || fmt.Sprint(got["env"]) != "[prod]" {
						return fmt.Errorf("backend tags = %v", got)
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
				resource.TestCheckResourceAttr(addr, "name", "lab-renamed"),
				resource.TestCheckResourceAttr(addr, "vlan", "edge"),
				resource.TestCheckNoResourceAttr(addr, "vlan_id"),
				resource.TestCheckNoResourceAttr(addr, "description"),
				resource.TestCheckNoResourceAttr(addr, "tags.owner"),
				resource.TestCheckNoResourceAttr(addr, "tags_list.teams.0"),
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable,
		},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "192.0.2.0/24", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport,
		},
	)
	if b.Count() != 0 {
		t.Errorf("subnet was not deleted at the end of the test")
	}
}

func TestSubnetDefaultsNameAndParent(t *testing.T) {
	b := ipamtest.New(t)
	outer := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	cfg := config(b, `
  network_addr        = "198.51.100.0"
  prefix              = 24
  deletion_protection = false
`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "name", "198.51.100.0/24"),
			resource.TestCheckResourceAttr(addr, "parent_id", fmt.Sprint(outer)),
			resource.TestCheckResourceAttr(addr, "protocol", "IPv4"),
		)},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestSubnetKeepsConfiguredAncestorAsParent(t *testing.T) {
	b := ipamtest.New(t)
	b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	cfg := config(b, `
  parent_id           = 1
  network_addr        = "198.51.100.0"
  prefix              = 24
  deletion_protection = false
`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(addr, "parent_id", "1")},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestSubnetReplacesWhenImmutableAttributesChange(t *testing.T) {
	b := ipamtest.New(t)
	cfg := func(extra string) string {
		return config(b, extra+"\ndeletion_protection = false")
	}
	replace := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace)}}
	run(t,
		resource.TestStep{Config: cfg("network_addr = \"192.0.2.0\"\nprefix = 24\nbusiness_unit_id = 11")},
		resource.TestStep{Config: cfg("network_addr = \"192.0.2.0\"\nprefix = 24\nbusiness_unit_id = 12"), ConfigPlanChecks: replace},
		resource.TestStep{Config: cfg("network_addr = \"192.0.2.0\"\nprefix = 25\nbusiness_unit_id = 12"), ConfigPlanChecks: replace},
		resource.TestStep{Config: cfg("network_addr = \"192.0.2.128\"\nprefix = 25\nbusiness_unit_id = 12"), ConfigPlanChecks: replace},
		resource.TestStep{Config: cfg("network_addr = \"192.0.2.128\"\nprefix = 25\nprotocol = \"IPv4\"\nbusiness_unit_id = 12")},
	)
}

func TestSubnetIPv6SpellingProducesNoDiff(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, `
  network_addr        = "2001:DB8:1::"
  prefix              = 48
  deletion_protection = false
`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "protocol", "IPv6"),
			resource.TestCheckResourceAttr(addr, "cidr", "2001:db8:1::/48"),
			resource.TestCheckResourceAttr(addr, "parent_id", fmt.Sprint(ipamtest.FamilyV6ID)),
		)},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestSubnetInvalidConfigurationIsRejectedAtPlan(t *testing.T) {
	b := ipamtest.New(t)
	bad := func(body string) string { return config(b, body) }
	run(t,
		resource.TestStep{Config: bad("network_addr = \"192.0.2.5\"\nprefix = 24"), PlanOnly: true, ExpectError: regexp.MustCompile(`host bits set; the network address is 192\.0\.2\.0/24`)},
		resource.TestStep{Config: bad("network_addr = \"192.0.2.0\"\nprefix = 40"), PlanOnly: true, ExpectError: regexp.MustCompile(`out of range`)},
		resource.TestStep{Config: bad("network_addr = \"192.0.2.0\"\nprefix = 24\nprotocol = \"IPv6\""), PlanOnly: true, ExpectError: regexp.MustCompile(`Protocol does not match network_addr`)},
		resource.TestStep{Config: bad("network_addr = \"192.0.2.0\"\nprefix = 24\nprotocol = \"IPX\""), PlanOnly: true, ExpectError: regexp.MustCompile(`protocol value must be one of`)},
		resource.TestStep{Config: bad("network_addr = \"not-an-ip\"\nprefix = 24"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)Invalid.*IP`)},
		resource.TestStep{Config: bad("network_addr = \"192.0.2.0\"\nprefix = 24\nvlan = \"a\"\nvlan_id = 5"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)vlan_id.*vlan|vlan.*vlan_id`)},
	)
}

func TestSubnetBackendRejectionsCarryDetail(t *testing.T) {
	b := ipamtest.New(t)
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "192.0.2.0/24", Label: "existing"})
	run(t,
		resource.TestStep{
			Config:      config(b, "network_addr = \"192.0.2.0\"\nprefix = 25"),
			ExpectError: regexp.MustCompile(`(?s)HTTP 400.*overlaps sibling`),
		},
		resource.TestStep{
			Config:      config(b, "parent_id = 999\nnetwork_addr = \"203.0.113.0\"\nprefix = 24"),
			ExpectError: regexp.MustCompile(`(?s)HTTP 400.*Parent node 999 not found`),
		},
	)
}

func TestSubnetApprovalResponseReportsRequestID(t *testing.T) {
	b := ipamtest.New(t)
	b.SubnetBody = func(map[string]any) (fakeserver.Response, bool) {
		return b.Srv.ApprovalPending(321, "Subnet creation request"), true
	}
	run(t, resource.TestStep{
		Config:      config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false"),
		ExpectError: regexp.MustCompile(`Approval required \(request 321\)`),
	})
	if b.Count() != 0 {
		t.Errorf("nothing may be created while approval is pending")
	}
}

func TestSubnetReadRemovesDeletedObject(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{PreConfig: func() { b.Remove(3) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	)
}

func TestSubnetDeleteTreatsMissingObjectAsGone(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{PreConfig: func() { b.Remove(3) }, Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/subnets/3")); got != 0 {
		t.Errorf("a vanished subnet must not be deleted again, DELETE count = %d", got)
	}
}

func TestSubnetDeletionProtectionBlocksDestroy(t *testing.T) {
	b := ipamtest.New(t)
	run(t,
		resource.TestStep{Config: config(b, "network_addr = \"192.0.2.0\"\nprefix = 24"), Check: resource.TestCheckResourceAttr(addr, "deletion_protection", "true")},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL()), ExpectError: regexp.MustCompile(`Deletion protection is enabled`)},
		resource.TestStep{Config: config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/subnets/3")); got != 1 {
		t.Errorf("DELETE sent %d times; protection must stop the first destroy before any API call", got)
	}
}

func TestSubnetWithDHCPServiceRefusesUnlessForced(t *testing.T) {
	b := ipamtest.New(t)
	base := "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false\n"
	run(t,
		resource.TestStep{Config: config(b, base)},
		resource.TestStep{
			PreConfig: func() {
				n, _ := b.Get(3)
				n.SubnetID = ptr(int64(77))
				b.Remove(3)
				b.Seed(n)
			},
			Config:      acctest.ProviderConfig(b.Srv.URL()),
			ExpectError: regexp.MustCompile(`(?s)Refusing to delete with dependent objects.*DHCP service configured`),
		},
		resource.TestStep{Config: config(b, base+"force_destroy = true\n"), Check: resource.TestCheckResourceAttr(addr, "force_destroy", "true")},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	if b.Count() != 0 {
		t.Errorf("forced destroy must delete the subnet")
	}
}

func TestSubnetReadRejectsNodeOfAnotherType(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				n, _ := b.Get(3)
				n.Type = "CONTAINER"
				b.Remove(3)
				b.Seed(n)
			},
			Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`is a CONTAINER, not a SUBNET`),
		},
		resource.TestStep{PreConfig: func() { b.Remove(3) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	)
}

func TestSubnetImportUnknownCIDR(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: addr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "203.0.113.0/24", nil },
			ExpectError:       regexp.MustCompile(`No object matches "203.0.113.0/24"`),
		},
	)
}

func TestSubnetUnknownTagFailsAfterSavingState(t *testing.T) {
	b := ipamtest.New(t)
	run(t,
		resource.TestStep{
			Config:      config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false\ntags = { nonexistent = \"x\" }"),
			ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*saved to the Terraform state`),
		},
		resource.TestStep{Config: config(b, "network_addr = \"192.0.2.0\"\nprefix = 24\ndeletion_protection = false")},
	)
}

func TestSubnetDataSources(t *testing.T) {
	b := ipamtest.New(t)
	outer := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	one := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "198.51.100.0/24", Label: "lab", Owner: ptr("netops")})
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "203.0.113.0/24", Label: "dmz"})
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "2001:db8:1::/48", Label: "v6lab"})
	b.Srv.AddTagDefinition(fakeserver.TagDefinition{ID: 9, Name: "zone"})
	b.ApplyTag(t, client.TagObjectSubnet, one, "zone", "east")

	hcl := plain(b, fmt.Sprintf(`
data "omniddi_ipam_subnet" "by_id" { id = %d }
data "omniddi_ipam_subnet" "by_cidr" { cidr = "203.0.113.0/24" }
data "omniddi_ipam_subnets" "all" {}
data "omniddi_ipam_subnets" "search" { search = "LAB" }
data "omniddi_ipam_subnets" "scoped" { scope_node_id = %d }
data "omniddi_ipam_subnets" "tagged" { tag_filters = { zone = "east" } }
data "omniddi_ipam_subnets" "filtered" {
  filters = [
    { field = "owner", operator = "EQ", value = "netops" },
    { field = "name", operator = "NOT_NULL" },
  ]
}
`, one, outer))

	run(t, resource.TestStep{
		Config: hcl,
		ConfigStateChecks: []statecheck.StateCheck{
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnet.by_id", tfjsonpath.New("name"), knownvalue.StringExact("lab")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnet.by_id", tfjsonpath.New("cidr"), knownvalue.StringExact("198.51.100.0/24")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnet.by_id", tfjsonpath.New("parent_id"), knownvalue.Int64Exact(outer)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnet.by_id", tfjsonpath.New("tags"), knownvalue.MapExact(map[string]knownvalue.Check{"zone": knownvalue.StringExact("east")})),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnet.by_cidr", tfjsonpath.New("name"), knownvalue.StringExact("dmz")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnets.all", tfjsonpath.New("subnets"), knownvalue.ListSizeExact(3)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnets.search", tfjsonpath.New("subnets"), knownvalue.ListSizeExact(2)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnets.scoped", tfjsonpath.New("subnets"), knownvalue.ListSizeExact(1)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnets.tagged", tfjsonpath.New("subnets"), knownvalue.ListSizeExact(1)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_subnets.filtered", tfjsonpath.New("subnets"), knownvalue.ListSizeExact(1)),
		},
	})
}

func TestSubnetDataSourceErrors(t *testing.T) {
	b := ipamtest.New(t)
	container := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	run(t,
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_subnet" "x" { cidr = "203.0.113.0/24" }`), ExpectError: regexp.MustCompile(`not found`)},
		resource.TestStep{
			Config:      plain(b, fmt.Sprintf(`data "omniddi_ipam_subnet" "x" { id = %d }`, container)),
			ExpectError: regexp.MustCompile(`is a CONTAINER, not a SUBNET`),
		},
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_subnet" "x" {}`), ExpectError: regexp.MustCompile(`Exactly one of these attributes`)},
		resource.TestStep{
			Config:      plain(b, `data "omniddi_ipam_subnets" "x" { filters = [{ field = "name", operator = "BETWEEN" }] }`),
			ExpectError: regexp.MustCompile(`(?s)operator.*must be one of`),
		},
	)
}
