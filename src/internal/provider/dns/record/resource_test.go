package record_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const recordAddr = "omniddi_dns_record.test"

func recordConfig(b *recordBackend, body string) string {
	return acctest.ProviderConfigWith(b.srv.URL(), `  default_tags = { "env" = "prod" }
`) + fmt.Sprintf("\nresource \"omniddi_dns_record\" \"test\" {\n  zone_id = %d\n%s\n}\n", testZoneID, body)
}

func recordConfigWithoutResource(b *recordBackend) string {
	return acctest.ProviderConfig(b.srv.URL())
}

var importIgnore = []string{"timeouts", "approval_mode", "record_set_ordering"}

func TestRecordAValuesLifecycleTagsAndImport(t *testing.T) {
	b := newRecordBackend(t)
	create := recordConfig(b, `
  name   = "WWW"
  type   = "A"
  values = ["192.0.2.10", "192.0.2.11", "192.0.2.12"]
  disabled_values = ["192.0.2.11"]
  tags      = { owner = "netops" }
  tags_list = { teams = ["a", "b"] }
`)
	update := recordConfig(b, `
  name   = "www"
  type   = "A"
  ttl    = 600
  values = ["192.0.2.10", "192.0.2.20"]
  tags   = { env = "dev" }
`)

	recordCase(t,
		resource.TestStep{
			Config: create,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("name"), knownvalue.StringExact("WWW")),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("disabled_values"), knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("192.0.2.11")})),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("ttl"), knownvalue.Int64Exact(testZoneTTL)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("effective_ttl"), knownvalue.Int64Exact(testZoneTTL)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("zone_name"), knownvalue.StringExact(testZoneName)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("view_id"), knownvalue.Int64Exact(testViewID)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("is_auto_created"), knownvalue.Bool(false)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
				statecheck.ExpectIdentityValueMatchesState(recordAddr, tfjsonpath.New("id")),
			},
			Check: func(*terraform.State) error {
				rec := b.find("www", "A")
				if rec == nil {
					return fmt.Errorf("record not stored with the lower-cased name")
				}
				got := b.srv.TagValues(client.TagObjectDNSRecord, rec.ID)
				if fmt.Sprint(got["owner"]) != "[netops]" || fmt.Sprint(got["teams"]) != "[a b]" || fmt.Sprint(got["env"]) != "[prod]" {
					return fmt.Errorf("backend tags = %v", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{
			Config: update,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionUpdate),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(recordAddr, "ttl", "600"),
				resource.TestCheckResourceAttr(recordAddr, "effective_ttl", "600"),
				resource.TestCheckResourceAttr(recordAddr, "values.#", "2"),
				resource.TestCheckNoResourceAttr(recordAddr, "disabled_values.#"),
				resource.TestCheckResourceAttr(recordAddr, "tags_all.env", "dev"),
				func(*terraform.State) error {
					body := b.lastRequestBody()
					if _, sent := body["name"]; sent {
						return fmt.Errorf("an unchanged name must not be sent: %v", body)
					}
					flags, _ := body["enabled_flags"].([]any)
					if len(flags) != 2 || flags[0] != true || flags[1] != true {
						return fmt.Errorf("stale disabled flags must be reset explicitly, got %v", body["enabled_flags"])
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: update, PlanOnly: true},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, Config: update,
		},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) {
				return "internal/example.com/www/a", nil
			},
			ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore,
		},
	)
	if b.count() != 0 {
		t.Errorf("record was not deleted at the end of the test")
	}
}

func TestRecordApexNameAndSingleValueType(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name   = "@"
  type   = "TXT"
  values = ["v=spf1 -all"]
`)
	recordCase(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(recordAddr, "name", "@")},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordCNAMENoDiffForCaseAndTrailingDot(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name   = "alias"
  type   = "CNAME"
  values = ["Target.Example.com."]
`)
	recordCase(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(recordAddr, "values.0", "Target.Example.com.")},
		resource.TestStep{
			PreConfig: func() {
				rec := b.find("alias", "CNAME")
				b.mu.Lock()
				b.records[rec.ID].Values = map[string]any{"values": []any{"target.example.com"}, "value_meta": []any{map[string]any{"enabled": true}}}
				b.mu.Unlock()
			},
			Config: cfg, PlanOnly: true,
		},
	)
	if got := b.lastRequestBody()["data"]; got != "Target.Example.com." {
		t.Errorf("single-valued types must send a scalar, got %#v", got)
	}
}

