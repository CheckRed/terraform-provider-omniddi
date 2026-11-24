package node_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/node"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, nodeapi.AllQueries...)
}

func plain(b *ipamtest.Backend, hcl string) string { return acctest.ProviderConfig(b.Srv.URL()) + hcl }

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: ipamtest.Factories(node.Resources(), node.DataSources()),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

func TestNodeLookups(t *testing.T) {
	b := ipamtest.New(t)
	container := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	subnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "198.51.100.0/24", Label: "lab"})

	hcl := plain(b, fmt.Sprintf(`
data "omniddi_ipam_node" "family" { id = 1 }
data "omniddi_ipam_node" "v4_root" { protocol = "IPv4" }
data "omniddi_ipam_node" "v6_root" {
  protocol = "IPv6"
  type     = "FAMILY"
}
data "omniddi_ipam_node" "container" { cidr = "198.51.0.0/16" }
data "omniddi_ipam_node" "subnet" { cidr = "198.51.100.0/24" }
data "omniddi_ipam_node" "typed_subnet" {
  cidr = "198.51.100.0/24"
  type = "SUBNET"
}
data "omniddi_ipam_node" "typed_container" {
  cidr = "198.51.0.0/16"
  type = "CONTAINER"
}
data "omniddi_ipam_node" "typed_id" {
  id   = %d
  type = "SUBNET"
}
`, subnet))

	run(t, resource.TestStep{
		Config: hcl,
		ConfigStateChecks: []statecheck.StateCheck{
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.family", tfjsonpath.New("type"), knownvalue.StringExact("FAMILY")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.family", tfjsonpath.New("cidr"), knownvalue.Null()),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.family", tfjsonpath.New("protocol"), knownvalue.StringExact("IPv4")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.v4_root", tfjsonpath.New("id"), knownvalue.Int64Exact(ipamtest.FamilyV4ID)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.v4_root", tfjsonpath.New("type"), knownvalue.StringExact("FAMILY")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.v6_root", tfjsonpath.New("id"), knownvalue.Int64Exact(ipamtest.FamilyV6ID)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.container", tfjsonpath.New("id"), knownvalue.Int64Exact(container)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.container", tfjsonpath.New("type"), knownvalue.StringExact("CONTAINER")),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.container", tfjsonpath.New("has_children"), knownvalue.Bool(true)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.subnet", tfjsonpath.New("id"), knownvalue.Int64Exact(subnet)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.subnet", tfjsonpath.New("parent_id"), knownvalue.Int64Exact(container)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.typed_subnet", tfjsonpath.New("id"), knownvalue.Int64Exact(subnet)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.typed_container", tfjsonpath.New("id"), knownvalue.Int64Exact(container)),
			statecheck.ExpectKnownValue("data.omniddi_ipam_node.typed_id", tfjsonpath.New("cidr"), knownvalue.StringExact("198.51.100.0/24")),
		},
	})
}

func TestNodeLookupErrors(t *testing.T) {
	b := ipamtest.New(t)
	b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.51.0.0/16", Label: "outer"})
	run(t,
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_node" "x" { cidr = "172.16.0.0/12" }`), ExpectError: regexp.MustCompile(`not found`)},
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_node" "x" { id = 9999 }`), ExpectError: regexp.MustCompile(`not found`)},
		resource.TestStep{
			Config:      plain(b, "data \"omniddi_ipam_node\" \"x\" {\n  cidr = \"198.51.0.0/16\"\n  type = \"SUBNET\"\n}"),
			ExpectError: regexp.MustCompile(`not found`),
		},
		resource.TestStep{
			Config:      plain(b, "data \"omniddi_ipam_node\" \"x\" {\n  id   = 1\n  type = \"SUBNET\"\n}"),
			ExpectError: regexp.MustCompile(`is a FAMILY, not a SUBNET`),
		},
		resource.TestStep{
			Config:      plain(b, "data \"omniddi_ipam_node\" \"x\" {\n  cidr = \"198.51.0.0/16\"\n  type = \"ROOT\"\n}"),
			ExpectError: regexp.MustCompile(`value must be one of`),
		},
		resource.TestStep{Config: plain(b, `data "omniddi_ipam_node" "x" {}`), ExpectError: regexp.MustCompile(`Exactly one of these attributes`)},
		resource.TestStep{
			Config:      plain(b, "data \"omniddi_ipam_node\" \"x\" {\n  protocol = \"IPv4\"\n  cidr     = \"198.51.0.0/16\"\n}"),
			ExpectError: regexp.MustCompile(`Exactly one of these attributes`),
		},
		resource.TestStep{
			Config:      plain(b, "data \"omniddi_ipam_node\" \"x\" {\n  protocol = \"IPv4\"\n  type     = \"SUBNET\"\n}"),
			ExpectError: regexp.MustCompile(`returns a FAMILY root`),
		},
		resource.TestStep{
			Config:      plain(b, `data "omniddi_ipam_node" "x" { protocol = "ipv4" }`),
			ExpectError: regexp.MustCompile(`value must be one of`),
		},
	)
}
