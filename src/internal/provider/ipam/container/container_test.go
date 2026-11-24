package container_test

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
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/container"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const addr = "omniddi_ipam_container.test"

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, nodeapi.AllQueries...)
}

func config(b *ipamtest.Backend, body string) string {
	return acctest.ProviderConfigWith(b.Srv.URL(), `  default_tags = { "env" = "prod" }
`) + fmt.Sprintf("\nresource \"omniddi_ipam_container\" \"test\" {\n%s\n}\n", body)
}

func plain(b *ipamtest.Backend, hcl string) string { return acctest.ProviderConfig(b.Srv.URL()) + hcl }

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ipamtest.Factories(container.Resources(), container.DataSources()),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

func ptr[T any](v T) *T { return &v }

var ignoreOnImport = []string{"deletion_protection", "force_destroy", "reparent_children_on_destroy", "timeouts"}

func TestContainerLifecycleTagsAndImport(t *testing.T) {
	b := ipamtest.New(t)
	create := config(b, `
  parent_id           = 1
  name                = "corp"
  cidr                = "10.0.0.0/16"
  description         = "first"
  vlan                = "100"
  owner               = "netops"
  deletion_protection = false
  tags                = { owner = "netops" }
  tags_list           = { teams = ["a", "b"] }
`)
	update := config(b, `
  parent_id           = 1
  name                = "corp-renamed"
  cidr                = "10.0.0.0/16"
  owner               = "secops"
  deletion_protection = false
  tags                = { env = "dev" }
`)
	importable := config(b, `
  parent_id           = 1
  name                = "corp-renamed"
  cidr                = "10.0.0.0/16"
  owner               = "secops"
  tags                = { env = "dev" }
`)

	run(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("protocol"), knownvalue.StringExact("IPv4")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("prefix_length"), knownvalue.Int64Exact(16)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("status"), knownvalue.StringExact("ACTIVE")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("business_unit_id"), knownvalue.Int64Exact(7)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectIdentityValueMatchesState(addr, tfjsonpath.New("id")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "parent_id", "1"),
				resource.TestCheckResourceAttr(addr, "description", "first"),
				resource.TestCheckResourceAttr(addr, "reparent_children_on_destroy", "false"),
				resource.TestCheckResourceAttrSet(addr, "created_on"),
				func(*terraform.State) error {
					got := b.Srv.TagValues(client.TagObjectNetworkContain, 3)
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
				resource.TestCheckResourceAttr(addr, "name", "corp-renamed"),
				resource.TestCheckResourceAttr(addr, "owner", "secops"),
				resource.TestCheckNoResourceAttr(addr, "description"),
				resource.TestCheckNoResourceAttr(addr, "vlan"),
				resource.TestCheckNoResourceAttr(addr, "tags.owner"),
				resource.TestCheckNoResourceAttr(addr, "tags_list.teams.0"),
				func(*terraform.State) error {
					n, _ := b.Get(3)
					if n.Label != "corp-renamed" || n.Description == nil || *n.Description != "" {
						return fmt.Errorf("backend node = %+v; cleared fields must be sent as empty strings", n)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable,
		},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "10.0.0.0/16", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport,
		},
	)
	if b.Count() != 0 {
		t.Errorf("container was not deleted at the end of the test")
	}
}

func TestContainerImportUnknownCIDR(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, `
parent_id = 1
name = "corp"
cidr = "10.0.0.0/16"
deletion_protection = false`)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: addr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "172.16.0.0/12", nil },
			ExpectError:       regexp.MustCompile(`No object matches "172.16.0.0/12"`),
		},
	)
}

