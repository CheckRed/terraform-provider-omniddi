package nodeapi

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	TypeRoot      = "ROOT"
	TypeFamily    = "FAMILY"
	TypeContainer = "CONTAINER"
	TypeSubnet    = "SUBNET"

	nodeFields = `id type parentId label subnetId cidr prefixLength protocol businessUnitId vlan owner tags description status createdOn updatedOn hasChildren`

	NodeByIDQuery    = `query IpamNodeById($id: Int!) { ipSpaceNodeById(id: $id) { ` + nodeFields + ` } }`
	ChildrenQuery    = `query IpamNodeChildren($parentId: Int, $search: String) { ipSpaceChildren(parentId: $parentId, search: $search) { ` + nodeFields + ` } }`
	AncestorsQuery   = `query IpamNodeAncestors($nodeId: Int!) { ipSpaceAncestors(nodeId: $nodeId) { id } }`
	SubnetsPageQuery = `query IpamSubnets($scope: IpScopeInput, $pagination: PaginationInput) { managedSubnetsPaginated(scope: $scope, pagination: $pagination) { items { ` + nodeFields + ` } total hasMore } }`
	DHCPConfigQuery  = `query IpamDhcpConfig($id: Int!) { subnetById(id: $id) { id } }`

	subnetPageSize    = 200
	maxTreeDepth      = 64
	familySearchDepth = 2
	subnetSortKey     = "label"
)

var AllQueries = []string{NodeByIDQuery, ChildrenQuery, AncestorsQuery, SubnetsPageQuery, DHCPConfigQuery}

type Node struct {
	ID             int64          `json:"id"`
	Type           string         `json:"type"`
	ParentID       *int64         `json:"parentId"`
	Label          string         `json:"label"`
	SubnetID       *int64         `json:"subnetId"`
	CIDR           *string        `json:"cidr"`
	PrefixLength   *int64         `json:"prefixLength"`
	Protocol       *string        `json:"protocol"`
	BusinessUnitID *int64         `json:"businessUnitId"`
	VLAN           *string        `json:"vlan"`
	Owner          *string        `json:"owner"`
	Tags           map[string]any `json:"tags"`
	Description    *string        `json:"description"`
	Status         *string        `json:"status"`
	CreatedOn      *string        `json:"createdOn"`
	UpdatedOn      *string        `json:"updatedOn"`
	HasChildren    bool           `json:"hasChildren"`
}

func (n Node) Prefix() (netip.Prefix, bool) {
	if n.CIDR == nil {
		return netip.Prefix{}, false
	}
	p, err := ParsePrefix(*n.CIDR)
	return p, err == nil
}

