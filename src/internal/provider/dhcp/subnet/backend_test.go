package subnet_test

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcptest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/subnet"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	ipamsubnet "github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/subnet"
)

const (
	opPromote = "promote"
	opUpdate  = "update"
	opDemote  = "demote"

	groupNoServer  = int64(8)
	groupNoAgent   = int64(9)
	groupNoPrimary = int64(10)
	missingVLAN    = int64(99)
)

type dhcpConfig struct {
	GroupID       int64
	Pools         []any
	Options       []any
	Config        map[string]any
	ClientClasses []int64
	VLANs         []int64
	ViewID        *int64
	SharedNetwork *int64
}

type leaseCounts struct {
	Active  int
	Expired int
}

type backend struct {
	ipam *ipamtest.Backend
	srv  *fakeserver.Server

	mu           sync.Mutex
	configs      map[int64]*dhcpConfig
	reservations map[int64]int
	leases       map[int64]leaseCounts

	approval          func(op string, id int64, body map[string]any) (fakeserver.Response, bool)
	failAfterPromote  bool
	failCounts        bool
	failNextFetch     bool
	failFetchAfterPut bool
	enrichDDNS        bool
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	ipam := ipamtest.New(t)
	b := &backend{
		ipam: ipam, srv: ipam.Srv,
		configs: map[int64]*dhcpConfig{}, reservations: map[int64]int{}, leases: map[int64]leaseCounts{},
	}
	b.srv.HandleREST(http.MethodPut, "/api/v1/ipam/subnets/{id}", b.putIPAMSubnet)
	b.srv.HandleREST(http.MethodDelete, "/api/v1/ipam/networks/{id}/dhcp-config", b.demote)
	b.srv.HandleREST(http.MethodPut, "/api/v1/dhcp/subnets/{id}", b.putSettings)
	b.srv.HandleREST(http.MethodDelete, "/api/v1/dhcp/subnets/{id}", b.hardDelete)
	b.srv.HandleGraphQL("subnetById", b.subnetByID)
	b.srv.HandleGraphQL("hostReservationsPaginated", b.reservationCount)
	b.srv.HandleGraphQL("leasesPaginated", b.leaseCount)
	return b
}

func (b *backend) seedSubnet(cidr string) int64 {
	return b.ipam.Seed(ipamtest.Node{Type: "SUBNET", CIDR: cidr})
}

func (b *backend) seedContainer(cidr string) int64 {
	return b.ipam.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: cidr})
}

func (b *backend) seedDHCP(cidr string, group int64) int64 {
	id := b.seedSubnet(cidr)
	b.enable(id, group)
	return id
}

func (b *backend) enable(id, group int64) {
	node, ok := b.ipam.Get(id)
	if !ok {
		return
	}
	b.mu.Lock()
	b.configs[id] = &dhcpConfig{GroupID: group, Config: map[string]any{}}
	b.mu.Unlock()
	node.SubnetID = &id
	b.ipam.Seed(node)
}

func (b *backend) disable(id int64) {
	b.mu.Lock()
	delete(b.configs, id)
	b.mu.Unlock()
	if node, ok := b.ipam.Get(id); ok {
		node.SubnetID = nil
		b.ipam.Seed(node)
	}
}

func (b *backend) config(id int64) (dhcpConfig, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg, ok := b.configs[id]
	if !ok {
		return dhcpConfig{}, false
	}
	return *cfg, true
}

func (b *backend) setCounts(id int64, reservations int, leases leaseCounts) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reservations[id], b.leases[id] = reservations, leases
}

func pathID(r *fakeserver.Request) int64 {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	return id
}

func requestBodies(srv *fakeserver.Server, method, path string) []map[string]any {
	var out []map[string]any
	for _, r := range srv.RequestsFor(method, path) {
		out = append(out, r.Map())
	}
	return out
}

func badRequest(format string, args ...any) fakeserver.Response {
	return fakeserver.Detail(http.StatusBadRequest, fmt.Sprintf(format, args...))
}

func (b *backend) gated(op string, id int64, body map[string]any) (fakeserver.Response, bool) {
	if b.approval == nil {
		return fakeserver.Response{}, false
	}
	return b.approval(op, id, body)
}

