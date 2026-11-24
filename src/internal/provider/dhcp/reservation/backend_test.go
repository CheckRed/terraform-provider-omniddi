package reservation_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
)

type subnet struct {
	ID            int64
	CIDR          string
	ServerGroupID int64
}

type entry struct {
	ID             int64
	Name           string
	SubnetID       int64
	IdentifierType *string
	Identifier     *string
	IP             string
	Description    *string
	Options        any
}

type backend struct {
	srv             *fakeserver.Server
	mu              sync.Mutex
	subnets         []subnet
	items           map[int64]*entry
	approval        func(method string, body map[string]any) (fakeserver.Response, bool)
	failAfterCreate bool
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	b := &backend{
		srv:     dhcptest.NewServer(t),
		subnets: []subnet{{ID: 100, CIDR: "10.20.0.0/24", ServerGroupID: 7}, {ID: 101, CIDR: "10.30.0.0/24", ServerGroupID: 7}, {ID: 102, CIDR: "2001:db8:20::/64", ServerGroupID: 7}},
		items:   map[int64]*entry{},
	}
	b.srv.HandleREST(http.MethodPost, "/api/v1/dhcp/reservations", b.create)
	b.srv.HandleREST(http.MethodPut, "/api/v1/dhcp/reservations/{id}", b.update)
	b.srv.HandleREST(http.MethodDelete, "/api/v1/dhcp/reservations/{id}", b.remove)
	b.srv.HandleGraphQL("hostReservationById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["id"].(float64)
		item, ok := b.items[int64(id)]
		if !ok {
			return nil, nil
		}
		return b.row(item), nil
	})
	b.srv.HandleGraphQL("hostReservationsPaginated", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		wantSubnet, hasSubnet := args["subnetId"].(float64)
		var rows []map[string]any
		for _, item := range b.items {
			row := b.row(item)
			if hasSubnet && item.SubnetID != int64(wantSubnet) {
				continue
			}
			if dhcptest.MatchesFilters(args, row) {
				rows = append(rows, row)
			}
		}
		return dhcptest.Page(rows), nil
	})
	return b
}

func (b *backend) subnetByID(id int64) *subnet {
	for i := range b.subnets {
		if b.subnets[i].ID == id {
			return &b.subnets[i]
		}
	}
	return nil
}

func (b *backend) subnetFor(ip string) *subnet {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	for i := range b.subnets {
		if netip.MustParsePrefix(b.subnets[i].CIDR).Contains(addr) {
			return &b.subnets[i]
		}
	}
	return nil
}

func (b *backend) row(item *entry) map[string]any {
	sn := b.subnetByID(item.SubnetID)
	var idType any
	if item.IdentifierType != nil {
		idType = map[string]string{"Mac Address": "MAC_ADDRESS", "DUID": "DUID"}[*item.IdentifierType]
	}
	return map[string]any{
		"id": item.ID, "name": item.Name, "subnet_id": item.SubnetID, "server_group_id": sn.ServerGroupID,
		"identifier_type": idType, "identifier": item.Identifier, "ip_address": item.IP,
		"is_dhcp6": netip.MustParsePrefix(sn.CIDR).Addr().Is6(), "subnet": sn.CIDR, "description": item.Description,
	}
}

func optString(body map[string]any, key string) *string {
	if v, ok := body[key].(string); ok {
		return &v
	}
	return nil
}

func normalizeMAC(v *string) *string {
	if v == nil {
		return nil
	}
	out := strings.ReplaceAll(strings.ToLower(*v), "-", ":")
	return &out
}

func (b *backend) nameTaken(name string, except int64) bool {
	for _, item := range b.items {
		if item.Name == name && item.ID != except {
			return true
		}
	}
	return false
}

