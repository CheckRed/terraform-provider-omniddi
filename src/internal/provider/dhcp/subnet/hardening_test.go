package subnet_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestConfiguredOptionDescriptorsSurviveServerEnrichment(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.70.0.0/24")
	cfg := b.hclFor(id, `
server_group_id     = 7
deletion_protection = false

option_data = [
  { code = 3, name = "routers", data = "10.70.0.1", space = "dhcp4", csv_format = true, data_type = "ipv4-address" },
  { code = 6, data = "10.70.0.2" },
]
`)
	run(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "option_data.0.name", "routers"),
				resource.TestCheckResourceAttr(addr, "option_data.0.data_type", "ipv4-address"),
				resource.TestCheckNoResourceAttr(addr, "option_data.1.name"),
				func(*terraform.State) error {
					if stored, _ := b.config(id); len(stored.Options) != 2 {
						return fmt.Errorf("stored options = %v", stored.Options)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestDDNSConfigKeepsTheConfiguredJSONWhenTheServerAddsKeys(t *testing.T) {
	b := newBackend(t)
	b.enrichDDNS = true
	id := b.seedSubnet("10.71.0.0/24")
	cfg := b.hclFor(id, `
server_group_id     = 7
deletion_protection = false
enable_ddns         = true
ddns_config_json    = jsonencode({ ddns_ns_server = "10.71.0.2", secure_updates = true })
`)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{
			PreConfig: func() {
				b.edit(id, func(c *dhcpConfig) {
					c.Config["ddns_config"] = map[string]any{"ddns_ns_server": "192.0.2.53", "secure_updates": true}
				})
			},
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			}},
			Check: func(*terraform.State) error {
				bodies := requestBodies(b.srv, http.MethodPut, settingsPathFor(id))
				if last := bodies[len(bodies)-1]; last["ddns_config"] == nil {
					return fmt.Errorf("the drifted DDNS configuration was not restored: %v", last)
				}
				return nil
			},
		},
	)
}

func TestSettingsChangedElsewhereAreReadWithoutDiffForComputedValues(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.72.0.0/24")
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	view, shared := int64(4), int64(9)
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				b.edit(id, func(c *dhcpConfig) { c.ViewID, c.SharedNetwork = &view, &shared })
			},
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionNoop),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "view_id", "4"),
				resource.TestCheckResourceAttr(addr, "shared_network_id", "9"),
			),
		},
	)
}

func TestPoolsAndOptionsAreClearedWhenRemovedFromConfig(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.73.0.0/24")
	withAll := b.hclFor(id, `
server_group_id     = 7
deletion_protection = false
client_class_ids    = [5, 6]
option_data         = [{ code = 15, data = "example.test" }]
pools               = [{ pool = "10.73.0.10-10.73.0.20" }]
`)
	bare := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: withAll},
		resource.TestStep{
			Config: bare,
			Check: func(*terraform.State) error {
				cfg, _ := b.config(id)
				if len(cfg.Pools) != 0 || len(cfg.Options) != 0 || len(cfg.ClientClasses) != 0 {
					return fmt.Errorf("removed lists were not cleared on the server: %+v", cfg)
				}
				return nil
			},
		},
		resource.TestStep{Config: bare, PlanOnly: true},
	)
}
