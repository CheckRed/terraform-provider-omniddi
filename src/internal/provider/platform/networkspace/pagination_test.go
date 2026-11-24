package networkspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func pageWindow(args map[string]any, total int) (offset, end int) {
	pagination, _ := args["pagination"].(map[string]any)
	limit, _ := pagination["limit"].(float64)
	start, _ := pagination["offset"].(float64)
	offset = min(int(start), total)
	return offset, min(offset+int(limit), total)
}

func TestNetworkSpacesListPagesThroughEverySpaceInStableOrder(t *testing.T) {
	const total = 230
	srv := serving(t)
	srv.HandleGraphQL("networkSpacesPaginated", func(args map[string]any) (any, error) {
		offset, end := pageWindow(args, total)
		items := []map[string]any{}
		for i := offset; i < end; i++ {
			items = append(items, spaceJSON(i+1, space{fmt.Sprintf("space-%03d", i), "ACTIVE"}))
		}
		return map[string]any{"items": items, "total": total, "hasMore": end < total}, nil
	})
	run(t, srv, `data "omniddi_network_spaces" "all" {}`, resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.all", "network_spaces.#", "230"),
		resource.TestCheckResourceAttr("data.omniddi_network_spaces.all", "network_spaces.229.name", "space-229"),
		func(*terraform.State) error {
			var sorted int
			for _, r := range srv.RequestsFor("POST", "/graphql") {
				if strings.Contains(string(r.Body), `"sortBy":"id"`) {
					sorted++
				}
			}
			if sorted != 3 {
				return fmt.Errorf("sorted page requests = %d; 230 spaces at 100 per page need 3", sorted)
			}
			return nil
		},
	), "")
}

func TestNetworkSpacesListFailsInsteadOfTruncating(t *testing.T) {
	srv := serving(t)
	srv.HandleGraphQL("networkSpacesPaginated", func(map[string]any) (any, error) {
		return map[string]any{"items": []map[string]any{spaceJSON(1, space{"endless", "ACTIVE"})}, "total": 1000000, "hasMore": true}, nil
	})
	run(t, srv, `data "omniddi_network_spaces" "all" {}`, nil, `networkSpacesPaginated returned more than 10000 items`)
}
