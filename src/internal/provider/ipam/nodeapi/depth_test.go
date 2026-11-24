package nodeapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

func seedChain(b *ipamtest.Backend, levels int) {
	for bits := 16; bits < 16+levels; bits++ {
		b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: fmt.Sprintf("2001:db8::/%d", bits), Label: fmt.Sprintf("level-%d", bits)})
	}
}

func TestTreeWalksRefuseToDescendBeyondTheDepthLimit(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	seedChain(b, 90)
	ctx := context.Background()

	var depth *nodeapi.TreeDepthError
	if _, err := nodeapi.ResolveCIDR(ctx, c, "2001:db8::/127", ""); !errors.As(err, &depth) || depth.Limit != 64 {
		t.Errorf("ResolveCIDR error = %v, want a depth error", err)
	}
	if _, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true}); !errors.As(err, &depth) {
		t.Errorf("WalkContainers error = %v, want a depth error", err)
	}
}

func TestTreeWalksWithinTheDepthLimitStillSucceed(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	seedChain(b, 40)
	ctx := context.Background()

	node, err := nodeapi.ResolveCIDR(ctx, c, "2001:db8::/55", "")
	if err != nil || node.Label != "level-55" {
		t.Errorf("ResolveCIDR = %+v, %v", node, err)
	}
	all, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true})
	if err != nil || len(all) != 40 {
		t.Errorf("WalkContainers = %d, %v; want 40", len(all), err)
	}
}

func TestListSubnetsRequestsAStableSortAndAllPages(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	for i := 0; i < 450; i++ {
		b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: fmt.Sprintf("10.%d.%d.0/24", i/250, i%250), Label: fmt.Sprintf("lan-%03d", i)})
	}

	all, err := nodeapi.ListSubnets(context.Background(), c, nodeapi.SubnetQuery{})
	if err != nil || len(all) != 450 {
		t.Fatalf("ListSubnets = %d, %v; want 450", len(all), err)
	}
	var pages, sorted int
	for _, r := range b.Srv.Requests() {
		body := string(r.Body)
		if strings.Contains(body, "IpamSubnets") {
			pages++
		}
		if strings.Contains(body, `"sortBy":"label"`) {
			sorted++
		}
	}
	if pages != 3 || sorted != 3 {
		t.Errorf("subnet page requests = %d (sorted by label: %d); 450 subnets at 200 per page need 3 sorted pages", pages, sorted)
	}
}
