package subnet_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/subnet"
)

const addr = "omniddi_dhcp_subnet.test"

var ignoreOnImport = []string{"timeouts", "approval_mode", "deletion_protection", "force_destroy"}

func promotePathFor(id int64) string { return "/api/v1/ipam/subnets/" + strconv.FormatInt(id, 10) }

func settingsPathFor(id int64) string { return "/api/v1/dhcp/subnets/" + strconv.FormatInt(id, 10) }

func demotePathFor(id int64) string {
	return "/api/v1/ipam/networks/" + strconv.FormatInt(id, 10) + "/dhcp-config"
}

func TestPromoteReadUpdateAndImport(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.20.0.0/24")
	create := b.hclFor(id, `
  server_group_id     = 7
  deletion_protection = false
  lease_time          = 8
  lease_time_unit     = "hrs"
  enable_ddns         = true
  ddns_config_json    = jsonencode({ ddns_ns_server = "10.20.0.2" })
  client_class_ids    = [5]
  vlan_id             = 3

  option_data = [
    { code = 6, data = "10.20.0.2,10.20.0.3" },
  ]

  pools = [
    {
      pool = "10.20.0.100-10.20.0.200"
      name = "dynamic"
      option_data = [{ code = 3, data = "10.20.0.1" }]
    },
  ]
`)
	update := b.hclFor(id, `
  server_group_id     = 7
  deletion_protection = false
  lease_time          = 2
  lease_time_unit     = "days"
  enable_ddns         = true
  ddns_config_json    = jsonencode({ ddns_ns_server = "10.20.0.2" })
  vlan_id             = 3

  pools = [
    { pool = "10.20.0.50-10.20.0.60" },
    { pool = "10.20.0.100-10.20.0.200", name = "dynamic" },
  ]
`)
	importable := b.hclFor(id, `
  server_group_id  = 7
  lease_time       = 2
  lease_time_unit  = "days"
  enable_ddns      = true
  ddns_config_json = jsonencode({ ddns_ns_server = "10.20.0.2" })
  vlan_id          = 3

  pools = [
    { pool = "10.20.0.50-10.20.0.60" },
    { pool = "10.20.0.100-10.20.0.200", name = "dynamic" },
  ]
`)
	run(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectIdentityValueMatchesState(addr, tfjsonpath.New("id")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_id"), knownvalue.Int64Exact(id)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("cidr"), knownvalue.StringExact("10.20.0.0/24")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("kea_subnet_id"), knownvalue.Int64Exact(id)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("view_id"), knownvalue.Null()),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("shared_network_id"), knownvalue.Null()),
				statecheck.ExpectSensitiveValue(addr, tfjsonpath.New("ddns_config_json")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "id", strconv.FormatInt(id, 10)),
				resource.TestCheckResourceAttr(addr, "lease_time", "8"),
				resource.TestCheckResourceAttr(addr, "pools.0.option_data.0.code", "3"),
				resource.TestCheckResourceAttr(addr, "option_data.0.data", "10.20.0.2,10.20.0.3"),
				resource.TestCheckNoResourceAttr(addr, "option_data.0.name"),
				resource.TestCheckResourceAttr(addr, "client_class_ids.#", "1"),
				resource.TestCheckResourceAttr(addr, "vlan_id", "3"),
				resource.TestCheckNoResourceAttr(addr, "enable_dhcp_service"),
				func(*terraform.State) error {
					promotes := requestBodies(b.srv, http.MethodPut, promotePathFor(id))
					if len(promotes) != 1 || len(promotes[0]) != 1 || promotes[0]["server_group_id"] != float64(7) {
						return fmt.Errorf("the promote call must carry only server_group_id, got %v", promotes)
					}
					settings := requestBodies(b.srv, http.MethodPut, settingsPathFor(id))
					if len(settings) != 1 {
						return fmt.Errorf("settings PUT sent %d times, want 1", len(settings))
					}
					for _, ipamField := range []string{"name", "description", "vlan", "owner", "business_unit_id", "view_id", "auto_create_reverse_zone"} {
						if _, sent := settings[0][ipamField]; sent {
							return fmt.Errorf("the settings PUT sent %q", ipamField)
						}
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
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("cidr"), knownvalue.StringExact("10.20.0.0/24")),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("vlan_id"), knownvalue.Int64Exact(3)),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "lease_time", "2"),
				resource.TestCheckResourceAttr(addr, "lease_time_unit", "days"),
				resource.TestCheckResourceAttr(addr, "pools.#", "2"),
				resource.TestCheckNoResourceAttr(addr, "option_data.0.code"),
				resource.TestCheckResourceAttr(addr, "client_class_ids.#", "0"),
				func(*terraform.State) error {
					settings := requestBodies(b.srv, http.MethodPut, settingsPathFor(id))
					last := settings[len(settings)-1]
					for _, unchanged := range []string{"enable_ddns", "ddns_config", "vlan_id"} {
						if _, sent := last[unchanged]; sent {
							return fmt.Errorf("the update re-sent the unchanged field %q: %v", unchanged, last)
						}
					}
					if last["lease_time"] != float64(2) || last["lease_time_unit"] != "days" {
						return fmt.Errorf("lease time and unit must travel together, got %v", last)
					}
					if got := len(requestBodies(b.srv, http.MethodPut, promotePathFor(id))); got != 1 {
						return fmt.Errorf("an update re-promoted the subnet (%d promote calls)", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "10.20.0.0/24", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport,
		},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable},
		resource.TestStep{Config: update},
	)
	if _, enabled := b.config(id); enabled {
		t.Error("DHCP is still enabled after destroy")
	}
	node, ok := b.ipam.Get(id)
	if !ok || node.SubnetID != nil {
		t.Errorf("destroy must keep the IPAM subnet as a planning-only node, got present=%v node=%+v", ok, node)
	}
	if got := len(b.srv.RequestsFor(http.MethodDelete, settingsPathFor(id))); got != 0 {
		t.Errorf("DELETE /dhcp/subnets/{id} was called %d times; demote must never use it", got)
	}
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 1 {
		t.Errorf("demote called %d times, want 1", got)
	}
}

func TestMinimalConfigHasNoDiffAndNoSettingsCall(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.21.0.0/24")
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	run(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckNoResourceAttr(addr, "pools"),
				resource.TestCheckNoResourceAttr(addr, "lease_time"),
				resource.TestCheckResourceAttr(addr, "deletion_protection", "false"),
				resource.TestCheckResourceAttr(addr, "force_destroy", "false"),
				func(*terraform.State) error {
					if got := len(b.srv.RequestsFor(http.MethodPut, settingsPathFor(id))); got != 0 {
						return fmt.Errorf("a config without DHCP settings sent %d settings PUTs", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{
			Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\napproval_mode = \"wait\""),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
			}},
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor(http.MethodPut, settingsPathFor(id))); got != 0 {
					return fmt.Errorf("a change that only touches provider settings sent %d settings PUTs", got)
				}
				return nil
			},
		},
	)
}