// validateCreate mirrors the backend, which rejects these before any approval request is made.
func (b *backend) validateCreate(body map[string]any) (*subnet, *fakeserver.Response) {
	reject := func(status int, detail string) (*subnet, *fakeserver.Response) {
		resp := fakeserver.Detail(status, detail)
		return nil, &resp
	}
	ip, _ := body["ip_address"].(string)
	sn := b.subnetFor(ip)
	if id, ok := body["subnet_id"].(float64); ok {
		sn = b.subnetByID(int64(id))
		if sn == nil {
			return reject(http.StatusNotFound, "Subnet not found")
		}
		if !netip.MustParsePrefix(sn.CIDR).Contains(netip.MustParseAddr(ip)) {
			return reject(http.StatusBadRequest, "Invalid IP for provided subnet")
		}
	}
	if sn == nil {
		return reject(http.StatusBadRequest, "Unable to resolve subnet for the provided IP")
	}
	name, _ := body["name"].(string)
	identifier := normalizeMAC(optString(body, "identifier"))
	for _, item := range b.items {
		switch {
		case item.Name == name:
			return reject(http.StatusBadRequest, "Host reservation with this name already exists")
		case item.SubnetID == sn.ID && netip.MustParseAddr(item.IP) == netip.MustParseAddr(ip):
			return reject(http.StatusBadRequest, "Host reservation with this IP already exists")
		case identifier != nil && item.Identifier != nil && item.SubnetID == sn.ID && *item.Identifier == *identifier:
			return reject(http.StatusBadRequest, "Host reservation with this Identifier already exists")
		}
	}
	return sn, nil
}

func (b *backend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	b.mu.Lock()
	_, rejection := b.validateCreate(body)
	b.mu.Unlock()
	if rejection != nil {
		return *rejection
	}
	if b.approval != nil {
		if resp, pending := b.approval(http.MethodPost, body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sn, rejection := b.validateCreate(body)
	if rejection != nil {
		return *rejection
	}
	ip, _ := body["ip_address"].(string)
	name, _ := body["name"].(string)
	item := &entry{
		ID: b.srv.NextID(), Name: name, SubnetID: sn.ID, IP: ip, Description: optString(body, "description"),
		IdentifierType: optString(body, "identifier_type"), Identifier: normalizeMAC(optString(body, "identifier")), Options: body["options"],
	}
	b.items[item.ID] = item
	if b.failAfterCreate {
		return fakeserver.Detail(http.StatusBadGateway, "upstream went away after the write")
	}
	return fakeserver.JSON(http.StatusCreated, map[string]any{"id": item.ID, "name": item.Name, "ip_address": item.IP})
}

func (b *backend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	body := r.Map()
	if b.approval != nil {
		if resp, pending := b.approval(http.MethodPut, body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.items[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Host reservation not found")
	}
	if _, has := body["subnet_id"]; has {
		return fakeserver.Detail(http.StatusInternalServerError, "subnet_id must not be sent on update")
	}
	if name, _ := body["name"].(string); name != "" {
		if b.nameTaken(name, id) {
			return fakeserver.Detail(http.StatusBadRequest, "Host reservation with this name already exists")
		}
		item.Name = name
	}
	if ip, _ := body["ip_address"].(string); ip != "" {
		item.IP = ip
	}
	item.Description = optString(body, "description")
	item.IdentifierType = optString(body, "identifier_type")
	item.Identifier = normalizeMAC(optString(body, "identifier"))
	item.Options = body["dhcp_options"]
	return fakeserver.JSON(http.StatusOK, map[string]any{"id": id})
}

func (b *backend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	if b.approval != nil {
		if resp, pending := b.approval(http.MethodDelete, nil); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.items[id]; !ok {
		return fakeserver.Detail(http.StatusNotFound, "Host reservation not found")
	}
	delete(b.items, id)
	return fakeserver.JSON(http.StatusOK, map[string]any{"detail": "Host reservation deleted"})
}

func (b *backend) seed(name, ip string) *entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	item := &entry{ID: b.srv.NextID(), Name: name, IP: ip, SubnetID: b.subnetFor(ip).ID}
	b.items[item.ID] = item
	return item
}

func (b *backend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

func (b *backend) get(id int64) *entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.items[id]
}

func hcl(b *backend, body string) string {
	return dhcptest.Config(b.srv, fmt.Sprintf("resource \"omniddi_dhcp_host_reservation\" \"test\" {\n%s\n}", body))
}