func TestContainerReplacesWhenCIDROrParentChange(t *testing.T) {
	b := ipamtest.New(t)
	outer := `
resource "omniddi_ipam_container" "outer" {
  parent_id           = 1
  name                = "outer"
  cidr                = "10.0.0.0/8"
  deletion_protection = false
}
`
	inner := func(parent, cidr string) string {
		return config(b, fmt.Sprintf(`
  parent_id           = %s
  name                = "inner"
  cidr                = %q
  deletion_protection = false
  depends_on          = [omniddi_ipam_container.outer]`, parent, cidr)) + outer
	}
	replace := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace)}}

	run(t,
		resource.TestStep{Config: inner("omniddi_ipam_container.outer.id", "10.1.0.0/16"), Check: resource.TestCheckResourceAttr(addr, "cidr", "10.1.0.0/16")},
		resource.TestStep{Config: inner("omniddi_ipam_container.outer.id", "10.2.0.0/16"), ConfigPlanChecks: replace},
		resource.TestStep{Config: inner("1", "10.2.0.0/16"), ConfigPlanChecks: replace},
	)
}

func TestContainerBusinessUnitIsConfigurableAndReplaces(t *testing.T) {
	b := ipamtest.New(t)
	cfg := func(bu string) string {
		return config(b, fmt.Sprintf(`
  parent_id           = 1
  name                = "corp"
  cidr                = "10.0.0.0/16"
  business_unit_id    = %s
  deletion_protection = false`, bu))
	}
	run(t,
		resource.TestStep{Config: cfg("11"), Check: resource.TestCheckResourceAttr(addr, "business_unit_id", "11")},
		resource.TestStep{
			Config: cfg("12"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace),
			}},
		},
	)
}

