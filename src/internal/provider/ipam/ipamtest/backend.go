package ipamtest

import (
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	FamilyV4ID = int64(1)
	FamilyV6ID = int64(2)
)

type Node struct {
	ID          int64
	Type        string
	ParentID    *int64
	Label       string
	CIDR        string
	Protocol    string
	BU          *int64
	VLAN        *string
	Owner       *string
	Description *string
	SubnetID    *int64
}

type Backend struct {
	Srv *fakeserver.Server

	mu          sync.Mutex
	nodes       map[int64]*Node
	SubnetBody  func(body map[string]any) (fakeserver.Response, bool)
	DefaultBU   int64
	lastCreates []map[string]any
}

func New(t testing.TB) *Backend {
	t.Helper()
	srv := fakeserver.New(t)
	b := &Backend{Srv: srv, nodes: map[int64]*Node{}, DefaultBU: 7}
	b.seed(FamilyV4ID, "IPv4")
	b.seed(FamilyV6ID, "IPv6")
	srv.NextID()
	srv.NextID()
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})

	srv.HandleREST(http.MethodPost, "/api/v1/ipam/containers", b.createContainer)
	srv.HandleREST(http.MethodPut, "/api/v1/ipam/containers/{id}", b.updateNode("CONTAINER"))
	srv.HandleREST(http.MethodDelete, "/api/v1/ipam/containers/{id}", b.deleteContainer)
	srv.HandleREST(http.MethodPost, "/api/v1/ipam/subnets", b.createSubnet)
	srv.HandleREST(http.MethodPut, "/api/v1/ipam/subnets/{id}", b.updateNode("SUBNET"))
	srv.HandleREST(http.MethodDelete, "/api/v1/ipam/subnets/{id}", b.deleteSubnet)

	srv.HandleGraphQL("ipSpaceNodeById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		n, ok := b.nodes[argInt(args["id"])]
		if !ok {
			return nil, nil
		}
		return b.nodeJSON(n), nil
	})
	srv.HandleGraphQL("ipSpaceChildren", b.children)
	srv.HandleGraphQL("ipSpaceAncestors", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		out := []any{}
		n, ok := b.nodes[argInt(args["nodeId"])]
		for ok && n.ParentID != nil {
			n = b.nodes[*n.ParentID]
			out = append(out, map[string]any{"id": n.ID})
			ok = n != nil
		}
		return out, nil
	})
	srv.HandleGraphQL("managedSubnetsPaginated", b.subnetsPage)
	srv.HandleGraphQL("subnetById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		n, ok := b.nodes[argInt(args["id"])]
		if !ok || n.SubnetID == nil {
			return nil, nil
		}
		return map[string]any{"id": n.ID}, nil
	})
	return b
}

func (b *Backend) seed(id int64, protocol string) {
	b.nodes[id] = &Node{ID: id, Type: "FAMILY", Label: protocol, Protocol: protocol}
}

func argInt(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func (b *Backend) Get(id int64) (Node, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

func (b *Backend) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.nodes) - 2
}

func (b *Backend) Remove(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.nodes, id)
}

func (b *Backend) Seed(n Node) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n.ID == 0 {
		n.ID = b.Srv.NextID()
	}
	if prefix, err := netip.ParsePrefix(n.CIDR); err == nil && n.Protocol == "" {
		n.Protocol = protocolOf(prefix)
	}
	if n.Label == "" {
		n.Label = n.CIDR
	}
	stored := n
	b.nodes[n.ID] = &stored
	b.remap()
	return n.ID
}

func (b *Backend) LastCreate() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lastCreates) == 0 {
		return nil
	}
	return b.lastCreates[len(b.lastCreates)-1]
}

