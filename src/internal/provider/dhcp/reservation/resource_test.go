package reservation_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/reservation"
)

const addr = "omniddi_dhcp_host_reservation.test"

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	dhcptest.Case(t, dhcptest.Factories(reservation.Resources(), nil), steps...)
}

var ignoreOnImport = []string{"timeouts", "approval_mode", "options"}

func TestLifecycleTagsUpdateAndImport(t *testing.T) {
	b := newBackend(t)
	create := hcl(b, `
  name            = "printer-01"
  ip_address      = "10.20.0.50"
  identifier_type = "Mac Address"
  identifier      = "aa:bb:cc:dd:ee:01"
  description     = "front desk"
  tags            = { owner = "netops" }
  tags_list       = { teams = ["a", "b"] }
`)
	update := hcl(b, `
  name            = "printer-01"
  ip_address      = "10.20.0.51"
  identifier_type = "Mac Address"
  identifier      = "aa:bb:cc:dd:ee:02"
  description     = "moved"
  tags            = { env = "dev" }
`)
	importable := hcl(b, `
  name            = "printer-01"
  ip_address      = "10.20.0.51"
  identifier_type = "Mac Address"
  identifier      = "aa:bb:cc:dd:ee:02"
  description     = "moved"
  tags            = { env = "dev" }
`)
	var id string
	run(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectIdentityValueMatchesState(addr, tfjsonpath.New("id")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_id"), knownvalue.Int64Exact(100)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_cidr"), knownvalue.StringExact("10.20.0.0/24")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("server_group_id"), knownvalue.Int64Exact(7)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("is_dhcp6"), knownvalue.Bool(false)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error { id = s.RootModule().Resources[addr].Primary.ID; return nil },
				resource.TestCheckNoResourceAttr(addr, "approval_mode"),
				func(s *terraform.State) error {
					n, _ := strconv.ParseInt(s.RootModule().Resources[addr].Primary.ID, 10, 64)
					got := b.srv.TagValues(client.TagObjectHostReserv, n)
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
			Check: func(s *terraform.State) error {
				if s.RootModule().Resources[addr].Primary.ID != id {
					return fmt.Errorf("an in-place update replaced the reservation")
				}
				return resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "ip_address", "10.20.0.51"),
					resource.TestCheckResourceAttr(addr, "description", "moved"),
					resource.TestCheckResourceAttr(addr, "subnet_id", "100"),
					resource.TestCheckResourceAttr(addr, "tags_all.env", "dev"),
					resource.TestCheckNoResourceAttr(addr, "tags.owner"),
				)(s)
			},
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "10.20.0.0/24/printer-01", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport,
		},
		resource.TestStep{
			ResourceName: addr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "printer-01", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport,
		},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable},
	)
	if b.count() != 0 {
		t.Errorf("reservations left after destroy: %d", b.count())
	}
}

func TestMACSpellingIsKeptAsConfigured(t *testing.T) {
	b := newBackend(t)
	cfg := hcl(b, `
  name            = "printer-02"
  ip_address      = "10.20.0.60"
  identifier_type = "Mac Address"
  identifier      = "AA-BB-CC-DD-EE-03"
`)
	run(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(addr, "identifier", "AA-BB-CC-DD-EE-03")},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestIPOnlyReservationAndOptions(t *testing.T) {
	b := newBackend(t)
	ipOnly := hcl(b, `
  name       = "reserved-only"
  ip_address = "10.20.0.70"
`)
	withOptions := hcl(b, `
  name       = "reserved-only"
  ip_address = "10.20.0.70"
  options = [
    { code = 3, name = "routers", data = "10.20.0.1", space = "dhcp4", csv_format = true },
    { code = 15, data = "example.test" },
  ]
`)
	run(t,
		resource.TestStep{Config: ipOnly, Check: resource.TestCheckNoResourceAttr(addr, "identifier")},
		resource.TestStep{
			Config: withOptions,
			Check: func(*terraform.State) error {
				opts, _ := b.get(firstID(b)).Options.([]any)
				if len(opts) != 2 {
					return fmt.Errorf("options sent as dhcp_options = %v", b.get(firstID(b)).Options)
				}
				return nil
			},
		},
		resource.TestStep{Config: withOptions, PlanOnly: true},
		resource.TestStep{Config: ipOnly},
	)
}

func firstID(b *backend) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.items {
		return id
	}
	return 0
}

func TestOptionsAreSentOnCreate(t *testing.T) {
	b := newBackend(t)
	run(t, resource.TestStep{
		Config: hcl(b, `
  name       = "opts"
  ip_address = "10.20.0.71"
  options    = [{ code = 6, data = "10.20.0.2" }]
`),
		Check: func(*terraform.State) error {
			reqs := b.srv.RequestsFor(http.MethodPost, "/api/v1/dhcp/reservations")
			if len(reqs) != 1 || fmt.Sprint(reqs[0].Map()["options"]) != "[map[code:6 data:10.20.0.2]]" {
				return fmt.Errorf("create body = %v", reqs)
			}
			return nil
		},
	})
}

