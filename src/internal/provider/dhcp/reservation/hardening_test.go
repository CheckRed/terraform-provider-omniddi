package reservation_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const tagValuesPath = "/api/v1/tags/values"

func TestInPlaceUpdatePlansNoNoise(t *testing.T) {
	b := newBackend(t)
	base := "name = \"quiet\"\nip_address = \"10.20.0.50\"\ntags = { owner = \"netops\" }\n"
	tagsAll := knownvalue.MapExact(map[string]knownvalue.Check{"env": knownvalue.StringExact("prod"), "owner": knownvalue.StringExact("netops")})
	run(t,
		resource.TestStep{Config: hcl(b, base)},
		resource.TestStep{
			Config: hcl(b, base+"description = \"changed\"\n"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), tagsAll),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_id"), knownvalue.Int64Exact(100)),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("subnet_cidr"), knownvalue.StringExact("10.20.0.0/24")),
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("server_group_id"), knownvalue.Int64Exact(7)),
			}},
		},
		resource.TestStep{
			Config: hcl(b, "name = \"quiet\"\nip_address = \"10.20.0.51\"\ntags = { owner = \"netops\" }\ndescription = \"changed\"\n"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectKnownValue(addr, tfjsonpath.New("tags_all"), tagsAll),
				plancheck.ExpectUnknownValue(addr, tfjsonpath.New("subnet_id")),
				plancheck.ExpectUnknownValue(addr, tfjsonpath.New("subnet_cidr")),
			}},
			Check: resource.TestCheckResourceAttr(addr, "subnet_cidr", "10.20.0.0/24"),
		},
	)
}

func TestCreateAdoptsTheObjectAfterAnAmbiguousFailure(t *testing.T) {
	b := newBackend(t)
	b.failAfterCreate = true
	run(t, resource.TestStep{
		Config: hcl(b, "name = \"adopted\"\nip_address = \"10.20.0.77\""),
		Check: func(s *terraform.State) error {
			if b.count() != 1 {
				t.Errorf("reservations = %d, want the one created before the failure", b.count())
			}
			return resource.TestCheckResourceAttr(addr, "name", "adopted")(s)
		},
	})
}

func TestTagFailureAfterCreateTaintsTheResource(t *testing.T) {
	b := newBackend(t)
	b.srv.FailNext(http.MethodPut, tagValuesPath, http.StatusBadRequest, 1)
	run(t, resource.TestStep{
		Config:      hcl(b, "name = \"tainted\"\nip_address = \"10.20.0.78\"\ntags = { owner = \"netops\" }"),
		ExpectError: regexp.MustCompile(`(?s)tags\s+could\s+not\s+be\s+applied.*tainted`),
	})
	if b.count() != 0 {
		t.Errorf("the tainted reservation survived destroy: %d", b.count())
	}
}

func TestFailureAfterUpdateKeepsPriorStateSoTheNextPlanRetries(t *testing.T) {
	b := newBackend(t)
	before := "name = \"retry\"\nip_address = \"10.20.0.79\"\ntags = { owner = \"netops\" }\noptions = [{ code = 6, data = \"10.20.0.2\" }]\n"
	after := "name = \"retry\"\nip_address = \"10.20.0.79\"\ntags = { owner = \"sre\" }\noptions = [{ code = 6, data = \"10.20.0.3\" }]\n"
	run(t,
		resource.TestStep{Config: hcl(b, before)},
		resource.TestStep{
			PreConfig:   func() { b.srv.FailNext(http.MethodPut, tagValuesPath, http.StatusBadRequest, 1) },
			Config:      hcl(b, after),
			ExpectError: regexp.MustCompile(`(?s)tags\s+could\s+not\s+be\s+applied.*previous\s+values`),
		},
		resource.TestStep{
			Config: hcl(b, after), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		resource.TestStep{Config: hcl(b, after)},
	)
}
