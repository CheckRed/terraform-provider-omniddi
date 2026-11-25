package ipamtest

import (
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
)

const AllocatePath = "/api/v1/ipam/next-free-ip/allocate"

type Address struct {
	ID        int64
	NetworkID int64
	IP        string
	Hostname  string
	Status    string
}

type addressBook struct {
	rows          map[int64]*Address
	busyNext      int
	loseResponses int
	corruptNext   int
	allocations   int
}

var allocateFields = map[string]bool{
	"subnet_id": true, "subnet_cidr": true, "name": true, "exclude": true, "create_dns_records": true, "network_space": true,
}

func coded(status int, code, detail string) fakeserver.Response {
	return fakeserver.JSON(status, map[string]any{"detail": detail, "code": code})
}

func (b *Backend) registerAddresses() {
	b.addrs = addressBook{rows: map[int64]*Address{}}
	b.Srv.HandleREST(http.MethodPost, AllocatePath, b.allocate)
	b.Srv.HandleGraphQL("ipAddress", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		a, ok := b.addrs.rows[argInt(args["id"])]
		if !ok {
			return nil, nil
		}
		return addressJSON(a), nil
	})
	b.Srv.HandleGraphQL("ipAddresses", b.addressesPage)
}

func (b *Backend) SeedAddress(a Address) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a.ID == 0 {
		a.ID = b.Srv.NextID()
	}
	if a.Status == "" {
		a.Status = "Used"
	}
	stored := a
	b.addrs.rows[a.ID] = &stored
	return a.ID
}

func (b *Backend) Address(id int64) (Address, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.addrs.rows[id]
	if !ok {
		return Address{}, false
	}
	return *a, true
}

func (b *Backend) Addresses() []Address {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Address, 0, len(b.addrs.rows))
	for _, a := range b.addrs.rows {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Backend) RemoveAddress(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.addrs.rows, id)
}

func (b *Backend) SetAddress(id int64, update func(*Address)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.addrs.rows[id]; ok {
		update(a)
	}
}

func (b *Backend) BusyNext(times int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addrs.busyNext = times
}

func (b *Backend) LoseNextAllocateResponse(times int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addrs.loseResponses = times
}

func (b *Backend) CorruptNextAllocateResponse(times int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addrs.corruptNext = times
}

func (b *Backend) Allocations() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.addrs.allocations
}

func (b *Backend) allocate(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	b.mu.Lock()
	defer b.mu.Unlock()
	for key := range body {
		if !allocateFields[key] {
			return coded(http.StatusBadRequest, "INVALID_PARAMETER", key+": Extra inputs are not permitted.")
		}
	}
	if b.addrs.busyNext > 0 {
		b.addrs.busyNext--
		resp := coded(http.StatusServiceUnavailable, "ALLOCATION_BUSY", "Other allocations held the subnet for more than 10 s. Nothing was allocated; retry after 2 s.")
		resp.Header = http.Header{"Retry-After": []string{"2"}}
		return resp
	}
	name := strings.TrimSuffix(derefStr(str(body, "name")), ".")
	if name == "" {
		return coded(http.StatusBadRequest, "MISSING_PARAMETER", "name is required.")
	}
	node, ok := b.nodes[derefInt(intField(body, "subnet_id"))]
	if !ok || node.Type != "SUBNET" {
		return coded(http.StatusNotFound, "SUBNET_NOT_FOUND", fmt.Sprintf("Subnet %v was not found.", body["subnet_id"]))
	}
	prefix := netip.MustParsePrefix(node.CIDR)
	ip, found := b.nextFree(node.ID, prefix, excluded(body))
	if !found {
		return coded(http.StatusConflict, "NO_FREE_IP", fmt.Sprintf("No eligible address is free in %s %s. Nothing was allocated.", node.Label, node.CIDR))
	}
	row := &Address{ID: b.Srv.NextID(), NetworkID: node.ID, IP: ip.String(), Hostname: name, Status: "Used"}
	b.addrs.rows[row.ID] = row
	b.addrs.allocations++
	if b.addrs.loseResponses > 0 {
		b.addrs.loseResponses--
		return fakeserver.Detail(http.StatusBadGateway, "upstream connection reset")
	}
	if b.addrs.corruptNext > 0 {
		b.addrs.corruptNext--
		return fakeserver.JSON(http.StatusCreated, map[string]any{"status": "Used"})
	}
	return fakeserver.JSON(http.StatusCreated, map[string]any{
		"network_space_id": 1, "subnet": map[string]any{"id": node.ID, "cidr": node.CIDR},
		"address": row.IP, "record_id": row.ID, "status": row.Status, "name": row.Hostname, "dns_records": []any{},
	})
}

func excluded(body map[string]any) map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	list, _ := body["exclude"].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			if a, err := netip.ParseAddr(s); err == nil {
				out[a] = true
			}
		}
	}
	return out
}

func (b *Backend) nextFree(networkID int64, prefix netip.Prefix, skip map[netip.Addr]bool) (netip.Addr, bool) {
	used := map[netip.Addr]bool{}
	for _, a := range b.addrs.rows {
		if a.NetworkID == networkID {
			used[netip.MustParseAddr(a.IP)] = true
		}
	}
	first, last := hostRange(prefix)
	for cur := first; cur.IsValid() && prefix.Contains(cur); cur = cur.Next() {
		if !used[cur] && !skip[cur] {
			return cur, true
		}
		if cur == last {
			break
		}
	}
	return netip.Addr{}, false
}

func hostRange(prefix netip.Prefix) (netip.Addr, netip.Addr) {
	first := prefix.Masked().Addr()
	hostBits := first.BitLen() - prefix.Bits()
	lastInt := new(big.Int).Add(new(big.Int).SetBytes(first.AsSlice()), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(hostBits)), big.NewInt(1)))
	raw := make([]byte, len(first.AsSlice()))
	lastInt.FillBytes(raw)
	last, _ := netip.AddrFromSlice(raw)
	switch {
	case first.Is4() && prefix.Bits() <= 30:
		return first.Next(), last.Prev()
	case first.Is6() && prefix.Bits() <= 126:
		return first.Next(), last
	}
	return first, last
}

func addressJSON(a *Address) map[string]any {
	return map[string]any{"id": a.ID, "ip": a.IP, "status": a.Status, "hostname": a.Hostname, "subnetId": a.NetworkID}
}

func (b *Backend) addressesPage(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	network := argInt(args["networkId"])
	search := strings.ToLower(fmt.Sprint(args["search"]))
	if args["search"] == nil {
		search = ""
	}
	var matched []*Address
	for _, a := range b.addrs.rows {
		if network != 0 && a.NetworkID != network {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(a.IP+"/"+a.Hostname), search) {
			continue
		}
		matched = append(matched, a)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	after := int64(0)
	if cursor, ok := args["after"].(string); ok {
		_, _ = fmt.Sscan(cursor, &after)
	}
	first := int(argInt(args["first"]))
	edges := []any{}
	var last int64
	more := false
	for _, a := range matched {
		if a.ID <= after {
			continue
		}
		if len(edges) == first {
			more = true
			break
		}
		edges = append(edges, map[string]any{"node": addressJSON(a), "cursor": fmt.Sprint(a.ID)})
		last = a.ID
	}
	var end any
	if last != 0 {
		end = fmt.Sprint(last)
	}
	return map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": end}, "totalCount": len(matched)}, nil
}
