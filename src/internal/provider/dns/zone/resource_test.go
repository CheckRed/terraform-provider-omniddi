package zone_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const zoneAddr = "omniddi_dns_zone.test"

var importIgnore = []string{"zone_acl_id", "deletion_protection", "force_destroy", "timeouts", "approval_mode"}

func zoneCase(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	t.Parallel()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: zoneFactories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    steps,
	})
}

func TestZoneLifecycleUpdateImportAndNoDiff(t *testing.T) {
	b := newZoneBackend(t)
	create := zoneConfig(b, `
  name                      = "corp.example.com"
  ttl                       = 7200
  email                     = "hostmaster@example.com"
  enable_dnssec             = true
  zone_acl_id               = 4
  zone_transfer_tsig_key_id = 12
  deletion_protection       = false
  tags                      = { owner = "netops" }
`)
	update := zoneConfig(b, `
  name                = "corp.example.com"
  ttl                 = 9000
  email               = "hostmaster@example.com"
  enable_dnssec       = false
  zone_acl_id         = 4
  deletion_protection = false
  tags                = { env = "dev" }
`)
	importable := zoneConfig(b, `
  name          = "corp.example.com"
  ttl           = 9000
  email         = "hostmaster@example.com"
  enable_dnssec = false
  tags          = { env = "dev" }
`)

	zoneCase(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(zoneAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectIdentityValueMatchesState(zoneAddr, tfjsonpath.New("id")),
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "zone_type", "Primary"),
				resource.TestCheckResourceAttr(zoneAddr, "network_scope", "Internal"),
				resource.TestCheckResourceAttr(zoneAddr, "ttl", "7200"),
				resource.TestCheckResourceAttr(zoneAddr, "refresh", "3600"),
				resource.TestCheckResourceAttr(zoneAddr, "expiry", "604800"),
				resource.TestCheckResourceAttr(zoneAddr, "minimum", "300"),
				resource.TestCheckResourceAttr(zoneAddr, "soa_serial", "2026010101"),
				resource.TestCheckResourceAttr(zoneAddr, "view_name", seededViewName),
				resource.TestCheckResourceAttr(zoneAddr, "enable_dnssec", "true"),
				resource.TestCheckResourceAttr(zoneAddr, "zone_transfer_tsig_key_id", "12"),
				resource.TestCheckResourceAttr(zoneAddr, "business_unit_ids.#", "1"),
				resource.TestCheckNoResourceAttr(zoneAddr, "enable_dynamic_dns"),
				resource.TestCheckNoResourceAttr(zoneAddr, "primary_servers.#"),
				func(*terraform.State) error {
					z := b.first()
					if got := b.srv.TagValues(client.TagObjectDNSZone, z.ID); fmt.Sprint(got) != "map[env:[prod] owner:[netops]]" {
						return fmt.Errorf("backend tags = %v", got)
					}
					post := b.srv.RequestsFor("POST", "/api/v1/zones/")
					body := (&post[0]).Map()
					if got := sortedKeys(body); got != "email,enable_dnssec,name,network_scope,ttl,view_id,zone_acl_id,zone_transfer_tsig_key_id,zone_type" {
						return fmt.Errorf("create sent %s", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{
			Config: update,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "ttl", "9000"),
				resource.TestCheckResourceAttr(zoneAddr, "enable_dnssec", "false"),
				resource.TestCheckNoResourceAttr(zoneAddr, "zone_transfer_tsig_key_id"),
				resource.TestCheckResourceAttr(zoneAddr, "tags_all.env", "dev"),
				func(*terraform.State) error {
					body := lastBody(b, "PUT", b.first().ID)
					if got := sortedKeys(body); got != "enable_dnssec,ttl,zone_transfer_tsig_key_id" {
						return fmt.Errorf("update must send only the changed fields, sent %s", got)
					}
					if body["zone_transfer_tsig_key_id"] != nil {
						return fmt.Errorf("a removed key reference must be sent as null: %v", body)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: zoneAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
		resource.TestStep{ResourceName: zoneAddr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: importable},
		resource.TestStep{
			ResourceName: zoneAddr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return seededViewName + "/corp.example.com", nil },
			ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore,
		},
	)
	if b.count() != 0 {
		t.Errorf("zone was not deleted at the end of the test")
	}
}

func TestZoneNameCaseAndTrailingDotDoNotCauseDiffs(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, "name = \"Corp.Example.com.\"\ndeletion_protection = false")
	zoneCase(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(zoneAddr, "name", "Corp.Example.com.")},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{
			Config: zoneConfig(b, "name = \"corp.example.com\"\ndeletion_protection = false"),
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 1 {
					return fmt.Errorf("respelling the name must not replace the zone, POST count %d", got)
				}
				if got := len(b.srv.RequestsFor("PUT", fmt.Sprintf("/api/v1/zones/%d", b.viewID+1))); got != 0 {
					return fmt.Errorf("respelling the name must not send an update, PUT count %d", got)
				}
				return nil
			},
		},
	)
}

func TestZoneReverseNameIsDerivedFromCIDR(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, `
  zone_type           = "Reverse"
  protocol            = "IPv4"
  cidr                = "192.0.2.0/24"
  deletion_protection = false
`)
	zoneCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "name", "2.0.192.in-addr.arpa"),
				resource.TestCheckResourceAttr(zoneAddr, "cidr", "192.0.2.0/24"),
				resource.TestCheckResourceAttr(zoneAddr, "protocol", "IPv4"),
				func(*terraform.State) error {
					post := b.srv.RequestsFor("POST", "/api/v1/zones/")
					if body := (&post[0]).Map(); body["name"] != nil || body["cidr"] != "192.0.2.0/24" {
						return fmt.Errorf("reverse zone must be created from protocol and cidr: %v", body)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: zoneAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}

func TestZoneSecondaryPrimaryServersAndTsigKey(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, `
  name      = "sec.example.com"
  zone_type = "Secondary"
  primary_servers = [
    { ip = "198.51.100.10", tsig_key_id = 5 },
    { ip = "198.51.100.11", port = 5353 },
  ]
  deletion_protection = false
`)
	changed := zoneConfig(b, `
  name      = "sec.example.com"
  zone_type = "Secondary"
  primary_servers = [{ ip = "198.51.100.12" }]
  deletion_protection = false
`)
	zoneCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "primary_servers.#", "2"),
				resource.TestCheckResourceAttr(zoneAddr, "primary_servers.0.port", "53"),
				resource.TestCheckResourceAttr(zoneAddr, "primary_servers.0.tsig_key_id", "5"),
				resource.TestCheckResourceAttr(zoneAddr, "primary_servers.1.port", "5353"),
				resource.TestCheckNoResourceAttr(zoneAddr, "primary_servers.1.tsig_key_id"),
				resource.TestCheckResourceAttr(zoneAddr, "secondary_sync_tsig_key_id", "5"),
				resource.TestCheckResourceAttr(zoneAddr, "secure_sync", "true"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{Config: changed, Check: resource.TestCheckResourceAttr(zoneAddr, "primary_servers.0.ip", "198.51.100.12")},
		resource.TestStep{Config: changed, PlanOnly: true},
	)
}