func (b *backend) putIPAMSubnet(r *fakeserver.Request) fakeserver.Response {
	id, body := pathID(r), r.Map()
	node, ok := b.ipam.Get(id)
	if !ok || node.Type != "SUBNET" {
		return badRequest("Subnet node %d not found", id)
	}
	group, promoting := body["server_group_id"].(float64)
	if !promoting {
		return b.renameNode(node, body)
	}
	if resp, pending := b.gated(opPromote, id, body); pending {
		return resp
	}
	b.mu.Lock()
	_, deployed := b.configs[id]
	b.mu.Unlock()
	switch {
	case deployed:
		return badRequest("Subnet is already deployed to DHCP; changing its server group is not supported. Delete and recreate it to move groups.")
	case int64(group) == groupNoServer:
		return fakeserver.Detail(http.StatusNotFound, "DHCP server not found")
	case int64(group) == groupNoPrimary:
		return badRequest("Server group %d has no active primary DHCP server", int64(group))
	case int64(group) == groupNoAgent:
		return fakeserver.Detail(http.StatusConflict, "No agent mapped for DHCP server 4. Map an agent in AgentServerMap to proceed.")
	}
	b.enable(id, int64(group))
	if b.failAfterPromote {
		return fakeserver.Detail(http.StatusBadGateway, "upstream went away after the write")
	}
	b.armFetchFailure()
	parent := int64(0)
	if node.ParentID != nil {
		parent = *node.ParentID
	}
	return fakeserver.JSON(http.StatusOK, map[string]any{
		"id": id, "type": "SUBNET", "parent_id": parent, "label": node.Label, "subnet_id": id, "cidr": node.CIDR,
	})
}

func (b *backend) armFetchFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failNextFetch = b.failFetchAfterPut
}

func (b *backend) renameNode(node ipamtest.Node, body map[string]any) fakeserver.Response {
	if v, ok := body["name"].(string); ok {
		node.Label = v
	}
	if v, ok := body["description"].(string); ok {
		node.Description = &v
	}
	if v, ok := body["owner"].(string); ok {
		node.Owner = &v
	}
	if v, ok := body["vlan"].(string); ok {
		node.VLAN = &v
	}
	b.ipam.Seed(node)
	return fakeserver.JSON(http.StatusOK, map[string]any{
		"id": node.ID, "type": "SUBNET", "parent_id": 0, "label": node.Label, "subnet_id": node.SubnetID, "cidr": node.CIDR,
	})
}

func (b *backend) demote(r *fakeserver.Request) fakeserver.Response {
	id := pathID(r)
	if _, ok := b.ipam.Get(id); !ok {
		return fakeserver.Detail(http.StatusNotFound, "Subnet node not found")
	}
	if resp, pending := b.gated(opDemote, id, nil); pending {
		return resp
	}
	if _, deployed := b.config(id); !deployed {
		return badRequest("Subnet is already planning-only; it does not serve DHCP.")
	}
	b.disable(id)
	return fakeserver.JSON(http.StatusOK, map[string]any{
		"id": id, "dhcp_enabled": false, "server_group_id": nil, "message": "DHCP service removed; subnet kept in IPAM as planning-only.",
	})
}

func enrichOptions(raw any) []any {
	list, _ := raw.([]any)
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		stored := map[string]any{
			"code": entry["code"], "data": entry["data"], "name": fmt.Sprintf("option-%v", entry["code"]),
			"data_type": "string", "space": "dhcp4", "csv_format": true,
		}
		if space, ok := entry["space"].(string); ok {
			stored["space"] = space
		}
		out = append(out, stored)
	}
	return out
}

func enrichPools(raw any) []any {
	list, _ := raw.([]any)
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		stored := map[string]any{"pool": entry["pool"], "option_data": enrichOptions(entry["option_data"])}
		for _, key := range []string{"name", "client_class_id"} {
			if v, ok := entry[key]; ok {
				stored[key] = v
			}
		}
		out = append(out, stored)
	}
	return out
}

func toIDs(raw any) []int64 {
	list, _ := raw.([]any)
	out := make([]int64, 0, len(list))
	for _, item := range list {
		f, _ := item.(float64)
		out = append(out, int64(f))
	}
	return out
}

func (b *backend) putSettings(r *fakeserver.Request) fakeserver.Response {
	id, body := pathID(r), r.Map()
	if resp, pending := b.gated(opUpdate, id, body); pending {
		return resp
	}
	return b.applySettings(id, body)
}

func (b *backend) applySettings(id int64, body map[string]any) fakeserver.Response {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg, ok := b.configs[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Subnet not found")
	}
	if v, ok := body["vlan_id"].(float64); ok && int64(v) == missingVLAN {
		return fakeserver.Detail(http.StatusNotFound, "VLAN not found: 99")
	}
	for _, key := range []string{"lease_time", "lease_time_unit", "enable_dhcp_service", "enable_ddns"} {
		if v, ok := body[key]; ok {
			cfg.Config[key] = v
		}
	}
	if v, ok := body["ddns_config"].(map[string]any); ok {
		stored := map[string]any{}
		for key, value := range v {
			stored[key] = value
		}
		if b.enrichDDNS {
			stored["tsig_key_id"] = 3
		}
		cfg.Config["ddns_config"] = stored
	}
	if v, ok := body["pools"]; ok {
		cfg.Pools = enrichPools(v)
	}
	if v, ok := body["option_data"]; ok {
		cfg.Options = enrichOptions(v)
	}
	if v, ok := body["client_class_ids"]; ok {
		cfg.ClientClasses = toIDs(v)
	}
	if v, ok := body["vlan_id"].(float64); ok {
		cfg.VLANs = []int64{int64(v)}
	}
	if b.failFetchAfterPut {
		b.failNextFetch = true
	}
	return fakeserver.JSON(http.StatusOK, map[string]any{"id": id})
}

