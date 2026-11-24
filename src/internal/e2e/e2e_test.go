package e2e_test

import (
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfacctest "github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
)

const namePrefix = "tfacc-"

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"omniddi": providerserver.NewProtocol6WithError(omniprovider.New("e2e")()),
}

func preCheck(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OMNIDDI_ENDPOINT", "OMNIDDI_USERNAME", "OMNIDDI_PASSWORD"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s must be set for end-to-end tests", name)
		}
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func dnsGroupName() string  { return envOr("E2E_DNS_GROUP", "Default DNS Group") }
func dhcpGroupName() string { return envOr("E2E_DHCP_GROUP", "Default DHCP Group") }

func dnsViewName(t *testing.T) string {
	t.Helper()
	view := os.Getenv("E2E_DNS_VIEW")
	if view == "" {
		t.Fatal("E2E_DNS_VIEW must name an existing DNS view for the DNS end-to-end test")
	}
	return view
}

// Waits out the zone's automatic first sync, which retires records created while it runs (B-56).
func zoneSettle() time.Duration {
	d, err := time.ParseDuration(envOr("E2E_ZONE_SETTLE", "30s"))
	if err != nil {
		return 30 * time.Second
	}
	return d
}

func uniqueName() string {
	return namePrefix + strings.ToLower(tfacctest.RandString(6))
}

func octet() int { return rand.IntN(250) + 1 }

func providerBlock() string {
	return `
provider "omniddi" {}
`
}

func emptyPlanAfterApply() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
}

func updatesInPlace(addrs ...string) resource.ConfigPlanChecks {
	checks := make([]plancheck.PlanCheck, 0, len(addrs))
	for _, addr := range addrs {
		checks = append(checks, plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate))
	}
	return resource.ConfigPlanChecks{
		PreApply:             checks,
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

var commonImportIgnore = []string{"deletion_protection", "force_destroy", "approval_mode", "timeouts"}