func TestReplaceOnServerGroupChangeDemotesThenPromotes(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.22.0.0/24")
	other := b.seedSubnet("10.23.0.0/24")
	run(t,
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")},
		resource.TestStep{
			Config: b.hclFor(id, "server_group_id = 11\ndeletion_protection = false"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace),
			}},
			Check: func(*terraform.State) error {
				if cfg, ok := b.config(id); !ok || cfg.GroupID != 11 {
					return fmt.Errorf("the subnet is not served by group 11: %+v", cfg)
				}
				if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 1 {
					return fmt.Errorf("demote called %d times, want 1", got)
				}
				return nil
			},
		},
		resource.TestStep{
			Config: b.hclFor(other, "server_group_id = 11\ndeletion_protection = false"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace),
			}},
			Check: func(*terraform.State) error {
				if _, ok := b.config(id); ok {
					return fmt.Errorf("the old subnet still serves DHCP after subnet_id changed")
				}
				if _, ok := b.config(other); !ok {
					return fmt.Errorf("the new subnet does not serve DHCP")
				}
				return nil
			},
		},
	)
	if len(b.srv.RequestsFor(http.MethodDelete, settingsPathFor(id)))+len(b.srv.RequestsFor(http.MethodDelete, settingsPathFor(other))) != 0 {
		t.Error("a replacement must never call DELETE /dhcp/subnets/{id}")
	}
}

func TestReadRemovesSubnetDemotedElsewhere(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.24.0.0/24")
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() { b.disable(id) },
			Config:    cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: cfg,
			Check: func(*terraform.State) error {
				if _, ok := b.config(id); !ok {
					return fmt.Errorf("the subnet was not promoted again")
				}
				return nil
			},
		},
	)
}