func Fetch(ctx context.Context, c *client.Client, id int64) (*Node, error) {
	var out struct {
		Node *Node `json:"ipSpaceNodeById"`
	}
	if err := c.Query(ctx, NodeByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Node == nil {
		return nil, client.NewNotFound("IP space node")
	}
	return out.Node, nil
}

// The node's subnetId is its own id on every SUBNET, so only subnetById tells whether DHCP is configured.
func HasDHCPConfig(ctx context.Context, c *client.Client, id int64) (bool, error) {
	var out struct {
		Subnet *struct {
			ID int64 `json:"id"`
		} `json:"subnetById"`
	}
	if err := c.Query(ctx, DHCPConfigQuery, map[string]any{"id": id}, &out); err != nil {
		return false, err
	}
	return out.Subnet != nil, nil
}

func FetchOfType(ctx context.Context, c *client.Client, id int64, nodeType string) (*Node, error) {
	node, err := Fetch(ctx, c, id)
	if err != nil {
		return nil, err
	}
	if node.Type != nodeType {
		return nil, fmt.Errorf("IP space node %d is a %s, not a %s", id, node.Type, nodeType)
	}
	return node, nil
}

func Children(ctx context.Context, c *client.Client, parentID *int64, search string) ([]Node, error) {
	vars := map[string]any{"parentId": parentID}
	if search != "" {
		vars["search"] = search
	}
	var out struct {
		Nodes []Node `json:"ipSpaceChildren"`
	}
	if err := c.Query(ctx, ChildrenQuery, vars, &out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

func AncestorIDs(ctx context.Context, c *client.Client, id int64) ([]int64, error) {
	var out struct {
		Nodes []struct {
			ID int64 `json:"id"`
		} `json:"ipSpaceAncestors"`
	}
	if err := c.Query(ctx, AncestorsQuery, map[string]any{"nodeId": id}, &out); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(out.Nodes))
	for _, n := range out.Nodes {
		ids = append(ids, n.ID)
	}
	return ids, nil
}

func ResolveCIDR(ctx context.Context, c *client.Client, cidr string, nodeType string) (*Node, error) {
	target, err := ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}
	level, err := Children(ctx, c, nil, "")
	if err != nil {
		return nil, err
	}
	for depth := 0; len(level) > 0; depth++ {
		if depth >= maxTreeDepth {
			return nil, &TreeDepthError{Limit: maxTreeDepth}
		}
		var descend []Node
		for _, n := range level {
			prefix, hasCIDR := n.Prefix()
			switch {
			case hasCIDR && prefix == target:
				if nodeType != "" && n.Type != nodeType {
					return nil, client.NewNotFound(fmt.Sprintf("%s %s", strings.ToLower(nodeType), target))
				}
				found := n
				return &found, nil
			case hasCIDR && n.HasChildren && prefix.Bits() < target.Bits() && prefix.Overlaps(target):
				descend = append(descend, n)
			case !hasCIDR && n.HasChildren && familyMatches(n, target):
				descend = append(descend, n)
			}
		}
		level, err = childrenOfAll(ctx, c, descend)
		if err != nil {
			return nil, err
		}
	}
	return nil, client.NewNotFound(fmt.Sprintf("IP space node %s", target))
}

type TreeDepthError struct{ Limit int }

func (e *TreeDepthError) Error() string {
	return fmt.Sprintf("the IP space tree is deeper than %d levels; refusing to descend further", e.Limit)
}

func childrenOfAll(ctx context.Context, c *client.Client, parents []Node) ([]Node, error) {
	var level []Node
	for _, parent := range parents {
		id := parent.ID
		kids, err := Children(ctx, c, &id, "")
		if err != nil {
			return nil, err
		}
		level = append(level, kids...)
	}
	return level, nil
}

func familyMatches(n Node, target netip.Prefix) bool {
	if n.Protocol == nil || *n.Protocol == "" {
		return true
	}
	return strings.EqualFold(*n.Protocol, ProtocolOf(target))
}

type SubnetFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type SubnetQuery struct {
	Search      string
	Filters     []SubnetFilter
	ScopeNodeID *int64
}

func ListSubnets(ctx context.Context, c *client.Client, q SubnetQuery) ([]Node, error) {
	pagination := map[string]any{}
	if q.Search != "" {
		pagination["search"] = q.Search
	}
	if len(q.Filters) > 0 {
		pagination["filters"] = q.Filters
	}
	vars := map[string]any{"pagination": pagination}
	if q.ScopeNodeID != nil {
		vars["scope"] = map[string]any{"scopeNodeId": *q.ScopeNodeID, "includeDescendants": true}
	}
	return client.PaginateField[Node](ctx, c, SubnetsPageQuery, "managedSubnetsPaginated", vars,
		client.PageOptions{PageSize: subnetPageSize, SortBy: subnetSortKey})
}

func FindSubnetByCIDR(ctx context.Context, c *client.Client, cidr string) (*Node, error) {
	target, err := ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}
	found, err := ListSubnets(ctx, c, SubnetQuery{Search: target.String()})
	if err != nil {
		return nil, err
	}
	for _, n := range found {
		if prefix, ok := n.Prefix(); ok && prefix == target {
			match := n
			return &match, nil
		}
	}
	return nil, client.NewNotFound("subnet " + target.String())
}

func FindFamily(ctx context.Context, c *client.Client, protocol string) (*Node, error) {
	level, err := Children(ctx, c, nil, "")
	if err != nil {
		return nil, err
	}
	for depth := 0; len(level) > 0 && depth < familySearchDepth; depth++ {
		var descend []Node
		for _, n := range level {
			switch {
			case n.Type == TypeFamily && n.Protocol != nil && strings.EqualFold(*n.Protocol, protocol):
				found := n
				return &found, nil
			case n.Type == TypeRoot && n.HasChildren:
				descend = append(descend, n)
			}
		}
		level, err = childrenOfAll(ctx, c, descend)
		if err != nil {
			return nil, err
		}
	}
	return nil, client.NewNotFound(protocol + " family root")
}