func protocolOf(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

func bad(format string, args ...any) fakeserver.Response {
	return fakeserver.Detail(http.StatusBadRequest, fmt.Sprintf(format, args...))
}

func str(body map[string]any, key string) *string {
	if v, ok := body[key].(string); ok {
		return &v
	}
	return nil
}

func intField(body map[string]any, key string) *int64 {
	if v, ok := body[key].(float64); ok {
		n := int64(v)
		return &n
	}
	return nil
}

func (b *Backend) familyRoot(protocol string) int64 {
	if protocol == "IPv6" {
		return FamilyV6ID
	}
	return FamilyV4ID
}

func (b *Backend) remap() {
	for _, n := range b.nodes {
		prefix, err := netip.ParsePrefix(n.CIDR)
		if err != nil {
			continue
		}
		var best *Node
		bestBits := -1
		for _, c := range b.nodes {
			cp, err := netip.ParsePrefix(c.CIDR)
			if c.Type != "CONTAINER" || err != nil || c.ID == n.ID || cp.Addr().Is4() != prefix.Addr().Is4() {
				continue
			}
			if cp.Bits() < prefix.Bits() && cp.Overlaps(prefix) && cp.Bits() > bestBits {
				best, bestBits = c, cp.Bits()
			}
		}
		parent := b.familyRoot(protocolOf(prefix))
		if best != nil {
			parent = best.ID
		}
		n.ParentID = &parent
	}
}

func (b *Backend) dupCIDR(cidr string) bool {
	for _, n := range b.nodes {
		if n.CIDR == cidr {
			return true
		}
	}
	return false
}

func (b *Backend) response(status int, n *Node, extra map[string]any) fakeserver.Response {
	body := map[string]any{"id": n.ID, "type": n.Type, "parent_id": 0, "label": n.Label}
	if n.ParentID != nil {
		body["parent_id"] = *n.ParentID
	}
	for k, v := range extra {
		body[k] = v
	}
	return fakeserver.JSON(status, body)
}

func (b *Backend) createContainer(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastCreates = append(b.lastCreates, body)
	parent, ok := b.nodes[derefInt(intField(body, "parent_id"))]
	if !ok {
		return bad("Parent node %v not found", body["parent_id"])
	}
	if parent.Type != "FAMILY" && parent.Type != "CONTAINER" {
		return bad("Container parent must be FAMILY or CONTAINER, got %s", parent.Type)
	}
	prefix, err := netip.ParsePrefix(derefStr(str(body, "cidr")))
	if err != nil {
		return bad("Invalid CIDR %v: %v", body["cidr"], err)
	}
	if prefix.Bits() == prefix.Addr().BitLen() {
		return bad("A container cannot be a single host address (/32 IPv4, /128 IPv6); create it as an address under a subnet instead")
	}
	prefix = prefix.Masked()
	if pp, err := netip.ParsePrefix(parent.CIDR); err == nil && !(pp.Bits() <= prefix.Bits() && pp.Overlaps(prefix)) {
		return bad("Container %s is not inside parent %s", prefix, pp)
	}
	if b.dupCIDR(prefix.String()) {
		return bad("CIDR %s already exists", prefix)
	}
	bu := b.DefaultBU
	if v := intField(body, "business_unit_id"); v != nil {
		bu = *v
	}
	n := &Node{ID: b.Srv.NextID(), Type: "CONTAINER", Label: derefStr(str(body, "name")), CIDR: prefix.String(), Protocol: protocolOf(prefix),
		BU: &bu, VLAN: str(body, "vlan"), Owner: str(body, "owner"), Description: str(body, "description")}
	b.nodes[n.ID] = n
	b.remap()
	return b.response(http.StatusCreated, n, nil)
}

func (b *Backend) createSubnet(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	if b.SubnetBody != nil {
		if resp, handled := b.SubnetBody(body); handled {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastCreates = append(b.lastCreates, body)
	if _, sent := body["server_group_id"]; sent {
		return bad("server_group_id must not be sent")
	}
	addr, err := netip.ParseAddr(derefStr(str(body, "network_addr")))
	if err != nil {
		return bad("Invalid CIDR %v/%v", body["network_addr"], body["prefix"])
	}
	prefix := netip.PrefixFrom(addr, int(derefInt(intField(body, "prefix")))).Masked()
	declared := derefStr(str(body, "protocol"))
	if declared != "" && declared != protocolOf(prefix) {
		return bad("protocol %s does not match CIDR %s", declared, prefix)
	}
	parentID := b.familyRoot(protocolOf(prefix))
	if v := intField(body, "parent_id"); v != nil {
		parentID = *v
	}
	parent, ok := b.nodes[parentID]
	if !ok {
		return bad("Parent node %d not found", parentID)
	}
	if parent.Type != "FAMILY" && parent.Type != "CONTAINER" {
		return bad("Subnet parent must be FAMILY or CONTAINER, got %s", parent.Type)
	}
	if pp, err := netip.ParsePrefix(parent.CIDR); err == nil && !(pp.Bits() <= prefix.Bits() && pp.Overlaps(prefix)) {
		return bad("Subnet %s is not inside parent %s", prefix, pp)
	}
	for _, n := range b.nodes {
		if np, err := netip.ParsePrefix(n.CIDR); err == nil && n.Type == "SUBNET" && np.Overlaps(prefix) {
			return bad("Subnet %s overlaps sibling %s", prefix, np)
		}
	}
	if b.dupCIDR(prefix.String()) {
		return bad("CIDR %s already exists", prefix)
	}
	label := prefix.String()
	if v := str(body, "name"); v != nil {
		label = *v
	}
	bu := b.DefaultBU
	if v := intField(body, "business_unit_id"); v != nil {
		bu = *v
	}
	n := &Node{ID: b.Srv.NextID(), Type: "SUBNET", Label: label, CIDR: prefix.String(), Protocol: protocolOf(prefix),
		BU: &bu, VLAN: str(body, "vlan"), Owner: str(body, "owner"), Description: str(body, "description")}
	b.nodes[n.ID] = n
	b.remap()
	return b.response(http.StatusCreated, n, map[string]any{"subnet_id": nil, "cidr": n.CIDR})
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}

func (b *Backend) updateNode(kind string) fakeserver.RESTHandler {
	return func(r *fakeserver.Request) fakeserver.Response {
		id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
		body := r.Map()
		b.mu.Lock()
		defer b.mu.Unlock()
		n, ok := b.nodes[id]
		if !ok || n.Type != kind {
			return bad("Node %d is not a %s", id, kind)
		}
		if v := str(body, "name"); v != nil {
			if strings.TrimSpace(*v) == "" {
				return bad("name cannot be empty")
			}
			n.Label = *v
		}
		if v := str(body, "description"); v != nil {
			n.Description = v
		}
		if v := str(body, "vlan"); v != nil {
			n.VLAN = v
		}
		if v := str(body, "owner"); v != nil {
			n.Owner = v
		}
		extra := map[string]any{}
		if kind == "SUBNET" {
			extra["subnet_id"], extra["cidr"] = n.SubnetID, n.CIDR
		}
		return b.response(http.StatusOK, n, extra)
	}
}

func (b *Backend) hasChildren(id int64) bool {
	for _, n := range b.nodes {
		if n.ParentID != nil && *n.ParentID == id {
			return true
		}
	}
	return false
}

func (b *Backend) deleteContainer(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	reparent := r.Query.Get("reparent_children") == "true"
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[id]
	if !ok || n.Type != "CONTAINER" {
		return bad("Container node %d not found", id)
	}
	if b.hasChildren(id) && !reparent {
		return bad("This container still holds networks. Move or delete them first, or choose to move its contents to the parent container when deleting.")
	}
	for _, c := range b.nodes {
		if c.ParentID != nil && *c.ParentID == id {
			c.ParentID = n.ParentID
		}
	}
	delete(b.nodes, id)
	return fakeserver.JSON(http.StatusOK, map[string]any{"id": id, "deleted": true, "message": "Container deleted"})
}

func (b *Backend) deleteSubnet(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if n, ok := b.nodes[id]; !ok || n.Type != "SUBNET" {
		return bad("Subnet node %d not found", id)
	}
	delete(b.nodes, id)
	return fakeserver.JSON(http.StatusOK, map[string]any{"id": id, "deleted": true, "message": "Subnet deleted"})
}

func (b *Backend) nodeJSON(n *Node) map[string]any {
	out := map[string]any{
		"id": n.ID, "type": n.Type, "parentId": n.ParentID, "label": n.Label, "subnetId": gqlSubnetID(n),
		"cidr": nilIfEmpty(n.CIDR), "prefixLength": nil, "protocol": nilIfEmpty(n.Protocol), "businessUnitId": n.BU,
		"vlan": n.VLAN, "owner": n.Owner, "description": n.Description, "status": "ACTIVE",
		"createdOn": "2026-01-01T00:00:00", "updatedOn": "2026-01-02T00:00:00", "hasChildren": b.hasChildren(n.ID),
		"tags": b.tagsJSON(n),
	}
	if prefix, err := netip.ParsePrefix(n.CIDR); err == nil {
		out["prefixLength"] = prefix.Bits()
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (b *Backend) tagsJSON(n *Node) map[string]any {
	objectType := client.TagObjectNetworkContain
	if n.Type == "SUBNET" {
		objectType = client.TagObjectSubnet
	}
	out := map[string]any{}
	for name, values := range b.Srv.TagValues(objectType, n.ID) {
		if len(values) == 1 && name != "teams" {
			out[name] = values[0]
			continue
		}
		list := make([]any, 0, len(values))
		for _, v := range values {
			list = append(list, v)
		}
		out[name] = list
	}
	return out
}

func (b *Backend) children(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var parent *int64
	if v, ok := args["parentId"].(float64); ok {
		id := int64(v)
		parent = &id
	}
	search, _ := args["search"].(string)
	out := []any{}
	for _, n := range b.sorted() {
		if (parent == nil) != (n.ParentID == nil) || (parent != nil && *n.ParentID != *parent) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(n.Label+n.CIDR), strings.ToLower(search)) {
			continue
		}
		out = append(out, b.nodeJSON(n))
	}
	return out, nil
}

func (b *Backend) sorted() []*Node {
	list := make([]*Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

func (b *Backend) inScope(n *Node, scope int64) bool {
	for cur := n; cur != nil; {
		if cur.ID == scope {
			return true
		}
		if cur.ParentID == nil {
			return false
		}
		cur = b.nodes[*cur.ParentID]
	}
	return false
}

func (b *Backend) subnetsPage(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pagination, _ := args["pagination"].(map[string]any)
	scope, hasScope := scopeNode(args)
	search, _ := pagination["search"].(string)
	filters, _ := pagination["filters"].([]any)
	var matched []*Node
	for _, n := range b.sorted() {
		if n.Type != "SUBNET" || (hasScope && !b.inScope(n, scope)) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(n.Label+n.CIDR+derefStr(n.Owner)+derefStr(n.VLAN)), strings.ToLower(search)) {
			continue
		}
		if !b.matchesFilters(n, filters) {
			continue
		}
		matched = append(matched, n)
	}
	limit, offset := int(argInt(pagination["limit"])), int(argInt(pagination["offset"]))
	end := min(offset+limit, len(matched))
	items := []any{}
	for _, n := range matched[min(offset, len(matched)):end] {
		items = append(items, b.nodeJSON(n))
	}
	return map[string]any{"items": items, "total": len(matched), "hasMore": end < len(matched)}, nil
}

func scopeNode(args map[string]any) (int64, bool) {
	scope, ok := args["scope"].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := scope["scopeNodeId"].(float64)
	return int64(v), ok
}

func (b *Backend) matchesFilters(n *Node, filters []any) bool {
	for _, raw := range filters {
		f, _ := raw.(map[string]any)
		field, _ := f["field"].(string)
		value := fmt.Sprint(f["value"])
		got := ""
		switch {
		case strings.HasPrefix(field, "tag."):
			got = strings.Join(b.Srv.TagValues(client.TagObjectSubnet, n.ID)[strings.TrimPrefix(field, "tag.")], ",")
		case field == "name":
			got = n.Label
		case field == "owner":
			got = derefStr(n.Owner)
		case field == "protocol":
			got = n.Protocol
		}
		switch f["operator"] {
		case "EQ":
			if got != value {
				return false
			}
		case "CONTAINS":
			if !strings.Contains(got, value) {
				return false
			}
		case "NOT_NULL":
			if got == "" {
				return false
			}
		}
	}
	return true
}

func gqlSubnetID(n *Node) any {
	if n.Type == "SUBNET" {
		return n.ID
	}
	return nil
}