func TestReadRemovesSubnetWhoseNodeWasDeleted(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.25.0.0/24")
	cfg := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() { b.disable(id); b.ipam.Remove(id) },
			Config:    cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`IPAM subnet not found`)},
		resource.TestStep{Config: dhcptest.Config(b.srv, "")},
	)
}

func TestCreateRefusesAnIPAMSubnetThatAlreadyServesDHCP(t *testing.T) {
	b := newBackend(t)
	id := b.seedDHCP("10.26.0.0/24", 7)
	run(t, resource.TestStep{
		Config:      b.hclFor(id, "server_group_id = 7\ndeletion_protection = false"),
		ExpectError: regexp.MustCompile(`(?s)already serves DHCP.*terraform\s+import\s+omniddi_dhcp_subnet`),
	})
	if cfg, ok := b.config(id); !ok || cfg.GroupID != 7 {
		t.Errorf("the existing DHCP configuration was altered: %+v", cfg)
	}
}

func TestCreateValidatesTheTargetNode(t *testing.T) {
	b := newBackend(t)
	container := b.seedContainer("10.0.0.0/8")
	run(t,
		resource.TestStep{
			Config:      b.hclFor(9999, "server_group_id = 7\ndeletion_protection = false"),
			ExpectError: regexp.MustCompile(`No IPAM subnet has id 9999`),
		},
		resource.TestStep{
			Config:      b.hclFor(container, "server_group_id = 7\ndeletion_protection = false"),
			ExpectError: regexp.MustCompile(`(?s)cannot\s+get\s+DHCP.*is\s+a\s+CONTAINER,\s+not\s+a\s+SUBNET`),
		},
	)
	if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(container))); got != 0 {
		t.Errorf("a non-subnet node was sent a promote call %d times", got)
	}
}

func TestBackendRejectionsCarryDetail(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.27.0.0/24")
	run(t,
		resource.TestStep{
			Config:      b.hclFor(id, "server_group_id = 8\ndeletion_protection = false"),
			ExpectError: regexp.MustCompile(`(?s)HTTP 404.*DHCP\s+server\s+not\s+found`),
		},
		resource.TestStep{
			Config:      b.hclFor(id, "server_group_id = 9\ndeletion_protection = false"),
			ExpectError: regexp.MustCompile(`(?s)HTTP 409.*No\s+agent\s+mapped`),
		},
	)
}

func TestInvalidConfigIsRejectedAtPlanTime(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.28.0.0/24")
	base := "server_group_id = 7\ndeletion_protection = false\n"
	cases := map[string]string{
		`subnet_id`:                       "subnet_id = 0\nserver_group_id = 7",
		`server_group_id`:                 fmt.Sprintf("subnet_id = %d\nserver_group_id = 0", id),
		`not\s+a\s+range`:                 fmt.Sprintf("subnet_id = %d\n%spools = [{ pool = \"10.28.0.5\" }]", id, base),
		`ends\s+\(10.28.0.1\)\s+before`:   fmt.Sprintf("subnet_id = %d\n%spools = [{ pool = \"10.28.0.5-10.28.0.1\" }]", id, base),
		`not\s+an\s+IP\s+address`:         fmt.Sprintf("subnet_id = %d\n%spools = [{ pool = \"a-b\" }]", id, base),
		`mixes\s+IPv4\s+and\s+IPv6`:       fmt.Sprintf("subnet_id = %d\n%spools = [{ pool = \"10.28.0.1-2001:db8::1\" }]", id, base),
		`lease_time_unit`:                 fmt.Sprintf("subnet_id = %d\n%slease_time_unit = \"weeks\"", id, base),
		`lease_time`:                      fmt.Sprintf("subnet_id = %d\n%slease_time = -1", id, base),
		`Invalid\s+JSON\s+object`:         fmt.Sprintf("subnet_id = %d\n%sddns_config_json = jsonencode([1])", id, base),
		`approval_mode`:                   fmt.Sprintf("subnet_id = %d\n%sapproval_mode = \"later\"", id, base),
		`(?s)code.*required|Missing`:      fmt.Sprintf("subnet_id = %d\n%soption_data = [{ data = \"x\" }]", id, base),
		`(?s)client_class_id.*at\s+least`: fmt.Sprintf("subnet_id = %d\n%spools = [{ pool = \"10.28.0.1-10.28.0.2\", client_class_id = 0 }]", id, base),
	}
	var steps []resource.TestStep
	for pattern, body := range cases {
		steps = append(steps, resource.TestStep{Config: b.hcl(body), PlanOnly: true, ExpectError: regexp.MustCompile(pattern)})
	}
	run(t, steps...)
	if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(id))); got != 0 {
		t.Errorf("an invalid configuration reached the API (%d promote calls)", got)
	}
}