func TestSubnetIDChangeReplaces(t *testing.T) {
	b := newBackend(t)
	var first string
	run(t,
		resource.TestStep{
			Config: hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"\nsubnet_id = 100"),
			Check:  func(s *terraform.State) error { first = s.RootModule().Resources[addr].Primary.ID; return nil },
		},
		resource.TestStep{
			Config: hcl(b, "name = \"a\"\nip_address = \"10.30.0.80\"\nsubnet_id = 101"),
			Check: func(s *terraform.State) error {
				if s.RootModule().Resources[addr].Primary.ID == first {
					return fmt.Errorf("a subnet change must replace the reservation")
				}
				return resource.TestCheckResourceAttr(addr, "subnet_cidr", "10.30.0.0/24")(s)
			},
		},
	)
}

func TestMovingIPOutOfSubnetIsRejectedAtPlan(t *testing.T) {
	b := newBackend(t)
	run(t,
		resource.TestStep{Config: hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"")},
		resource.TestStep{Config: hcl(b, "name = \"a\"\nip_address = \"10.30.0.80\""), ExpectError: regexp.MustCompile(`outside\s+the\s+reservation's\s+subnet`)},
	)
}

func TestDuplicateAddressGuard(t *testing.T) {
	b := newBackend(t)
	b.seed("existing", "10.20.0.90")
	run(t,
		resource.TestStep{
			Config:      hcl(b, "name = \"new\"\nip_address = \"10.20.0.90\""),
			ExpectError: regexp.MustCompile(`(?s)already\s+holds\s+10\.20\.0\.90`),
		},
		resource.TestStep{Config: hcl(b, "name = \"new\"\nip_address = \"10.20.0.91\"")},
		resource.TestStep{
			Config:      hcl(b, "name = \"new\"\nip_address = \"10.20.0.90\""),
			ExpectError: regexp.MustCompile(`(?s)already\s+holds\s+10\.20\.0\.90`),
		},
	)
	if got := len(b.srv.RequestsFor(http.MethodPost, "/api/v1/dhcp/reservations")); got != 1 {
		t.Errorf("POST sent %d times; the duplicate must be stopped before any write", got)
	}
}

func TestCreateAdoptsItsOwnAppliedReservationDespiteTheAddressGuard(t *testing.T) {
	b := newBackend(t)
	held := b.seed("printer", "10.20.0.90")
	idType, mac := "Mac Address", "aa:bb:cc:dd:ee:01"
	held.IdentifierType, held.Identifier = &idType, &mac
	applied := b.srv.StageApproval("create_host_reservation", map[string]any{
		"name": "printer", "ip_address": "10.20.0.90", "network_id": float64(held.SubnetID),
		"identifier_type": idType, "identifier": mac, "is_dhcp6": false, "description": nil, "options": nil,
	}, fakeserver.RequestApplied)
	b.srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Hour))
	run(t, resource.TestStep{
		Config: hcl(b, "name = \"printer\"\nip_address = \"10.20.0.90\"\nidentifier_type = \"Mac Address\"\nidentifier = \"AA-BB-CC-DD-EE-01\""),
		ConfigStateChecks: []statecheck.StateCheck{
			statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.Int64Exact(held.ID)),
		},
	})
	if got := len(b.srv.RequestsFor(http.MethodPost, "/api/v1/dhcp/reservations")); got != 0 {
		t.Errorf("POST sent %d times; the applied request must be adopted", got)
	}
}

func TestBackendRejectionsCarryDetail(t *testing.T) {
	b := newBackend(t)
	b.seed("taken", "10.20.0.99")
	run(t,
		resource.TestStep{Config: hcl(b, "name = \"taken\"\nip_address = \"10.20.0.100\""), ExpectError: regexp.MustCompile(`(?s)HTTP 400.*name\s+already\s+exists`)},
		resource.TestStep{Config: hcl(b, "name = \"far\"\nip_address = \"172.16.0.1\""), ExpectError: regexp.MustCompile(`Unable\s+to\s+resolve\s+subnet`)},
	)
}

func TestInvalidConfigurations(t *testing.T) {
	b := newBackend(t)
	run(t,
		resource.TestStep{Config: hcl(b, "name = \"x\"\nip_address = \"10.20.0.5\"\nidentifier = \"aa:bb:cc:dd:ee:ff\""), ExpectError: regexp.MustCompile(`identifier_type`)},
		resource.TestStep{Config: hcl(b, "name = \"x\"\nip_address = \"10.20.0.5\"\nidentifier_type = \"Mac Address\"\nidentifier = \"nonsense\""), ExpectError: regexp.MustCompile(`not\s+a\s+valid\s+MAC\s+address`)},
		resource.TestStep{Config: hcl(b, "name = \"x\"\nip_address = \"10.20.0.5\"\nidentifier_type = \"DUID\"\nidentifier = \"zz\""), ExpectError: regexp.MustCompile(`not\s+a\s+valid\s+DUID`)},
		resource.TestStep{Config: hcl(b, "name = \"x\"\nip_address = \"10.20.0.5\"\nidentifier_type = \"Serial\"\nidentifier = \"1\""), ExpectError: regexp.MustCompile(`identifier_type`)},
		resource.TestStep{Config: hcl(b, "name = \"x\"\nip_address = \"nope\""), ExpectError: regexp.MustCompile(`ip_address`)},
	)
}

