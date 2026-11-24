package servergroup_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
)

func dsConfig(b *backend, body string) string {
	return acctest.ProviderConfig(b.srv.URL()) + body
}

func TestDataSourceByNameAndID(t *testing.T) {
	b := newBackend(t)
	id := b.seed("edge", "dns", member{ID: 5, Role: "lead primary"})
	b.seed("other", "dhcp")
	b.srv.AddTagDefinition(fakeTag(9, "region"))
	cfg := dsConfig(b, fmt.Sprintf(`
data "omniddi_server_group" "by_name" { name = "edge" }
data "omniddi_server_group" "by_id" { id = %d }
`, id))
	runCase(t, resource.TestStep{
		Config: cfg,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_server_group.by_name", "id", fmt.Sprint(id)),
			resource.TestCheckResourceAttr("data.omniddi_server_group.by_name", "type", "dns"),
			resource.TestCheckResourceAttr("data.omniddi_server_group.by_name", "servers.#", "1"),
			resource.TestCheckResourceAttr("data.omniddi_server_group.by_name", "servers.0.role", "lead primary"),
			resource.TestCheckResourceAttr("data.omniddi_server_group.by_id", "name", "edge"),
		),
	})
}

func TestDataSourceNotFoundAmbiguousAndTypeMismatch(t *testing.T) {
	b := newBackend(t)
	dnsID := b.seed("twin", "dns")
	b.seed("twin", "dhcp")
	runCase(t,
		resource.TestStep{Config: dsConfig(b, `data "omniddi_server_group" "x" { name = "ghost" }`), ExpectError: regexp.MustCompile(`No server group is named "ghost"`)},
		resource.TestStep{Config: dsConfig(b, `data "omniddi_server_group" "x" { id = 9999 }`), ExpectError: regexp.MustCompile(`No server group has id 9999`)},
		resource.TestStep{Config: dsConfig(b, `data "omniddi_server_group" "x" { name = "twin" }`), ExpectError: regexp.MustCompile(`qualify the lookup with a type`)},
		resource.TestStep{Config: dsConfig(b, fmt.Sprintf(`data "omniddi_server_group" "x" {
  id   = %d
  type = "dhcp"
}`, dnsID)), ExpectError: regexp.MustCompile(`is a dns group, not dhcp`)},
		resource.TestStep{Config: dsConfig(b, `data "omniddi_server_group" "x" {
  id   = 1
  name = "a"
}`), ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Combination|exactly one`)},
		resource.TestStep{Config: dsConfig(b, `data "omniddi_server_group" "x" {
  name = "twin"
  type = "dhcp"
}`)},
	)
}

func TestListDataSourceFilters(t *testing.T) {
	b := newBackend(t)
	b.seed("a", "dns")
	b.seed("b", "dhcp")
	b.seed("b", "dns")
	runCase(t, resource.TestStep{
		Config: dsConfig(b, `
data "omniddi_server_groups" "all" {}
data "omniddi_server_groups" "dhcp" { type = "dhcp" }
data "omniddi_server_groups" "named" { name = "b" }
data "omniddi_server_groups" "none" { name = "zzz" }
`),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_server_groups.all", "server_groups.#", "3"),
			resource.TestCheckResourceAttr("data.omniddi_server_groups.dhcp", "server_groups.#", "1"),
			resource.TestCheckResourceAttr("data.omniddi_server_groups.named", "server_groups.#", "2"),
			resource.TestCheckResourceAttr("data.omniddi_server_groups.none", "server_groups.#", "0"),
		),
	})
}