func TestSettingsFailureAfterPromoteTaintsAndLowersProtection(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.29.0.0/24")
	run(t, resource.TestStep{
		Config:      b.hclFor(id, fmt.Sprintf("server_group_id = 7\nvlan_id = %d", missingVLAN)),
		ExpectError: regexp.MustCompile(`(?s)settings\s+could\s+not\s+be\s+applied.*VLAN\s+not\s+found.*deletion_protection\s+was\s+set\s+to\s+false`),
	})
	if _, enabled := b.config(id); enabled {
		t.Error("the tainted subnet was not demoted by the final destroy")
	}
}

func TestReadBackFailureAfterCreateTaints(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.30.0.0/24")
	b.failFetchAfterPut = true
	run(t, resource.TestStep{
		Config:      b.hclFor(id, "server_group_id = 7"),
		ExpectError: regexp.MustCompile(`(?s)could\s+not\s+be\s+read\s+back.*deletion_protection\s+was\s+set\s+to\s+false`),
	})
}

func TestFailureAfterUpdateKeepsPriorStateAndTheNextRefreshReconciles(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.31.0.0/24")
	before := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\nlease_time = 60")
	after := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\nlease_time = 120")
	run(t,
		resource.TestStep{Config: before},
		resource.TestStep{
			PreConfig:   func() { b.failFetchAfterPut = true },
			Config:      after,
			ExpectError: regexp.MustCompile(`(?s)could\s+not\s+be\s+read\s+back.*previous\s+values`),
		},
		resource.TestStep{
			PreConfig: func() { b.failFetchAfterPut = false },
			Config:    after,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionNoop),
			}},
			Check: resource.TestCheckResourceAttr(addr, "lease_time", "120"),
		},
	)
}

func TestSettingsUpdateRejectionLeavesPriorState(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.32.0.0/24")
	run(t,
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")},
		resource.TestStep{
			Config:      b.hclFor(id, fmt.Sprintf("server_group_id = 7\ndeletion_protection = false\nvlan_id = %d", missingVLAN)),
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+the\s+DHCP\s+subnet.*VLAN\s+not\s+found`),
		},
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false"), PlanOnly: true},
	)
}

func TestCreateAdoptsThePromoteAfterAnAmbiguousFailure(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.33.0.0/24")
	b.failAfterPromote = true
	run(t, resource.TestStep{
		Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false"),
		Check: func(s *terraform.State) error {
			if got := len(b.srv.RequestsFor(http.MethodPut, promotePathFor(id))); got != 1 {
				return fmt.Errorf("promote sent %d times, want 1", got)
			}
			return resource.TestCheckResourceAttr(addr, "server_group_id", "7")(s)
		},
	})
}

func TestDeletionProtectionBlocksDestroy(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.35.0.0/24")
	run(t,
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7"), Check: resource.TestCheckResourceAttr(addr, "deletion_protection", "true")},
		resource.TestStep{Config: dhcptest.Config(b.srv, ""), ExpectError: regexp.MustCompile(`Deletion protection is enabled`)},
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")},
		resource.TestStep{Config: dhcptest.Config(b.srv, "")},
	)
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 1 {
		t.Errorf("demote sent %d times; protection must stop the first destroy before any API call", got)
	}
}

func TestForceDestroyGuardsReservationsAndActiveLeases(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.36.0.0/24")
	protected := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")
	forced := b.hclFor(id, "server_group_id = 7\ndeletion_protection = false\nforce_destroy = true")
	run(t,
		resource.TestStep{Config: protected},
		resource.TestStep{
			PreConfig:   func() { b.setCounts(id, 2, leaseCounts{}) },
			Config:      dhcptest.Config(b.srv, ""),
			ExpectError: regexp.MustCompile(`(?s)Refusing\s+to\s+demote.*2\s+host\s+reservation\(s\).*force_destroy`),
		},
		resource.TestStep{
			PreConfig:   func() { b.setCounts(id, 0, leaseCounts{Active: 3, Expired: 4}) },
			Config:      dhcptest.Config(b.srv, ""),
			ExpectError: regexp.MustCompile(`3\s+active\s+lease\(s\)`),
		},
		resource.TestStep{
			PreConfig:   func() { b.setCounts(id, 1, leaseCounts{Active: 1}) },
			Config:      dhcptest.Config(b.srv, ""),
			ExpectError: regexp.MustCompile(`1\s+host\s+reservation\(s\)\s+and\s+1\s+active\s+lease\(s\)`),
		},
		resource.TestStep{
			PreConfig:   func() { b.setCounts(id, 0, leaseCounts{}); b.failCounts = true },
			Config:      dhcptest.Config(b.srv, ""),
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+check.*read_hosts`),
		},
		resource.TestStep{PreConfig: func() { b.failCounts = false; b.setCounts(id, 0, leaseCounts{Expired: 9}) }, Config: protected, PlanOnly: true},
		resource.TestStep{PreConfig: func() { b.setCounts(id, 5, leaseCounts{Active: 5}) }, Config: forced},
		resource.TestStep{Config: dhcptest.Config(b.srv, "")},
	)
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 1 {
		t.Errorf("demote sent %d times, want exactly the forced one", got)
	}
	if _, ok := b.ipam.Get(id); !ok {
		t.Error("the IPAM subnet did not survive a forced demote")
	}
}

