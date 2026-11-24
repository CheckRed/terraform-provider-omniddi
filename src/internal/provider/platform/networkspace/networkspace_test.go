package networkspace_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/platform/networkspace"
)

type testProvider struct{ *omniprovider.OmniDDIProvider }

func (testProvider) DataSources(context.Context) []func() datasource.DataSource {
	return networkspace.DataSources()
}

func fastClient(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func factories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastClient)().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = testProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

type space struct {
	name, state string
}

func spaceJSON(id int, s space) map[string]any {
	return map[string]any{"id": id, "name": s.name, "description": nil, "isDefault": id == 1, "state": s.state, "available": s.state == "ACTIVE", "serverCount": 3}
}

func serving(t *testing.T, spaces ...space) *fakeserver.Server {
	srv := fakeserver.New(t)
	srv.HandleGraphQL("networkSpacesPaginated", func(map[string]any) (any, error) {
		items := []map[string]any{}
		for i, s := range spaces {
			items = append(items, spaceJSON(i+1, s))
		}
		return map[string]any{"items": items, "total": len(items), "hasMore": false}, nil
	})
	srv.HandleGraphQL("networkSpaceById", func(args map[string]any) (any, error) {
		id, _ := args["id"].(float64)
		if int(id) < 1 || int(id) > len(spaces) {
			return nil, nil
		}
		return spaceJSON(int(id), spaces[int(id)-1]), nil
	})
	return srv
}

func run(t *testing.T, srv *fakeserver.Server, body string, check resource.TestCheckFunc, expect string) {
	t.Helper()
	t.Parallel()
	step := resource.TestStep{Config: acctest.ProviderConfig(srv.URL()) + body, Check: check}
	if expect != "" {
		step.ExpectError = regexp.MustCompile(expect)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)},
		Steps:                    []resource.TestStep{step},
	})
}

func TestQueriesMatchSchema(t *testing.T) {
	omniddigql.RequireValid(t,
		`query NetworkSpaceByID($id: Int!) { networkSpaceById(id: $id) { id name description isDefault state available serverCount } }`,
		`query NetworkSpaces($pagination: PaginationInput) { networkSpacesPaginated(pagination: $pagination) { items { id name description isDefault state available serverCount } total hasMore } }`,
	)
}

func TestNetworkSpaceByNameAndID(t *testing.T) {
	srv := serving(t, space{"default", "ACTIVE"}, space{"lab", "INACTIVE"})
	run(t, srv, `
data "omniddi_network_space" "by_name" { name = "lab" }
data "omniddi_network_space" "by_id" { id = 1 }
`, resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_name", "id", "2"),
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_name", "state", "INACTIVE"),
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_name", "available", "false"),
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_id", "name", "default"),
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_id", "is_default", "true"),
		resource.TestCheckResourceAttr("data.omniddi_network_space.by_id", "server_count", "3"),
	), "")
}

func TestNetworkSpaceNotFound(t *testing.T) {
	srv := serving(t, space{"default", "ACTIVE"})
	t.Run("by name", func(t *testing.T) {
		run(t, srv, `data "omniddi_network_space" "x" { name = "ghost" }`, nil, `Network space not found`)
	})
	t.Run("by id", func(t *testing.T) {
		run(t, srv, `data "omniddi_network_space" "x" { id = 99 }`, nil, `No network space has id 99`)
	})
}

func TestNetworkSpaceAmbiguousName(t *testing.T) {
	run(t, serving(t, space{"dup", "ACTIVE"}, space{"dup", "ACTIVE"}), `data "omniddi_network_space" "x" { name = "dup" }`, nil, `Network space name is ambiguous`)
}

func TestNetworkSpaceNeedsExactlyOneSelector(t *testing.T) {
	run(t, serving(t), `data "omniddi_network_space" "x" {}`, nil, `(?s)exactly one|Missing Attribute`)
}

func TestNetworkSpacesList(t *testing.T) {
	srv := serving(t, space{"default", "ACTIVE"}, space{"lab", "INACTIVE"}, space{"lab2", "ACTIVE"})
	run(t, srv, `
data "omniddi_network_spaces" "all" {}
data "omniddi_network_spaces" "active" { state = "ACTIVE" }
data "omniddi_network_spaces" "named" { name = "lab" }
data "omniddi_network_spaces" "none" { name = "zzz" }
`, resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.all", "network_spaces.#", "3"),
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.active", "network_spaces.#", "2"),
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.named", "network_spaces.#", "1"),
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.none", "network_spaces.#", "0"),
	), "")
}
