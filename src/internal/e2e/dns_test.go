package e2e_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type dnsFixture struct {
	view string
	name string
	ttl  int
}

func (f dnsFixture) zone() string { return f.name + ".test" }

func (f dnsFixture) base() string {
	return providerBlock() + fmt.Sprintf(`
data "omniddi_dns_view" "test" {
  name = %[1]q
}

resource "omniddi_dns_zone" "test" {
  view_id             = data.omniddi_dns_view.test.id
  name                = %[2]q
  ttl                 = 3600
  email               = "hostmaster@%[2]s"
  deletion_protection = false
}
`, f.view, f.zone())
}

func (f dnsFixture) withRecords() string {
	return f.base() + fmt.Sprintf(`
resource "omniddi_dns_record" "www" {
  zone_id = omniddi_dns_zone.test.id
  name    = "www"
  type    = "A"
  ttl     = %[1]d
  values  = ["192.0.2.10", "192.0.2.11"]
}

resource "omniddi_dns_record" "alias" {
  zone_id = omniddi_dns_zone.test.id
  name    = "app"
  type    = "CNAME"
  values  = ["www.%[2]s."]
}

resource "omniddi_dns_record" "txt" {
  zone_id = omniddi_dns_zone.test.id
  name    = "_verify"
  type    = "TXT"
  values  = ["terraform-e2e"]
}

resource "omniddi_dns_record" "mx" {
  zone_id = omniddi_dns_zone.test.id
  name    = "@"
  type    = "MX"

  mx {
    preference = 10
    exchange   = "mail.%[2]s."
  }
}
`, f.ttl, f.zone())
}

func naturalKey(parts ...string) func(*terraform.State) (string, error) {
	return func(*terraform.State) (string, error) {
		key := parts[0]
		for _, p := range parts[1:] {
			key += "/" + p
		}
		return key, nil
	}
}

func TestAccDNS_ZoneRecordsLifecycle(t *testing.T) {
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance tests skipped unless env %q is set", resource.EnvTfAcc)
	}
	f := dnsFixture{view: dnsViewName(t), name: uniqueName(), ttl: 300}
	updated := f
	updated.ttl = 120
	settle := zoneSettle()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:           f.base(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_dns_zone.test", "name", f.zone()),
					resource.TestCheckResourceAttr("omniddi_dns_zone.test", "network_scope", "Internal"),
				),
			},
			{
				PreConfig:        func() { time.Sleep(settle) },
				Config:           f.withRecords(),
				ConfigPlanChecks: emptyPlanAfterApply(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("omniddi_dns_record.www", "values.#", "2"),
					resource.TestCheckResourceAttr("omniddi_dns_record.www", "ttl", "300"),
					resource.TestCheckResourceAttr("omniddi_dns_record.alias", "type", "CNAME"),
					resource.TestCheckResourceAttr("omniddi_dns_record.mx", "mx.#", "1"),
				),
			},
			{
				Config:           updated.withRecords(),
				ConfigPlanChecks: updatesInPlace("omniddi_dns_record.www"),
				Check:            resource.TestCheckResourceAttr("omniddi_dns_record.www", "ttl", "120"),
			},
			{ResourceName: "omniddi_dns_zone.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: commonImportIgnore},
			{
				ResourceName: "omniddi_dns_zone.test", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: naturalKey(f.view, f.zone()), ImportStateVerifyIgnore: commonImportIgnore,
			},
			{ResourceName: "omniddi_dns_record.www", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: commonImportIgnore},
			{
				ResourceName: "omniddi_dns_record.mx", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: naturalKey(f.view, f.zone(), "@", "MX"), ImportStateVerifyIgnore: commonImportIgnore,
			},
		},
	})
}