func TestRecordAAAANoDiffForEquivalentAddress(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name   = "v6"
  type   = "AAAA"
  values = ["2001:db8::0:1"]
`)
	recordCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				rec := b.find("v6", "AAAA")
				b.mu.Lock()
				b.records[rec.ID].Values["values"] = []any{"2001:db8::1"}
				b.mu.Unlock()
			},
			Config: cfg, PlanOnly: true,
		},
	)
}

func TestRecordMXBlocks(t *testing.T) {
	b := newRecordBackend(t)
	create := recordConfig(b, `
  name = "@"
  type = "MX"
  mx {
    preference = 10
    exchange   = "Mail1.example.com."
  }
  mx {
    preference = 20
    exchange   = "mail2.example.com"
  }
`)
	update := recordConfig(b, `
  name = "@"
  type = "MX"
  mx {
    preference = 5
    exchange   = "mail1.example.com"
  }
`)
	recordCase(t,
		resource.TestStep{
			Config: create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(recordAddr, "mx.#", "2"),
				resource.TestCheckResourceAttr(recordAddr, "mx.0.preference", "10"),
				resource.TestCheckResourceAttr(recordAddr, "mx.0.exchange", "Mail1.example.com."),
				resource.TestCheckNoResourceAttr(recordAddr, "values.#"),
			),
		},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{Config: update, Check: resource.TestCheckResourceAttr(recordAddr, "mx.#", "1")},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}

func TestRecordSRVBlocks(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name = "_sip._tcp"
  type = "SRV"
  srv {
    priority = 10
    weight   = 60
    port     = 5060
    host     = "sip1.example.com"
  }
  srv {
    priority = 10
    weight   = 40
    port     = 5060
    host     = "sip2.example.com"
  }
`)
	recordCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(recordAddr, "srv.#", "2"),
				resource.TestCheckResourceAttr(recordAddr, "srv.1.host", "sip2.example.com"),
				resource.TestCheckResourceAttr(recordAddr, "srv.0.port", "5060"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}

func TestRecordCAAListWithIssueAndIodef(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name = "@"
  type = "CAA"
  caa {
    flag  = 0
    tag   = "issue"
    value = "letsencrypt.org"
  }
  caa {
    flag  = 0
    tag   = "iodef"
    value = "mailto:security@example.com"
  }
`)
	recordCase(t,
		resource.TestStep{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(recordAddr, "caa.#", "2"),
				resource.TestCheckResourceAttr(recordAddr, "caa.1.tag", "iodef"),
			),
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}

func TestRecordDataJSONForSVCBAndNAPTRAndTLSA(t *testing.T) {
	cases := map[string]struct {
		name, recordType, json string
	}{
		"svcb":  {"svc", "SVCB", `{ priority = 1, host = "svc.example.com", alpn = "h2", port = 8443 }`},
		"naptr": {"enum", "NAPTR", `[{ order = 100, preference = 10, flags = "S", services = "SIP+D2U", regexp = "", replacement = "_sip._udp.example.com" }]`},
		"tlsa":  {"_443._tcp", "TLSA", `[{ usage = 3, selector = 1, matching = 1, data = "abcdef0123" }]`},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			b := newRecordBackend(t)
			cfg := recordConfig(b, fmt.Sprintf(`
  name      = %q
  type      = %q
  data_json = jsonencode(%s)
`, tc.name, tc.recordType, tc.json))
			recordCase(t,
				resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttrSet(recordAddr, "data_json")},
				resource.TestStep{Config: cfg, PlanOnly: true},
				resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
			)
		})
	}
}

func TestRecordDataJSONIgnoresKeyOrderAndWhitespace(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name      = "svc"
  type      = "SVCB"
  data_json = "{ \"port\": 8443, \"host\": \"svc.example.com\",\n \"priority\": 1 }"
`)
	recordCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: append([]string{"data_json"}, importIgnore...)},
	)
}

