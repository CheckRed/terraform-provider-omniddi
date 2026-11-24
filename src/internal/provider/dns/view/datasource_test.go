package view_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func dataConfig(b *viewBackend, body string) string {
	return providerBlock(b) + "\n" + body + "\n"
}

func TestViewDataSourceByIDAndName(t *testing.T) {
	b := newViewBackend(t)
	seeded := b.seed("corp", 70)
	b.mu.Lock()
	seeded.Description = "corporate"
	seeded.Zones = 4
	seeded.ACLID = float64(9)
	seeded.Options["forwarders"] = []any{map[string]any{"ip": "198.51.100.53", "port": float64(53)}}
	seeded.Options["recursion"] = true
	b.mu.Unlock()
	b.srv.SetInheritedTag("DNS_VIEW", seeded.ID, 2, "ignored")
	cfg := dataConfig(b, fmt.Sprintf(`
data "omniddi_dns_view" "by_name" {
  name = "corp"
}

data "omniddi_dns_view" "by_id" {
  id = %d
}
`, seeded.ID))

	viewCase(t, resource.TestStep{
		Config: cfg,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "id", idOf(seeded)),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "priority", "70"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "description", "corporate"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "zone_count", "4"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "view_acl_id", "9"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "recursion", "true"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "forwarders.0.ip", "198.51.100.53"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_name", "server_group_ids.#", "1"),
			resource.TestCheckResourceAttr("data.omniddi_dns_view.by_id", "name", "corp"),
		),
	})
}

func TestViewDataSourceNotFound(t *testing.T) {
	b := newViewBackend(t)
	viewCase(t,
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_view" "x" { name = "missing" }`), ExpectError: regexp.MustCompile(`view not found`)},
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_view" "x" { id = 424242 }`), ExpectError: regexp.MustCompile(`view not found`)},
	)
}

func TestViewDataSourceRequiresExactlyOneLookup(t *testing.T) {
	b := newViewBackend(t)
	pattern := regexp.MustCompile(`Exactly one of these attributes must be configured|Attribute .* cannot be specified`)
	viewCase(t,
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_view" "x" {}`), PlanOnly: true, ExpectError: pattern},
		resource.TestStep{Config: dataConfig(b, "data \"omniddi_dns_view\" \"x\" {\n  id   = 1\n  name = \"a\"\n}"), PlanOnly: true, ExpectError: pattern},
	)
}

func TestViewsDataSourceFiltersAndPagination(t *testing.T) {
	b := newViewBackend(t)
	for i := range 450 {
		b.seed(fmt.Sprintf("bulk-%d", i), 100)
	}
	b.seed("special", 7)
	viewCase(t,
		resource.TestStep{
			Config: dataConfig(b, `data "omniddi_dns_views" "all" {}`),
			Check:  resource.TestCheckResourceAttr("data.omniddi_dns_views.all", "views.#", "451"),
		},
		resource.TestStep{
			Config: dataConfig(b, `
data "omniddi_dns_views" "special" {
  filters = [{ field = "priority", operator = "EQ", value = "7" }]
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.omniddi_dns_views.special", "views.#", "1"),
				resource.TestCheckResourceAttr("data.omniddi_dns_views.special", "views.0.name", "special"),
			),
		},
		resource.TestStep{
			Config: dataConfig(b, `data "omniddi_dns_views" "none" {
  filters = [{ field = "name", operator = "EQ", value = "nothing" }]
}`),
			Check: resource.TestCheckResourceAttr("data.omniddi_dns_views.none", "views.#", "0"),
		},
	)
}

func TestViewsDataSourceInvalidFilters(t *testing.T) {
	b := newViewBackend(t)
	viewCase(t,
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_views" "x" { filters = [{ field = "name", operator = "LIKE", value = "a" }] }`),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`operator value must be one of`),
		},
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_views" "x" { filters = [{ field = "name", operator = "IN" }] }`),
			ExpectError: regexp.MustCompile(`The IN operator needs values`),
		},
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_views" "x" { filters = [{ field = "name", operator = "EQ" }] }`),
			ExpectError: regexp.MustCompile(`The EQ operator needs value`),
		},
	)
}