func TestContainerInvalidCIDRIsRejectedAtPlan(t *testing.T) {
	b := ipamtest.New(t)
	bad := func(cidr string) string {
		return config(b, fmt.Sprintf("parent_id = 1\nname = \"x\"\ncidr = %q", cidr))
	}
	run(t,
		resource.TestStep{Config: bad("not-a-cidr"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)Invalid.*CIDR`)},
		resource.TestStep{Config: bad("10.0.0.5/24"), PlanOnly: true, ExpectError: regexp.MustCompile(`host bits set; the network address is 10\.0\.0\.0/24`)},
	)
}

func TestContainerBackendRejectionsCarryDetail(t *testing.T) {
	b := ipamtest.New(t)
	run(t,
		resource.TestStep{
			Config:      config(b, "parent_id = 999\nname = \"x\"\ncidr = \"10.0.0.0/16\""),
			ExpectError: regexp.MustCompile(`(?s)HTTP 400.*Parent node 999 not found`),
		},
		resource.TestStep{
			Config:      config(b, "parent_id = 1\nname = \"x\"\ncidr = \"10.0.0.1/32\"\n"),
			ExpectError: regexp.MustCompile(`cannot be a single host|host bits`),
		},
		resource.TestStep{
			Config:      config(b, "parent_id = 1\nname = \"x\"\ncidr = \"10.0.0.0/32\""),
			ExpectError: regexp.MustCompile(`(?s)HTTP 400.*single host address`),
		},
	)
	if b.Count() != 0 {
		t.Errorf("nothing may be created by rejected requests")
	}
}

func TestContainerIPv6AndCIDRSpellingProduceNoDiff(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, `
parent_id = 2
name = "v6"
cidr = "2001:DB8::/32"
deletion_protection = false`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "protocol", "IPv6"),
			resource.TestCheckResourceAttr(addr, "prefix_length", "32"),
		)},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestContainerKeepsConfiguredAncestorAsParent(t *testing.T) {
	b := ipamtest.New(t)
	b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/8", Label: "outer"})
	cfg := config(b, `
parent_id = 1
name = "inner"
cidr = "10.1.0.0/16"
deletion_protection = false`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(addr, "parent_id", "1")},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{
			PreConfig: func() { b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.1.0.0/12", Label: "between"}) },
			Config:    cfg, PlanOnly: true,
		},
	)
}

func TestContainerReadRemovesDeletedObject(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, `
parent_id = 1
name = "corp"
cidr = "10.0.0.0/16"
deletion_protection = false`)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() { b.Remove(3) },
			Config:    cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	)
}

func TestContainerReadRejectsNodeOfAnotherType(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, `
parent_id = 1
name = "corp"
cidr = "10.0.0.0/16"
deletion_protection = false`)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				n, _ := b.Get(3)
				n.Type = "SUBNET"
				b.Remove(3)
				b.Seed(n)
			},
			Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`is a SUBNET, not a CONTAINER`),
		},
		resource.TestStep{PreConfig: func() { b.Remove(3) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	)
}

func TestContainerDeletionProtectionBlocksDestroy(t *testing.T) {
	b := ipamtest.New(t)
	protected := config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"")
	unprotected := config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: protected, Check: resource.TestCheckResourceAttr(addr, "deletion_protection", "true")},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL()), ExpectError: regexp.MustCompile(`Deletion protection is enabled`)},
		resource.TestStep{Config: unprotected},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/containers/3")); got != 1 {
		t.Errorf("DELETE sent %d times; protection must stop the first destroy before any API call", got)
	}
}

func TestContainerWithChildrenRefusesThenReparents(t *testing.T) {
	b := ipamtest.New(t)
	base := "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false\n"
	seedChild := func() { b.Seed(ipamtest.Node{ID: 50, Type: "SUBNET", CIDR: "10.0.1.0/24", Label: "child"}) }

	run(t,
		resource.TestStep{Config: config(b, base)},
		resource.TestStep{
			PreConfig:   seedChild,
			Config:      acctest.ProviderConfig(b.Srv.URL()),
			ExpectError: regexp.MustCompile(`(?s)Refusing to delete with dependent objects.*1 child network\(s\).*reparent_children_on_destroy`),
		},
		resource.TestStep{
			Config: config(b, base+"force_destroy = true\n"),
			Check:  resource.TestCheckResourceAttr(addr, "force_destroy", "true"),
		},
		resource.TestStep{
			Config:      acctest.ProviderConfig(b.Srv.URL()),
			ExpectError: regexp.MustCompile(`(?s)HTTP 400.*still holds networks`),
		},
		resource.TestStep{Config: config(b, base+"reparent_children_on_destroy = true\n")},
		resource.TestStep{Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	child, ok := b.Get(50)
	if !ok || child.ParentID == nil || *child.ParentID != ipamtest.FamilyV4ID {
		t.Errorf("child = %+v, want it reparented to the family root", child)
	}
	if got := b.Srv.RequestsFor("DELETE", "/api/v1/ipam/containers/3"); len(got) == 0 || got[len(got)-1].Query.Get("reparent_children") != "true" {
		t.Errorf("final DELETE must carry reparent_children=true, got %+v", got)
	}
}

func TestContainerUnknownTagFailsAfterSavingState(t *testing.T) {
	b := ipamtest.New(t)
	run(t,
		resource.TestStep{
			Config:      config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false\ntags = { nonexistent = \"x\" }"),
			ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*saved to the Terraform state`),
		},
		resource.TestStep{Config: config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false")},
	)
}

func TestContainerApprovalResponseReportsRequestID(t *testing.T) {
	b := ipamtest.New(t)
	b.Srv.HandleREST("POST", "/api/v1/ipam/containers", func(*fakeserver.Request) fakeserver.Response {
		return b.Srv.ApprovalPending(321, "Container creation request")
	})
	run(t, resource.TestStep{
		Config:      config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false"),
		ExpectError: regexp.MustCompile(`Approval required \(request 321\)`),
	})
	if b.Count() != 0 {
		t.Errorf("nothing may be created while approval is pending")
	}
}

func TestContainerDeleteTreatsMissingObjectAsGone(t *testing.T) {
	b := ipamtest.New(t)
	cfg := config(b, "parent_id = 1\nname = \"corp\"\ncidr = \"10.0.0.0/16\"\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{PreConfig: func() { b.Remove(3) }, Config: acctest.ProviderConfig(b.Srv.URL())},
	)
	if got := len(b.Srv.RequestsFor("DELETE", "/api/v1/ipam/containers/3")); got != 0 {
		t.Errorf("a vanished container must not be deleted again, DELETE count = %d", got)
	}
}