func TestRecordMXThroughDataJSONRoundTrips(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, `
  name      = "@"
  type      = "MX"
  data_json = jsonencode([{ preference = 10, exchange = "mail.example.com" }])
`)
	recordCase(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(recordAddr, "mx.#", "0")},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordTypeChangeReplaces(t *testing.T) {
	b := newRecordBackend(t)
	first := recordConfig(b, `
  name   = "host"
  type   = "A"
  values = ["192.0.2.10"]
`)
	second := recordConfig(b, `
  name   = "host"
  type   = "AAAA"
  values = ["2001:db8::10"]
`)
	recordCase(t,
		resource.TestStep{Config: first},
		resource.TestStep{
			Config: second,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionReplace),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(recordAddr, "type", "AAAA"),
				func(*terraform.State) error {
					if b.find("host", "A") != nil || b.find("host", "AAAA") == nil {
						return fmt.Errorf("the A record must be replaced by the AAAA record")
					}
					return nil
				},
			),
		},
	)
}

func TestRecordNameChangeUpdatesInPlace(t *testing.T) {
	b := newRecordBackend(t)
	first := recordConfig(b, "name = \"old\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	second := recordConfig(b, "name = \"new\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{Config: first},
		resource.TestStep{
			Config: second,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionUpdate),
			}},
		},
	)
}

func TestRecordReadRemovesDeletedObject(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, "name = \"gone\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			PreConfig: func() {
				b.mu.Lock()
				defer b.mu.Unlock()
				b.records = map[int64]*fakeRecord{}
			},
			Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	)
}

func TestRecordImportUnknownNaturalKey(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, "name = \"www\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "internal/example.com/missing/A", nil },
			ExpectError:       regexp.MustCompile(`No object matches`),
		},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, Config: cfg,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "not/a/key", nil },
			ExpectError:       regexp.MustCompile(`view/zone/name/type`),
		},
	)
}

func TestRecordImportApexByNaturalKey(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, "name = \"@\"\ntype = \"TXT\"\nvalues = [\"hello\"]")
	recordCase(t,
		resource.TestStep{Config: cfg},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "internal/example.com/@/TXT", nil },
		},
	)
}

func TestRecordDisabledValuesToggleAndReset(t *testing.T) {
	b := newRecordBackend(t)
	enabled := recordConfig(b, "name = \"lb\"\ntype = \"A\"\nvalues = [\"192.0.2.1\", \"192.0.2.2\"]")
	disabled := recordConfig(b, "name = \"lb\"\ntype = \"A\"\nvalues = [\"192.0.2.1\", \"192.0.2.2\"]\ndisabled_values = [\"192.0.2.2\"]")
	changed := recordConfig(b, "name = \"lb\"\ntype = \"A\"\nvalues = [\"192.0.2.1\", \"192.0.2.3\"]")
	recordCase(t,
		resource.TestStep{Config: enabled},
		resource.TestStep{Config: disabled, Check: resource.TestCheckResourceAttr(recordAddr, "disabled_values.#", "1")},
		resource.TestStep{Config: disabled, PlanOnly: true},
		resource.TestStep{Config: changed, Check: resource.TestCheckNoResourceAttr(recordAddr, "disabled_values.#")},
		resource.TestStep{Config: changed, PlanOnly: true},
	)
}

