package e2e_test

import (
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
)

func sweepEnv(t *testing.T, srv *fakeserver.Server) {
	t.Setenv("OMNIDDI_ENDPOINT", srv.URL())
	t.Setenv("OMNIDDI_USERNAME", fakeserver.DefaultUsername)
	t.Setenv("OMNIDDI_PASSWORD", fakeserver.DefaultPassword)
	t.Setenv("OMNIDDI_NETWORK_SPACE_ID", "")
}

func TestSweepIPAMDeletesOnlyTestNodesChildrenFirst(t *testing.T) {
	b := ipamtest.New(t)
	outer := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.18.0.0/16", Label: "tfacc-outer"})
	inner := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "198.18.4.0/22", Label: "tfacc-inner"})
	subnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "198.18.4.0/24", Label: "tfacc-inner-subnet"})
	keepContainer := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/8", Label: "production"})
	keepSubnet := b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.1.0.0/24", Label: "prod-tfacc-lookalike"})
	sweepEnv(t, b.Srv)

	if err := sweepIPAM(""); err != nil {
		t.Fatalf("sweepIPAM: %v", err)
	}
	for _, id := range []int64{outer, inner, subnet} {
		if _, ok := b.Get(id); ok {
			t.Errorf("test node %d was not swept", id)
		}
	}
	for _, id := range []int64{keepContainer, keepSubnet} {
		if _, ok := b.Get(id); !ok {
			t.Errorf("non-test node %d was deleted", id)
		}
	}
}

func TestSweepZonesDeletesOnlyTestZones(t *testing.T) {
	srv := fakeserver.New(t)
	var mu sync.Mutex
	zones := map[int64]string{1: "tfacc-abc123.test", 2: "example.com", 3: "TFACC-upper.test", 4: "corp-tfacc-x.test"}
	srv.HandleGraphQL("zonesPaginated", func(map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		items := []any{}
		for id, name := range zones {
			items = append(items, map[string]any{"id": id, "name": name})
		}
		return map[string]any{"items": items, "total": len(items), "hasMore": false}, nil
	})
	srv.HandleREST(http.MethodDelete, "/api/v1/zones/{id}", func(r *fakeserver.Request) fakeserver.Response {
		id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
		mu.Lock()
		delete(zones, id)
		mu.Unlock()
		return fakeserver.JSON(http.StatusOK, map[string]any{"zone_id": id})
	})
	sweepEnv(t, srv)

	if err := sweepZones(""); err != nil {
		t.Fatalf("sweepZones: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(zones) != 2 || zones[2] == "" || zones[4] == "" {
		t.Errorf("remaining zones = %v, want only example.com and corp-tfacc-x.test", zones)
	}
}
