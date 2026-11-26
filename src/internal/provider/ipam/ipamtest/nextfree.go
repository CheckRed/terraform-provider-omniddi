package ipamtest

import (
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"slices"
	"strconv"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
)

const (
	NextFreeSubnetPath = "/api/v1/ipam/next-free-subnet"
	NextFreeIPPath     = "/api/v1/ipam/next-free-ip"
)

func (b *Backend) registerNextFreeSubnet() {
	b.Srv.HandleREST(http.MethodGet, NextFreeSubnetPath, b.nextFreeSubnet)
	b.Srv.HandleREST(http.MethodGet, NextFreeIPPath, b.nextFreeIP)
}

func (b *Backend) nodeByTarget(r *fakeserver.Request, idKey, cidrKey string) (*Node, bool) {
	if raw := r.Query.Get(idKey); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		n, ok := b.nodes[id]
		return n, err == nil && ok
	}
	for _, n := range b.nodes {
		if r.Query.Get(cidrKey) != "" && n.CIDR == r.Query.Get(cidrKey) {
			return n, true
		}
	}
	return nil, false
}

func (b *Backend) nextFreeIP(r *fakeserver.Request) fakeserver.Response {
	b.mu.Lock()
	defer b.mu.Unlock()
	subnet, ok := b.nodeByTarget(r, "subnet_id", "subnet_cidr")
	if !ok {
		return coded(http.StatusNotFound, "SUBNET_NOT_FOUND", "Subnet was not found.")
	}
	if subnet.Type != "SUBNET" {
		return coded(http.StatusBadRequest, "NOT_A_SUBNET", fmt.Sprintf("Node %d is a %s, not a subnet.", subnet.ID, subnet.Type))
	}
	count := 1
	if raw := r.Query.Get("count"); raw != "" {
		count, _ = strconv.Atoi(raw)
	}
	skip := map[netip.Addr]bool{}
	for _, raw := range r.Query["exclude"] {
		a, err := netip.ParseAddr(raw)
		if err != nil {
			return coded(http.StatusBadRequest, "INVALID_PARAMETER", "exclude: "+err.Error())
		}
		skip[a] = true
	}
	prefix := netip.MustParsePrefix(subnet.CIDR)
	var found []any
	for len(found) < count {
		ip, ok := b.nextFree(subnet.ID, prefix, skip)
		if !ok {
			break
		}
		skip[ip] = true
		found = append(found, ip.String())
	}
	switch {
	case len(found) == 0:
		return coded(http.StatusConflict, "NO_FREE_IP", fmt.Sprintf("No eligible address is free in %s %s.", subnet.Label, subnet.CIDR))
	case len(found) < count:
		return coded(http.StatusConflict, "INSUFFICIENT_FREE_IPS", fmt.Sprintf("Only %d of %d addresses are free in %s.", len(found), count, subnet.CIDR))
	}
	return fakeserver.JSON(http.StatusOK, map[string]any{
		"network_space_id": 1, "subnet": map[string]any{"id": subnet.ID, "cidr": subnet.CIDR}, "addresses": found,
	})
}

func (b *Backend) nextFreeSubnet(r *fakeserver.Request) fakeserver.Response {
	b.mu.Lock()
	defer b.mu.Unlock()
	container, ok := b.nodeByTarget(r, "container_id", "container_cidr")
	if !ok {
		return coded(http.StatusNotFound, "CONTAINER_NOT_FOUND", "Container was not found.")
	}
	if container.Type != "CONTAINER" {
		return coded(http.StatusBadRequest, "NOT_A_CONTAINER", fmt.Sprintf("Node %d is a %s, not a container.", container.ID, container.Type))
	}
	outer := netip.MustParsePrefix(container.CIDR)
	bits, err := strconv.Atoi(r.Query.Get("prefix_length"))
	if err != nil || bits <= outer.Bits() || bits > outer.Addr().BitLen() {
		return coded(http.StatusBadRequest, "INVALID_PREFIX", fmt.Sprintf("prefix_length must be longer than the container's /%d.", outer.Bits()))
	}
	taken := b.occupiedIn(container.ID, outer)
	for _, raw := range r.Query["exclude"] {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return coded(http.StatusBadRequest, "INVALID_PARAMETER", "exclude: "+err.Error())
		}
		taken = append(taken, p.Masked())
	}
	block, found := lowestFreeBlock(outer, bits, taken)
	if !found {
		return coded(http.StatusConflict, "NO_FREE_SUBNET", fmt.Sprintf("No free /%d is left in container %s %s.", bits, container.Label, container.CIDR))
	}
	return fakeserver.JSON(http.StatusOK, map[string]any{
		"network_space_id": 1, "container": map[string]any{"id": container.ID, "cidr": container.CIDR}, "cidr": block.String(),
	})
}

func (b *Backend) occupiedIn(containerID int64, outer netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, n := range b.nodes {
		p, err := netip.ParsePrefix(n.CIDR)
		if err == nil && n.ID != containerID && outer.Bits() <= p.Bits() && outer.Overlaps(p) {
			out = append(out, p)
		}
	}
	return out
}

func lowestFreeBlock(outer netip.Prefix, bits int, taken []netip.Prefix) (netip.Prefix, bool) {
	step := new(big.Int).Lsh(big.NewInt(1), uint(outer.Addr().BitLen()-bits))
	cur := new(big.Int).SetBytes(outer.Masked().Addr().AsSlice())
	for {
		raw := make([]byte, outer.Addr().BitLen()/8)
		if cur.BitLen() > len(raw)*8 {
			return netip.Prefix{}, false
		}
		cur.FillBytes(raw)
		addr, _ := netip.AddrFromSlice(raw)
		if !outer.Contains(addr) {
			return netip.Prefix{}, false
		}
		candidate := netip.PrefixFrom(addr, bits)
		if !overlapsAny(candidate, taken) {
			return candidate, true
		}
		cur.Add(cur, step)
	}
}

func overlapsAny(p netip.Prefix, taken []netip.Prefix) bool {
	return slices.ContainsFunc(taken, p.Overlaps)
}