func TestRecordInvalidConfigs(t *testing.T) {
	b := newRecordBackend(t)
	cases := map[string]struct {
		body  string
		match string
	}{
		"two forms":            {"name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.1\"]\ndata_json = jsonencode([1])", `Conflicting value forms`},
		"mx values":            {"name = \"x\"\ntype = \"MX\"\nvalues = [\"mail.example.com\"]", `(?s)Value form does not match`},
		"a with mx block":      {"name = \"x\"\ntype = \"A\"\nmx {\n preference = 1\n exchange = \"m.example.com\"\n}", `(?s)Value form does not match`},
		"naptr values":         {"name = \"x\"\ntype = \"NAPTR\"\nvalues = [\"1 2 3\"]", `(?s)Value form does not match`},
		"no value":             {"name = \"x\"\ntype = \"A\"", `Missing record value`},
		"cname two values":     {"name = \"x\"\ntype = \"CNAME\"\nvalues = [\"a.example.com\", \"b.example.com\"]", `exactly one value`},
		"disabled not in vals": {"name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.1\", \"192.0.2.2\"]\ndisabled_values = [\"192.0.2.9\"]", `not one of values`},
		"all disabled":         {"name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.1\"]\ndisabled_values = [\"192.0.2.1\"]", `At least one value must remain enabled`},
		"disabled wrong type":  {"name = \"x\"\ntype = \"CNAME\"\nvalues = [\"a.example.com\"]\ndisabled_values = [\"a.example.com\"]", `only supported for A, AAAA`},
		"unknown type":         {"name = \"x\"\ntype = \"BOGUS\"\nvalues = [\"a\"]", `Attribute type value must be one of`},
		"bad json":             {"name = \"x\"\ntype = \"SVCB\"\ndata_json = \"{not json\"", `Invalid JSON`},
		"caa bad flag":         {"name = \"x\"\ntype = \"CAA\"\ncaa {\n flag = 1\n tag = \"issue\"\n value = \"a\"\n}", `must be one of`},
		"bad ordering":         {"name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.1\"]\nrecord_set_ordering = \"sideways\"", `must be one of`},
		"ttl zero":             {"name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.1\"]\nttl = 0", `must be between`},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			recordCase(t, resource.TestStep{Config: recordConfig(b, tc.body), PlanOnly: true, ExpectError: regexp.MustCompile(tc.match)})
		})
	}
}

func TestRecordBackendErrorsCarryDetail(t *testing.T) {
	b := newRecordBackend(t)
	b.seed(fakeRecord{Name: "dup", Type: "A", Values: map[string]any{"values": []any{"192.0.2.1"}}})
	cfg := recordConfig(b, "name = \"dup\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)HTTP 400.*already exists in the zone`)})
}

func TestRecordFailedTaskKeepsStateAndIsReplaced(t *testing.T) {
	b := newRecordBackend(t)
	b.taskFinal, b.logs = []string{"Inprogress", "Incomplete"}, "named-checkzone failed: bad rdata"
	cfg := recordConfig(b, "name = \"bad\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)agent task \d+ failed.*named-checkzone failed: bad rdata`)},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{"Completed"}, "" },
			Config:    cfg,
			Check: func(*terraform.State) error {
				if got := len(b.srv.RequestsFor("DELETE", "/api/v1/records/1")); got != 1 {
					return fmt.Errorf("the tainted record must be saved and replaced (DELETE count %d)", got)
				}
				return nil
			},
		},
	)
}

func TestRecordMissingCorrelationIDIsRefusedButStateSaved(t *testing.T) {
	b := newRecordBackend(t)
	b.srv.HandleREST("POST", "/api/v1/records/", func(*fakeserver.Request) fakeserver.Response {
		return fakeserver.JSON(http.StatusAccepted, map[string]any{"zone_id": testZoneID, "record_id": 41, "task_ids": []int64{}})
	})
	b.seed(fakeRecord{Name: "x", Type: "A", Values: map[string]any{"values": []any{"192.0.2.10"}}})
	cfg := recordConfig(b, "name = \"x\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t, resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`(?s)cannot be tracked.*saved to the Terraform state`)})
}

func TestRecordOwnerNameTrailingDotAndCase(t *testing.T) {
	b := newRecordBackend(t)
	upper := recordConfig(b, "name = \"WWW\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	absolute := recordConfig(b, "name = \"www.\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{Config: upper, Check: resource.TestCheckResourceAttr(recordAddr, "name", "WWW")},
		resource.TestStep{Config: upper, PlanOnly: true},
		resource.TestStep{
			Config: absolute,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionUpdate),
			}},
			Check: func(*terraform.State) error {
				if got := b.lastRequestBody()["name"]; got != "www." {
					return fmt.Errorf("the absolute name must be sent with its dot, got %v", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: absolute, PlanOnly: true},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "internal/example.com/www./A", nil },
		},
		resource.TestStep{
			ResourceName: recordAddr, ImportState: true, Config: absolute,
			ImportStateIdFunc: func(*terraform.State) (string, error) { return "internal/example.com/www/A", nil },
			ExpectError:       regexp.MustCompile(`No object matches`),
		},
	)
}

