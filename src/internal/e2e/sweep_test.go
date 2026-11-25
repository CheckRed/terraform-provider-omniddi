package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const sweepZonesQuery = `query SweepZones($pagination: PaginationInput) { zonesPaginated(pagination: $pagination) { items { id name } total hasMore } }`

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers("omniddi_dns_zone", &resource.Sweeper{Name: "omniddi_dns_zone", F: sweepZones})
	resource.AddTestSweepers("omniddi_ipam", &resource.Sweeper{Name: "omniddi_ipam", F: sweepIPAM})
}

func sweepClient() (*client.Client, error) {
	var space int64
	if raw := os.Getenv("OMNIDDI_NETWORK_SPACE_ID"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("OMNIDDI_NETWORK_SPACE_ID: %w", err)
		}
		space = parsed
	}
	var insecure bool
	if raw := os.Getenv("OMNIDDI_INSECURE"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("OMNIDDI_INSECURE: %w", err)
		}
		insecure = parsed
	}
	return client.New(context.Background(), client.Config{
		Endpoint: os.Getenv("OMNIDDI_ENDPOINT"), Username: os.Getenv("OMNIDDI_USERNAME"), Password: os.Getenv("OMNIDDI_PASSWORD"), NetworkSpaceID: space,
		CACertPEM: os.Getenv("OMNIDDI_CA_CERT_PEM"), Insecure: insecure,
	})
}

func isTestObject(name string) bool { return strings.HasPrefix(strings.ToLower(name), namePrefix) }

func sweepDelete(ctx context.Context, c *client.Client, path string) error {
	if _, err := c.Write(ctx, http.MethodDelete, path, nil); err != nil && !client.IsNotFound(err) {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	return nil
}

func sweepZones(string) error {
	c, err := sweepClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	type zoneRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	zones, err := client.PaginateField[zoneRef](ctx, c, sweepZonesQuery, "zonesPaginated", map[string]any{},
		client.PageOptions{PageSize: 200, SortBy: "id"})
	if err != nil {
		return fmt.Errorf("list zones: %w", err)
	}
	var failures []string
	for _, z := range zones {
		if !isTestObject(z.Name) {
			continue
		}
		if err := sweepDelete(ctx, c, "/zones/"+strconv.FormatInt(z.ID, 10)); err != nil {
			failures = append(failures, err.Error())
		}
	}
	return joinFailures(failures)
}

func sweepIPAM(string) error {
	c, err := sweepClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	subnets, err := nodeapi.ListSubnets(ctx, c, nodeapi.SubnetQuery{Search: namePrefix})
	if err != nil {
		return fmt.Errorf("list subnets: %w", err)
	}
	var failures []string
	for _, n := range subnets {
		if isTestObject(n.Label) {
			if err := sweepDelete(ctx, c, "/ipam/subnets/"+strconv.FormatInt(n.ID, 10)); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	containers, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true, Search: namePrefix})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	sort.SliceStable(containers, func(i, j int) bool { return prefixBits(containers[i]) > prefixBits(containers[j]) })
	for _, n := range containers {
		if isTestObject(n.Label) {
			if err := sweepDelete(ctx, c, "/ipam/containers/"+strconv.FormatInt(n.ID, 10)); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	return joinFailures(failures)
}

func prefixBits(n nodeapi.Node) int {
	if p, ok := n.Prefix(); ok {
		return p.Bits()
	}
	return 0
}

func joinFailures(failures []string) error {
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%d object(s) could not be swept:\n%s", len(failures), strings.Join(failures, "\n"))
}
