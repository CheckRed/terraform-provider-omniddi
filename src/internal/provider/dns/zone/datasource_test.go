package zone_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func dataConfig(b *zoneBackend, body string) string {
	return providerBlock(b) + "\n" + body + "\n"
}

func TestZoneDataSourceLookups(t *testing.T) {
	b := newZoneBackend(t)
	seeded := b.seed("corp.example.com", b.viewID)
	b.mu.Lock()
	seeded.Config = map[string]any{
		"ttl": float64(7200), "email": "hostmaster@example.com", "dnssec": map[string]any{"enable": true},
		"nameservers": []any{map[string]any{"ip": "198.51.100.53", "port": float64(53)}},
	}
	seeded.UserRecords = 3
	b.mu.Unlock()
	b.seed("other.example.com", b.viewID+99)
	cfg := dataConfig(b, fmt.Sprintf(`
data "omniddi_dns_zone" "by_id" {
  id = %[1]d
}

data "omniddi_dns_zone" "by_view_id" {
  name    = "Corp.Example.com."
  view_id = %[2]d
}

data "omniddi_dns_zone" "by_view_name" {
  name      = "corp.example.com"
  view_name = %[3]q
}
`, seeded.ID, b.viewID, seededViewName))

	zoneCase(t, resource.TestStep{
		Config: cfg,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "name", "corp.example.com"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "ttl", "7200"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "email", "hostmaster@example.com"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "enable_dnssec", "true"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "nameservers.0", "198.51.100.53"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "records_count", "5"),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_id", "view_name", seededViewName),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_view_id", "id", fmt.Sprint(seeded.ID)),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_view_name", "id", fmt.Sprint(seeded.ID)),
			resource.TestCheckResourceAttr("data.omniddi_dns_zone.by_view_name", "view_id", fmt.Sprint(b.viewID)),
		),
	})
}

func TestZoneDataSourceNotFoundAndInvalid(t *testing.T) {
	b := newZoneBackend(t)
	exactlyOne := regexp.MustCompile(`Exactly one of these attributes must be configured|cannot be specified`)
	zoneCase(t,
		resource.TestStep{Config: dataConfig(b, fmt.Sprintf(`data "omniddi_dns_zone" "x" {
  name    = "missing.example.com"
  view_id = %d
}`, b.viewID)), ExpectError: regexp.MustCompile(`zone not found`)},
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_zone" "x" {
  name      = "a.example.com"
  view_name = "nosuchview"
}`), ExpectError: regexp.MustCompile(`view not found`)},
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_zone" "x" { id = 424242 }`), ExpectError: regexp.MustCompile(`zone not found`)},
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_zone" "x" { name = "a.example.com" }`), ExpectError: regexp.MustCompile(`needs view_id or view_name`)},
		resource.TestStep{Config: dataConfig(b, `data "omniddi_dns_zone" "x" {}`), PlanOnly: true, ExpectError: exactlyOne},
		resource.TestStep{Config: dataConfig(b, "data \"omniddi_dns_zone\" \"x\" {\n  id = 1\n  name = \"a\"\n}"), PlanOnly: true, ExpectError: exactlyOne},
	)
}

func TestZonesDataSourceFiltersAndPagination(t *testing.T) {
	b := newZoneBackend(t)
	for i := range 450 {
		b.seed(fmt.Sprintf("bulk-%d.example.com", i), b.viewID)
	}
	special := b.seed("special.example.com", b.viewID+7)
	b.mu.Lock()
	special.Type = "Forward"
	b.mu.Unlock()
	zoneCase(t,
		resource.TestStep{
			Config: dataConfig(b, `data "omniddi_dns_zones" "all" {}`),
			Check:  resource.TestCheckResourceAttr("data.omniddi_dns_zones.all", "zones.#", "451"),
		},
		resource.TestStep{
			Config: dataConfig(b, fmt.Sprintf(`data "omniddi_dns_zones" "in_view" {
  view_id = %d
}`, b.viewID+7)),
			Check: resource.TestCheckResourceAttr("data.omniddi_dns_zones.in_view", "zones.0.name", "special.example.com"),
		},
		resource.TestStep{
			Config: dataConfig(b, `data "omniddi_dns_zones" "forward" {
  filters = [{ field = "zoneType", operator = "EQ", value = "Forward" }]
}`),
			Check: resource.TestCheckResourceAttr("data.omniddi_dns_zones.forward", "zones.#", "1"),
		},
		resource.TestStep{
			Config: dataConfig(b, `data "omniddi_dns_zones" "none" {
  filters = [{ field = "name", operator = "EQ", value = "nothing.example.com" }]
}`),
			Check: resource.TestCheckResourceAttr("data.omniddi_dns_zones.none", "zones.#", "0"),
		},
	)
}

func TestZonesDataSourceInvalidFilters(t *testing.T) {
	b := newZoneBackend(t)
	zoneCase(t,
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_zones" "x" { filters = [{ field = "name", operator = "LIKE", value = "a" }] }`),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`operator value must be one of`),
		},
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_zones" "x" { filters = [{ field = "name", operator = "IN" }] }`),
			ExpectError: regexp.MustCompile(`The IN operator needs values`),
		},
		resource.TestStep{
			Config:      dataConfig(b, `data "omniddi_dns_zones" "x" { filters = [{ field = "name", operator = "EQ" }] }`),
			ExpectError: regexp.MustCompile(`The EQ operator needs value`),
		},
	)
}