func TestRecordInPlaceUpdateKeepsTagsAllKnown(t *testing.T) {
	b := newRecordBackend(t)
	first := recordConfig(b, "name = \"www\"\ntype = \"A\"\nttl = 300\nvalues = [\"192.0.2.10\"]\ntags = { owner = \"netops\" }")
	second := recordConfig(b, "name = \"www\"\ntype = \"A\"\nttl = 600\nvalues = [\"192.0.2.10\"]\ntags = { owner = \"netops\" }")
	recordCase(t,
		resource.TestStep{Config: first},
		resource.TestStep{
			Config: second,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionUpdate),
				plancheck.ExpectKnownValue(recordAddr, tfjsonpath.New("tags_all"), knownvalue.MapExact(map[string]knownvalue.Check{
					"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops"),
				})),
			}},
		},
	)
}

func TestRecordCreateRecoversWhenTheResponseIsLost(t *testing.T) {
	b := newRecordBackend(t)
	b.srv.HandleREST(http.MethodPost, "/api/v1/records/", func(r *fakeserver.Request) fakeserver.Response {
		b.create(r)
		return fakeserver.Detail(http.StatusInternalServerError, "gateway lost the response")
	})
	cfg := recordConfig(b, "name = \"www\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	recordCase(t,
		resource.TestStep{
			Config: cfg,
			Check: func(*terraform.State) error {
				if got := b.count(); got != 1 {
					return fmt.Errorf("records on the backend = %d, want the single adopted one", got)
				}
				return nil
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordUpdateFailureRestoresPriorState(t *testing.T) {
	b := newRecordBackend(t)
	first := recordConfig(b, "name = \"www\"\ntype = \"A\"\nvalues = [\"192.0.2.10\"]")
	second := recordConfig(b, "name = \"www\"\ntype = \"A\"\nvalues = [\"192.0.2.20\"]")
	recordCase(t,
		resource.TestStep{Config: first},
		resource.TestStep{
			PreConfig:   func() { b.taskFinal, b.logs = []string{"Inprogress", "Incomplete"}, "named-checkzone failed" },
			Config:      second,
			ExpectError: regexp.MustCompile(`(?s)agent task \d+ failed.*previous values`),
		},
		resource.TestStep{
			PreConfig: func() { b.taskFinal, b.logs = []string{"Completed"}, "" },
			Config:    second,
			Check:     resource.TestCheckResourceAttr(recordAddr, "values.0", "192.0.2.20"),
		},
	)
}

func TestRecordInactiveIsReactivatedInsteadOfRecreated(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, "name = \"www\"\ntype = \"A\"\nttl = 120\nvalues = [\"192.0.2.10\", \"192.0.2.11\"]")
	var firstID int64
	recordCase(t,
		resource.TestStep{
			Config: cfg,
			Check: func(*terraform.State) error {
				firstID = b.find("www", "A").ID
				return nil
			},
		},
		resource.TestStep{
			PreConfig:          func() { b.deactivate("www", "A") },
			Config:             cfg,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionCreate)},
			},
			Check: func(*terraform.State) error {
				rec := b.find("www", "A")
				switch {
				case b.count() != 1:
					return fmt.Errorf("expected one record, got %d", b.count())
				case rec.Inactive:
					return fmt.Errorf("record %d is still inactive", rec.ID)
				case rec.ID != firstID:
					return fmt.Errorf("expected record %d to be reactivated, got %d", firstID, rec.ID)
				}
				return nil
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("id"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("ttl"), knownvalue.Int64Exact(120)),
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordInactiveSeededRecordIsAdoptedWithConfiguredValues(t *testing.T) {
	b := newRecordBackend(t)
	id := b.seed(fakeRecord{Name: "mail", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}, Inactive: true})
	cfg := recordConfig(b, "name = \"mail\"\ntype = \"A\"\nttl = 300\nvalues = [\"192.0.2.20\"]")
	recordCase(t,
		resource.TestStep{
			Config: cfg,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("id"), knownvalue.Int64Exact(id)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("values"), knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("192.0.2.20")})),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("ttl"), knownvalue.Int64Exact(300)),
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordReactivatedButNotUpdatedIsKeptInState(t *testing.T) {
	b := newRecordBackend(t)
	id := b.seed(fakeRecord{Name: "mail", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}, Inactive: true})
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if _, edit := body["ttl"]; edit && body["zone_id"] == nil {
			return fakeserver.Detail(http.StatusBadRequest, "Invalid TTL"), true
		}
		return fakeserver.Response{}, false
	}
	cfg := recordConfig(b, "name = \"mail\"\ntype = \"A\"\nttl = 300\nvalues = [\"192.0.2.20\"]")
	recordCase(t,
		resource.TestStep{
			Config:      cfg,
			ExpectError: regexp.MustCompile(`reactivated but this configuration was not applied`),
		},
		resource.TestStep{
			PreConfig: func() {
				if rec := b.find("mail", "A"); rec == nil || rec.ID != id || rec.Inactive {
					t.Errorf("record %d must be active after the reactivation, got %+v", id, rec)
				}
				b.approval = nil
			},
			Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("ttl"), knownvalue.Int64Exact(300)),
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func (b *recordBackend) reactivateNow(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records[id].Inactive = false
}

func isBareReactivation(body map[string]any) bool {
	return len(body) == 1 && body["is_active"] == true
}

func TestRecordReactivationThatIsNotConfirmedIsKeptInState(t *testing.T) {
	b := newRecordBackend(t)
	id := b.seed(fakeRecord{Name: "mail", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}, Inactive: true})
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if !isBareReactivation(body) {
			return fakeserver.Response{}, false
		}
		b.reactivateNow(id)
		return b.srv.AcceptedWithTasks(map[string]any{"record_id": id}, fakeserver.StatusIncomplete), true
	}
	cfg := recordConfig(b, "name = \"mail\"\ntype = \"A\"\nttl = 300\nvalues = [\"192.0.2.20\"]")
	recordCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`DNS record was reactivated but this configuration was not applied`)},
		resource.TestStep{
			PreConfig: func() { b.approval = nil },
			Config:    cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(recordAddr, plancheck.ResourceActionDestroyBeforeCreate),
			}},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordReactivationApprovedLaterIsAdoptedOnTheNextApply(t *testing.T) {
	b := newRecordBackend(t)
	id := b.seed(fakeRecord{Name: "mail", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}, Inactive: true})
	b.approval = func(body map[string]any) (fakeserver.Response, bool) {
		if isBareReactivation(body) {
			return b.srv.ApprovalPending(321, "Record update request"), true
		}
		return fakeserver.Response{}, false
	}
	cfg := recordConfig(b, "name = \"mail\"\ntype = \"A\"\nttl = 300\nvalues = [\"192.0.2.20\"]")
	recordCase(t,
		resource.TestStep{Config: cfg, ExpectError: regexp.MustCompile(`Approval required \(request 321\)`)},
		resource.TestStep{
			PreConfig: func() {
				b.approval = nil
				applied := b.srv.StageApproval("update_record", map[string]any{"record_id": float64(id), "is_active": true}, fakeserver.RequestApplied)
				b.srv.SetRequestAppliedOn(applied, time.Now().Add(-time.Minute))
				b.reactivateNow(id)
			},
			Config: cfg,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("id"), knownvalue.Int64Exact(id)),
				statecheck.ExpectKnownValue(recordAddr, tfjsonpath.New("ttl"), knownvalue.Int64Exact(300)),
			},
		},
		resource.TestStep{Config: cfg, PlanOnly: true},
	)
}