func TestZoneForwardNameserversAndPolicy(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, `
  name                = "fwd.example.com"
  zone_type           = "Forward"
  network_scope       = "External"
  nameservers         = ["198.51.100.53", "2001:db8::53"]
  policy              = "only"
  deletion_protection = false
`)
	zoneCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "nameservers.#", "2"),
				resource.TestCheckResourceAttr(zoneAddr, "policy", "only"),
				resource.TestCheckResourceAttr(zoneAddr, "network_scope", "External"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestZoneImmutableAttributesReplace(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(view, name string) string {
		return providerBlock(b) + fmt.Sprintf("resource \"omniddi_dns_zone\" \"test\" {\n  view_id = %s\n  name = %q\n  deletion_protection = false\n}\n", view, name)
	}
	other := fmt.Sprint(b.viewID + 1000)
	zoneCase(t,
		resource.TestStep{Config: cfg(fmt.Sprint(b.viewID), "a.example.com")},
		resource.TestStep{Config: cfg(fmt.Sprint(b.viewID), "b.example.com"), Check: resource.TestCheckResourceAttr(zoneAddr, "name", "b.example.com")},
		resource.TestStep{Config: cfg(other, "b.example.com"), Check: resource.TestCheckResourceAttr(zoneAddr, "view_id", other)},
	)
	if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 3 {
		t.Errorf("expected 3 creates (create + two replacements), got %d", got)
	}
}

func TestZoneInvalidConfigurations(t *testing.T) {
	b := newZoneBackend(t)
	invalid := func(body, pattern string) resource.TestStep {
		return resource.TestStep{Config: zoneConfig(b, body), PlanOnly: true, ExpectError: regexp.MustCompile(pattern)}
	}
	zoneCase(t,
		invalid(`ttl = 60`, `name is required`),
		invalid(`name = "bad_name.example.com"`, `invalid label`),
		invalid(`name = "a.example.com"
zone_type = "Stub"`, `zone_type value must be one of`),
		invalid(`name = "a.example.com"
zone_type = "Forward"`, `Forward zone requires at least one entry in nameservers`),
		invalid(`name = "a.example.com"
zone_type = "Secondary"`, `Secondary zone requires at least one entry in primary_servers`),
		invalid(`name = "a.example.com"
nameservers = ["198.51.100.1"]`, `nameservers only apply to a Forward zone`),
		invalid(`name = "a.example.com"
primary_servers = [{ ip = "198.51.100.1" }]`, `primary_servers only apply to a Secondary zone`),
		invalid(`name = "a.example.com"
policy = "only"`, `policy only applies to a Forward zone`),
		invalid(`zone_type = "Reverse"
protocol = "IPv4"`, `(?s)protocol and cidr must be set together|name is required`),
		invalid(`name = "a.example.com"
cidr = "192.0.2.0/24"
protocol = "IPv4"`, `protocol and cidr only apply to a Reverse zone`),
		invalid(`zone_type = "Reverse"
protocol = "IPv4"
cidr = "192.0.2.0/20"`, `needs a /8, /16, /24 or /32 prefix`),
		invalid(`zone_type = "Reverse"
protocol = "IPv6"
cidr = "192.0.2.0/24"`, `does not match protocol`),
		invalid(`name = "a.example.com"
refresh = 100`, `refresh.*between 300`),
		invalid(`name = "a.example.com"
zone_type = "Forward"
nameservers = ["not-an-ip"]`, `not a valid IPv4 or IPv6 address`),
		invalid(`name = "a.example.com"
zone_type = "Secondary"
primary_servers = [{ ip = "198.51.100.1" }, { ip = "198.51.100.1", port = 53 }]`, `Duplicate primary server`),
		invalid(`name = "a.example.com"
primary_name_server = "ns1"`, `not a valid IPv4 or IPv6 address`),
		invalid(`name = "a.example.com"
approval_mode = "maybe"`, `approval_mode`),
	)
	if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 0 {
		t.Errorf("invalid configurations must not reach the backend, %d POSTs", got)
	}
}

func TestZoneDeletionProtectionBlocksDestroy(t *testing.T) {
	b := newZoneBackend(t)
	protected := zoneConfig(b, "name = \"a.example.com\"")
	unprotected := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t,
		resource.TestStep{Config: protected, Check: resource.TestCheckResourceAttr(zoneAddr, "deletion_protection", "true")},
		resource.TestStep{Config: providerBlock(b), ExpectError: regexp.MustCompile(`Deletion protection is enabled`)},
		resource.TestStep{Config: unprotected},
		resource.TestStep{Config: providerBlock(b)},
	)
	if got := len(b.srv.RequestsFor("DELETE", fmt.Sprintf("/api/v1/zones/%d", b.viewID+1))); got != 1 {
		t.Errorf("DELETE sent %d times; protection must stop the first destroy before any API call", got)
	}
}