func TestDestroyIsIdempotentWhenDemotedElsewhere(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.37.0.0/24")
	run(t,
		resource.TestStep{Config: b.hclFor(id, "server_group_id = 7\ndeletion_protection = false")},
		resource.TestStep{PreConfig: func() { b.disable(id) }, Config: dhcptest.Config(b.srv, "")},
	)
	if got := len(b.srv.RequestsFor(http.MethodDelete, demotePathFor(id))); got != 0 {
		t.Errorf("demote sent %d times for a subnet that no longer served DHCP", got)
	}
}

func TestImportErrors(t *testing.T) {
	b := newBackend(t)
	planning := b.seedSubnet("10.38.0.0/24")
	b.seedDHCP("10.39.0.0/24", 7)
	cfg := b.hclFor(planning, "server_group_id = 7\ndeletion_protection = false")
	importKey := func(key string) resource.TestStep {
		return resource.TestStep{
			ResourceName: addr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return key, nil },
		}
	}
	notFound := importKey("10.38.0.0/24")
	notFound.ExpectError = regexp.MustCompile(`No\s+object\s+matches\s+"10.38.0.0/24"`)
	unknown := importKey("192.0.2.0/24")
	unknown.ExpectError = regexp.MustCompile(`No\s+object\s+matches`)
	planningID := importKey(strconv.FormatInt(planning, 10))
	planningID.ExpectError = regexp.MustCompile(`(?i)non-existent\s+remote\s+object`)
	run(t, notFound, unknown, planningID)
}

func TestGraphQLOperationsMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, subnet.Queries...)
}

func TestPromoteRejectionOfAPlanningSubnetIsNotReportedAsAlreadyServing(t *testing.T) {
	b := newBackend(t)
	id := b.seedSubnet("10.28.0.0/24")
	run(t, resource.TestStep{
		Config:      b.hclFor(id, fmt.Sprintf("server_group_id = %d\ndeletion_protection = false", groupNoPrimary)),
		ExpectError: regexp.MustCompile(`(?s)Unable to enable DHCP on the IPAM subnet.*no\s+active\s+primary\s+DHCP\s+server`),
	})
}

func TestImportByCIDROfAConfiguredSubnet(t *testing.T) {
	b := newBackend(t)
	id := b.seedDHCP("10.29.0.0/24", 7)
	run(t, resource.TestStep{
		ResourceName: addr, ImportState: true,
		Config:            b.hclFor(id, "server_group_id = 7\ndeletion_protection = false"),
		ImportStateIdFunc: func(*terraform.State) (string, error) { return "10.29.0.0/24", nil },
		ImportStateCheck: func(states []*terraform.InstanceState) error {
			if len(states) != 1 || states[0].ID != strconv.FormatInt(id, 10) {
				return fmt.Errorf("imported %v, want id %d", states, id)
			}
			return nil
		},
	})
}