func TestRecordActiveDuplicateWithoutAnApprovedReactivationIsRefused(t *testing.T) {
	b := newRecordBackend(t)
	b.seed(fakeRecord{Name: "mail", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}})
	recordCase(t, resource.TestStep{
		Config:      recordConfig(b, "name = \"mail\"\ntype = \"A\"\nvalues = [\"192.0.2.20\"]"),
		ExpectError: regexp.MustCompile(`already exists`),
	})
	if rec := b.find("mail", "A"); rec.TTL != 3600 {
		t.Errorf("the existing record must not be changed, got TTL %d", rec.TTL)
	}
}

func TestRecordCNAMEBlockedByAnInactiveRecordIsNotReactivated(t *testing.T) {
	b := newRecordBackend(t)
	b.seed(fakeRecord{Name: "www", Type: "A", TTL: 3600, Values: map[string]any{"values": []any{"192.0.2.99"}}, Inactive: true})
	recordCase(t, resource.TestStep{
		Config:      recordConfig(b, "name = \"www\"\ntype = \"CNAME\"\nvalues = [\"app.example.com.\"]"),
		ExpectError: regexp.MustCompile(`(?s)Unable to create DNS record.*CNAME records cannot coexist`),
	})
	if rec := b.find("www", "A"); rec == nil || !rec.Inactive {
		t.Errorf("the blocking A record must stay inactive, got %+v", rec)
	}
}