func TestContainerDataSources(t *testing.T) {
	b := ipamtest.New(t)
	ids := map[string]int64{
		"a":  b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/8", Label: "outer", Owner: ptr("netops")}),
		"b":  b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.1.0.0/16", Label: "inner"}),
		"v6": b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "2001:db8::/32", Label: "documentation"}),
	}
	b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.1.1.0/24", Label: "lan"})
	b.Srv.AddTagDefinition(fakeserver.TagDefinition{ID: 9, Name: "zone"})
	b.ApplyTag(t, client.TagObjectNetworkContain, ids["b"], "zone", "east")

	hcl := plain(b, fmt.Sprintf(`
data "omniddi_ipam_container" "by_id" { id = %d }
data "omniddi_ipam_container" "by_cidr" { cidr = "2001:DB8::/32" }
data "omniddi_ipam_containers" "all" {}
data "omniddi_ipam_containers" "top" { recursive = false }
data "omniddi_ipam_containers" "below" { parent_id = %d }
data "omniddi_ipam_containers" "search" { search = "OUTER" }
data "omniddi_ipam_containers" "tagged" { tag_filters = { zone = "east" } }
`, ids["a"], ids["a"]))

	run(t, resource.TestStep{
		Config: hcl,
		ConfigStateChecks: []statecheck.StateCheck{
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_id", tfjsonpath.New("name"), knownvalue.StringExact("outer")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_id", tfjsonpath.New("cidr"), knownvalue.StringExact("10.0.0.0/8")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_id", tfjsonpath.New("owner"), knownvalue.StringExact("netops")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_id", tfjsonpath.New("has_children"), knownvalue.Bool(true)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_cidr", tfjsonpath.New("id"), knownvalue.Int64Exact(ids["v6"])),
			statecheck.ExpectKnownValue("data.omniddi_ipam_container.by_cidr", tfjsonpath.New("cidr"), knownvalue.StringExact("2001:DB8::/32")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.all", tfjsonpath.New("containers"), knownvalue.ListSizeExact(3)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.top", tfjsonpath.New("containers"), knownvalue.ListSizeExact(2)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.below", tfjsonpath.New("containers"), knownvalue.ListSizeExact(1)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.search", tfjsonpath.New("containers"), knownvalue.ListSizeExact(1)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.tagged", tfjsonpath.New("containers"), knownvalue.ListSizeExact(1)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_containers.tagged", tfjsonpath.New("containers").AtSliceIndex(0).AtMapKey("tags"),
				knownvalue.MapExact(map[string]knownvalue.Check{"zone": knownvalue.StringExact("east")})),
		},
	})
}

func TestContainerDataSourceErrors(t *testing.T) {
	b := ipamtest.New(t)
	subnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.1.1.0/24", Label: "lan"})
	run(t,
		resource.TestStep{
			Config:      plain(b, `data "omniddi_ipam_container" "x" { cidr = "172.16.0.0/12" }`),
			ExpectError: regexp.MustCompile(`not found`),
		},
		resource.TestStep{
			Config:      plain(b, fmt.Sprintf(`data "omniddi_ipam_container" "x" { id = %d }`, subnet)),
			ExpectError: regexp.MustCompile(`is a SUBNET, not a CONTAINER`),
		},
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_container" "x" {}`), ExpectError: regexp.MustCompile(`Exactly one of these attributes`)},
		resource.TestStep{
			Config: plain(b, `data "omniddi_ipam_container" "x" {
  id   = 1
  cidr = "10.0.0.0/8"
}`),
			ExpectError: regexp.MustCompile(`Exactly one of these attributes`),
		},
	)
}
