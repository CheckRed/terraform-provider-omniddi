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

const NextFreeSubnetPath = "/api/v1/ipam/next-free-subnet"

func (b *Backend) registerNextFreeSubnet() {
	b.Srv.HandleREST(http.MethodGet, NextFreeSubnetPath, b.nextFreeSubnet)
}

func (b *Backend) nextFreeSubnet(r *fakeserver.Request) fakeserver.Response {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, err := strconv.ParseInt(r.Query.Get("container_id"), 10, 64)
	if err != nil {
		return coded(http.StatusBadRequest, "MISSING_PARAMETER", "container_id is required.")
	}
	container, ok := b.nodes[id]
	if !ok {
		return coded(http.StatusNotFound, "CONTAINER_NOT_FOUND", fmt.Sprintf("Container %d was not found.", id))
	}
	if container.Type != "CONTAINER" {
		return coded(http.StatusBadRequest, "NOT_A_CONTAINER", fmt.Sprintf("Node %d is a %s, not a container.", id, container.Type))
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