func mxConfig(b *recordBackend, firstPreference int) string {
	return recordConfig(b, fmt.Sprintf(`
  name = "@"
  type = "MX"
  mx {
    preference = %d
    exchange   = "mail1.example.com."
  }
  mx {
    preference = 20
    exchange   = "mail2.example.com."
  }
`, firstPreference))
}

func (b *recordBackend) disableValue(name, recordType string, index int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rec := range b.records {
		if rec.Name == name && rec.Type == recordType && rec.Configs != nil {
			meta, _ := rec.Configs["data_meta"].([]any)
			meta[index] = map[string]any{"enabled": false}
		}
	}
}

func (b *recordBackend) valueEnabled(name, recordType string, index int) bool {
	rec := b.find(name, recordType)
	meta, _ := rec.Configs["data_meta"].([]any)
	entry, _ := meta[index].(map[string]any)
	enabled, _ := entry["enabled"].(bool)
	return enabled
}

func TestRecordMXUpdateKeepsValuesDisabledInOmniDDI(t *testing.T) {
	b := newRecordBackend(t)
	recordCase(t,
		resource.TestStep{Config: mxConfig(b, 10)},
		resource.TestStep{
			PreConfig: func() { b.disableValue("@", "MX", 1) },
			Config:    mxConfig(b, 5),
			Check: func(*terraform.State) error {
				if _, sent := b.lastRequestBody()["enabled_flags"]; sent {
					return fmt.Errorf("update must not send enabled_flags for MX, sent %v", b.lastRequestBody())
				}
				if b.valueEnabled("@", "MX", 1) {
					return fmt.Errorf("the MX value disabled in OmniDDI was re-enabled")
				}
				return nil
			},
		},
	)
}

func TestRecordNSDelegationLifecycle(t *testing.T) {
	b := newRecordBackend(t)
	create := recordConfig(b, "name = \"lab\"\ntype = \"NS\"\nvalues = [\"ns1.lab.example.com.\", \"ns2.lab.example.com.\"]")
	update := recordConfig(b, "name = \"lab\"\ntype = \"NS\"\nvalues = [\"ns1.lab.example.com.\"]")
	recordCase(t,
		resource.TestStep{Config: create, Check: resource.TestCheckResourceAttr(recordAddr, "values.#", "2")},
		resource.TestStep{Config: create, PlanOnly: true},
		resource.TestStep{Config: update, Check: resource.TestCheckResourceAttr(recordAddr, "values.#", "1")},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}

func TestRecordPTRLifecycle(t *testing.T) {
	b := newRecordBackend(t)
	cfg := recordConfig(b, "name = \"10\"\ntype = \"PTR\"\nvalues = [\"www.example.com.\"]")
	recordCase(t,
		resource.TestStep{Config: cfg, Check: resource.TestCheckResourceAttr(recordAddr, "values.0", "www.example.com.")},
		resource.TestStep{Config: cfg, PlanOnly: true},
		resource.TestStep{ResourceName: recordAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: importIgnore},
	)
}