func (b *backend) hardDelete(r *fakeserver.Request) fakeserver.Response {
	id := pathID(r)
	b.disable(id)
	b.ipam.Remove(id)
	return fakeserver.NoContent()
}

func idRefs(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"id": id})
	}
	return out
}

func (b *backend) subnetByID(args map[string]any) (any, error) {
	id := int64(argFloat(args["id"]))
	node, nodeOK := b.ipam.Get(id)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failNextFetch {
		b.failNextFetch = false
		return nil, fmt.Errorf("the backend dropped the connection")
	}
	cfg, ok := b.configs[id]
	if !ok || !nodeOK {
		return nil, nil
	}
	pools, options := cfg.Pools, cfg.Options
	if pools == nil {
		pools = []any{}
	}
	if options == nil {
		options = []any{}
	}
	return map[string]any{
		"id": id, "networkAddr": nil, "prefix": 24, "isDhcp6": false, "config": cfg.Config, "serverGroupId": cfg.GroupID,
		"pools": pools, "optionData": options, "viewId": cfg.ViewID, "keaSubnetId": id,
		"createdOn": "2026-02-01T00:00:00", "updatedOn": "2026-02-02T00:00:00", "subnet": node.CIDR,
		"sharedNetworkId": cfg.SharedNetwork, "vlans": idRefs(cfg.VLANs), "clientClasses": idRefs(cfg.ClientClasses),
	}, nil
}

func argFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func totalOf(n int) map[string]any {
	return map[string]any{"items": []any{}, "total": n, "hasMore": false}
}

func (b *backend) reservationCount(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failCounts {
		return nil, fmt.Errorf("permission denied: read_hosts")
	}
	return totalOf(b.reservations[int64(argFloat(args["subnetId"]))]), nil
}

func (b *backend) leaseCount(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	counts := b.leases[int64(argFloat(args["subnetId"]))]
	if len(dhcptest.PaginationFilters(args)) == 0 {
		return totalOf(counts.Active + counts.Expired), nil
	}
	for _, f := range dhcptest.PaginationFilters(args) {
		if f["field"] == "state" && f["operator"] == "EQ" && f["value"] == "ACTIVE" {
			return totalOf(counts.Active), nil
		}
	}
	return totalOf(0), nil
}

func (b *backend) hcl(body string) string {
	return dhcptest.Config(b.srv, fmt.Sprintf("resource \"omniddi_dhcp_subnet\" \"test\" {\n%s\n}", body))
}

func (b *backend) hclFor(id int64, body string) string {
	return b.hcl(fmt.Sprintf("subnet_id = %d\n%s", id, body))
}

func run(t *testing.T, steps ...resource.TestStep) {
	t.Helper()
	resources := append([]func() fwresource.Resource{}, subnet.Resources()...)
	resources = append(resources, ipamsubnet.Resources()...)
	dataSources := ipamsubnet.DataSources()
	dhcptest.Case(t, dhcptest.Factories(resources, dataSources), steps...)
}

func (b *backend) edit(id int64, change func(cfg *dhcpConfig)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cfg, ok := b.configs[id]; ok {
		change(cfg)
	}
}

func approvalDetails(op string, id int64, body map[string]any) (string, map[string]any) {
	switch op {
	case opPromote:
		return "create_subnet", map[string]any{"server_group_id": body["server_group_id"], "ipam_node": map[string]any{"attach_node_id": id}}
	case opUpdate:
		return "update_subnet", map[string]any{"subnet_id": id, "updates": body}
	}
	return "demote_subnet", map[string]any{"subnet_id": id, "server_id": nil}
}

func (b *backend) approveImmediately() func(string, int64, map[string]any) (fakeserver.Response, bool) {
	return func(op string, id int64, body map[string]any) (fakeserver.Response, bool) {
		action, details := approvalDetails(op, id, body)
		request := b.srv.StageApproval(action, details, fakeserver.RequestPending, fakeserver.RequestApproved, fakeserver.RequestApplied)
		switch op {
		case opPromote:
			group, _ := body["server_group_id"].(float64)
			b.enable(id, int64(group))
		case opUpdate:
			b.applySettings(id, body)
		case opDemote:
			b.disable(id)
		}
		return b.srv.ApprovalPending(request, "Request"), true
	}
}

func (b *backend) holdForApproval(only string, staged *atomic.Int64) func(string, int64, map[string]any) (fakeserver.Response, bool) {
	return func(op string, id int64, body map[string]any) (fakeserver.Response, bool) {
		if op != only {
			return fakeserver.Response{}, false
		}
		action, details := approvalDetails(op, id, body)
		request := b.srv.StageApproval(action, details, fakeserver.RequestPending)
		staged.Store(request)
		return b.srv.ApprovalPending(request, "Request"), true
	}
}