func TestZoneForceDestroyGuardsUserRecords(t *testing.T) {
	b := newZoneBackend(t)
	cfg := func(extra string) string {
		return zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false\n"+extra)
	}
	zoneCase(t,
		resource.TestStep{Config: cfg("")},
		resource.TestStep{
			PreConfig:   func() { b.setUserRecords(3) },
			Config:      providerBlock(b),
			ExpectError: regexp.MustCompile(`still has 3 record\(s\) that were not created automatically`),
		},
		resource.TestStep{Config: cfg("force_destroy = true\n")},
		resource.TestStep{Config: providerBlock(b)},
	)
	if b.count() != 0 {
		t.Errorf("zone was not deleted after force_destroy")
	}
	if got := len(b.srv.RequestsFor("DELETE", fmt.Sprintf("/api/v1/zones/%d", b.viewID+1))); got != 1 {
		t.Errorf("DELETE sent %d times, want exactly one after force_destroy", got)
	}
}

func TestZoneFailedTaskKeepsStateAndIsReplacedNextApply(t *testing.T) {
	b := newZoneBackend(t)
	b.taskFinal, b.logs = []string{fakeserver.StatusInprogress, fakeserver.StatusIncomplete}, "named-checkzone failed: bad serial"
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)agent task \d+ failed.*corr-1.*named-checkzone failed: bad serial.*saved to the Terraform state`)},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{fakeserver.StatusCompleted}, "" },
			Config:    cfg,
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 2 {
					return fmt.Errorf("the tainted zone must be replaced, POST count %d", got)
				}
				return nil
			},
		},
	)
}

func TestZoneFakeSuccessResponseIsRejected(t *testing.T) {
	b := newZoneBackend(t)
	b.legacy = true
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`returned no correlation_id`)})
}

func TestZoneApprovalFailModeReportsUntrackedRequest(t *testing.T) {
	b := newZoneBackend(t)
	b.gate = func(method string, _ map[string]any) (fakeserver.Response, bool) {
		return b.srv.ApprovalUntracked(), method == http.MethodPost
	}
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`did not return a request id`)})
	if b.count() != 0 {
		t.Errorf("nothing may be created while approval is pending")
	}
}

func TestZoneApprovalWaitModeResumesUntrackedRequest(t *testing.T) {
	b := newZoneBackend(t)
	submitted := false
	b.gate = func(method string, _ map[string]any) (fakeserver.Response, bool) {
		if method != http.MethodPost || submitted {
			return fakeserver.Response{}, false
		}
		submitted = true
		b.srv.StageApproval("create_zone", map[string]any{"name": "a.example.com", "view_id": b.viewID}, fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)
		b.seed("a.example.com", b.viewID)
		return b.srv.ApprovalUntracked(), true
	}
	cfg := zoneConfig(b, "name = \"A.Example.com\"\napproval_mode = \"wait\"\ndeletion_protection = false")
	zoneCase(t, resource.TestStep{
		Config: cfg,
		Check: func(*terraform.State) error {
			if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 1 {
				return fmt.Errorf("POST sent %d times", got)
			}
			return nil
		},
	})
}

func TestZonePendingDuplicateIsNotResubmitted(t *testing.T) {
	b := newZoneBackend(t)
	pending := b.srv.StageApproval("create_zone", map[string]any{"name": "a.example.com", "view_id": b.viewID})
	b.srv.StageApproval("create_zone", map[string]any{"name": "other.example.com", "view_id": b.viewID})
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(fmt.Sprintf(`Approval required \(request %d\)`, pending))})
	if got := len(b.srv.RequestsFor("POST", "/api/v1/zones/")); got != 0 {
		t.Errorf("a duplicate request must not be submitted, POST count = %d", got)
	}
}

func TestZoneUpdateAndDeleteUnderApproval(t *testing.T) {
	b := newZoneBackend(t)
	original := zoneConfig(b, "name = \"a.example.com\"\nttl = 3000\ndeletion_protection = false")
	changed := zoneConfig(b, "name = \"a.example.com\"\nttl = 4000\ndeletion_protection = false")
	hold := false
	b.gate = func(method string, _ map[string]any) (fakeserver.Response, bool) {
		if !hold || method == http.MethodPost {
			return fakeserver.Response{}, false
		}
		return b.srv.ApprovalPending(88, "Zone change request"), true
	}
	zoneCase(t,
		resource.TestStep{Config: original},
		resource.TestStep{PreConfig: func() { hold = true }, Config: changed, ExpectError: regexp.MustCompile(`Approval required \(request 88\)`)},
		resource.TestStep{Config: changed, PlanOnly: true, ExpectNonEmptyPlan: true},
		resource.TestStep{Config: providerBlock(b), ExpectError: regexp.MustCompile(`Approval required \(request 88\)`)},
		resource.TestStep{PreConfig: func() { hold = false }, Config: providerBlock(b)},
	)
	if b.count() != 0 {
		t.Errorf("zone was not deleted once approval was lifted")
	}
}

func TestZoneUnknownTagFailsAfterSavingState(t *testing.T) {
	b := newZoneBackend(t)
	bad := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false\ntags = { nonexistent = \"x\" }")
	good := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t,
		resource.TestStep{Config: bad, ExpectError: regexp.MustCompile(`(?s)tag definition\(s\) not found: nonexistent.*saved to the Terraform state`)},
		resource.TestStep{Config: good},
	)
}

func TestZoneReadRemovesDeletedObject(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	zoneCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{PreConfig: b.wipe, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
	)
}

func TestZoneImportUnknownKeys(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, "name = \"a.example.com\"\ndeletion_protection = false")
	importWith := func(id, pattern string) resource.TestStep {
		return resource.TestStep{
			ResourceName: zoneAddr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return id, nil },
			ExpectError:       regexp.MustCompile(pattern),
		}
	}
	zoneCase(t,
		resource.TestStep{Config: cfg},
		importWith("internal/missing.example.com", `No object matches`),
		importWith("nosuchview/a.example.com", `No object matches`),
		importWith("justaname", `expected a numeric zone id or <view>/<zone>`),
	)
}

func TestZoneBackendErrorsCarryDetailAndRequestID(t *testing.T) {
	b := newZoneBackend(t)
	b.seed("taken.example.com", b.viewID)
	cfg := zoneConfig(b, "name = \"taken.example.com\"\ndeletion_protection = false")
	zoneCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)HTTP 409.*already exists in view.*X-Request-ID`)})
}

func TestZoneIPv6ReverseLifecycle(t *testing.T) {
	b := newZoneBackend(t)
	cfg := zoneConfig(b, `
  zone_type           = "Reverse"
  protocol            = "IPv6"
  cidr                = "2001:DB8::/32"
  deletion_protection = false
`)
	zoneCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(zoneAddr, "name", "8.b.d.0.1.0.0.2.ip6.arpa"),
				resource.TestCheckResourceAttr(zoneAddr, "cidr", "2001:DB8::/32"),
				resource.TestCheckResourceAttr(zoneAddr, "protocol", "IPv6"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: zoneAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}