func TestImportUnknownNaturalKey(t *testing.T) {
	b := newBackend(t)
	cfg := hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: addr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "10.30.0.0/24/a", nil },
			ExpectError:       regexp.MustCompile(`No object matches`),
		},
	)
}

func TestReadRemovesDeletedReservation(t *testing.T) {
	b := newBackend(t)
	cfg := hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"")
	run(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				b.mu.Lock()
				defer b.mu.Unlock()
				b.items = map[int64]*entry{}
			},
			Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	)
}

func TestApprovalFailModeOnCreateUpdateAndDelete(t *testing.T) {
	b := newBackend(t)
	first := hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"")
	updated := hcl(b, "name = \"a\"\nip_address = \"10.20.0.80\"\ndescription = \"changed\"")
	run(t,
		resource.TestStep{
			PreConfig: func() {
				b.approval = func(string, map[string]any) (fakeserver.Response, bool) {
					return b.srv.ApprovalPending(321, "Host reservation request"), true
				}
			},
			Config: first, ExpectError: regexp.MustCompile(`Approval required \(request 321\)`),
		},
		resource.TestStep{PreConfig: func() { b.approval = nil }, Config: first},
		resource.TestStep{
			PreConfig: func() {
				b.approval = func(string, map[string]any) (fakeserver.Response, bool) {
					return b.srv.ApprovalPending(322, "Host reservation request"), true
				}
			},
			Config: updated, ExpectError: regexp.MustCompile(`Approval required \(request 322\)`),
		},
		resource.TestStep{
			Config: dhcptest.Config(b.srv, ""), ExpectError: regexp.MustCompile(`Approval required \(request 322\)`),
		},
		resource.TestStep{PreConfig: func() { b.approval = nil }, Config: dhcptest.Config(b.srv, "")},
	)
	if b.count() != 0 {
		t.Errorf("reservation survived destroy")
	}
}

func TestApprovalWaitModeCreate(t *testing.T) {
	b := newBackend(t)
	var submitted int64
	b.approval = func(method string, body map[string]any) (fakeserver.Response, bool) {
		if method != http.MethodPost || submitted != 0 {
			return fakeserver.Response{}, false
		}
		submitted = b.srv.StageApproval("create_host_reservation", map[string]any{"name": body["name"]},
			fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)
		b.mu.Lock()
		ip, _ := body["ip_address"].(string)
		item := &entry{ID: b.srv.NextID(), Name: body["name"].(string), IP: ip, SubnetID: b.subnetFor(ip).ID}
		b.items[item.ID] = item
		b.mu.Unlock()
		return b.srv.ApprovalPending(submitted, "Host reservation request"), true
	}
	run(t, resource.TestStep{
		Config: hcl(b, "name = \"waited\"\nip_address = \"10.20.0.80\"\napproval_mode = \"wait\""),
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor(http.MethodPost, "/api/v1/dhcp/reservations")); got != 1 {
				return fmt.Errorf("POST sent %d times", got)
			}
			return nil
		},
	})
}

func TestPendingDuplicateRequestIsReportedNotResubmitted(t *testing.T) {
	b := newBackend(t)
	pending := b.srv.StageApproval("create_host_reservation", map[string]any{"name": "queued"})
	run(t, resource.TestStep{
		Config:      hcl(b, "name = \"queued\"\nip_address = \"10.20.0.80\""),
		ExpectError: regexp.MustCompile(fmt.Sprintf(`Approval required \(request %d\)`, pending)),
	})
	if got := len(b.srv.RequestsFor(http.MethodPost, "/api/v1/dhcp/reservations")); got != 0 {
		t.Errorf("a duplicate request was submitted: %d POSTs", got)
	}
}

func TestGraphQLOperationsMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, reservation.Queries...)
}

func TestDUIDReservationOnADHCPv6Subnet(t *testing.T) {
	b := newBackend(t)
	cfg := hcl(b, `
  name            = "app-server-v6"
  ip_address      = "2001:db8:20::50"
  identifier_type = "DUID"
  identifier      = "00:01:00:01:2c:4d:5e:6f:aa:bb:cc:dd:ee:02"
`)
	run(t,
		resource.TestStep{
			Config: cfg,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("identifier_type"), knownvalue.StringExact("DUID")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("identifier"), knownvalue.StringExact("00:01:00:01:2c:4d:5e:6f:aa:bb:cc:dd:ee:02")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("is_dhcp6"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_cidr"), knownvalue.StringExact("2001:db8:20::/64")),
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: ignoreOnImport},
	)
}
