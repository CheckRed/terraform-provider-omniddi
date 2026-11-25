package servergroup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	typeDNS            = "dns"
	typeDHCP           = "dhcp"
	typeAnalytics      = "analytics"
	tagObjectDHCPGroup = "DHCP_SERVER_GROUP"
	groupFields        = "id name description type dnsVendorId dhcpVendorId protocol protocolId"

	groupByIDQuery = `query ServerGroupByID($id: Int!) {
  serverGroupById(id: $id) { ` + groupFields + ` serversList { id sg_role } }
}`

	groupsQuery = `query ServerGroups($pagination: PaginationInput, $type: String) {
  serverGroupsPaginated(pagination: $pagination, type: $type) {
    items { ` + groupFields + ` }
    total
    hasMore
  }
}`
)

type memberDTO struct {
	ID   int64   `json:"id"`
	Role *string `json:"sg_role"`
}

type groupDTO struct {
	ID           int64       `json:"id"`
	Name         string      `json:"name"`
	Description  *string     `json:"description"`
	Type         string      `json:"type"`
	DNSVendorID  *int64      `json:"dnsVendorId"`
	DHCPVendorID *int64      `json:"dhcpVendorId"`
	Protocol     *string     `json:"protocol"`
	ProtocolID   *string     `json:"protocolId"`
	Servers      []memberDTO `json:"serversList"`
}

func (g groupDTO) scopeID() *int64 {
	if g.ProtocolID == nil {
		return nil
	}
	id, err := strconv.ParseInt(*g.ProtocolID, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func (g groupDTO) tagObject() string { return tagObjectFor(g.Type) }

func tagObjectFor(groupType string) string {
	switch strings.ToLower(groupType) {
	case typeDNS:
		return client.TagObjectDNSServerGroup
	case typeDHCP:
		return tagObjectDHCPGroup
	}
	return ""
}

func fetchGroup(ctx context.Context, c *client.Client, id int64) (*groupDTO, error) {
	var out struct {
		Group *groupDTO `json:"serverGroupById"`
	}
	if err := c.Query(ctx, groupByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Group == nil {
		return nil, client.NewNotFound("server group")
	}
	return out.Group, nil
}

func listGroups(ctx context.Context, c *client.Client, groupType, name string) ([]groupDTO, error) {
	vars := map[string]any{}
	if groupType != "" {
		vars["type"] = groupType
	}
	if name != "" {
		vars["pagination"] = map[string]any{"filters": []map[string]any{{"field": "name", "operator": "EQ", "value": name}}}
	}
	groups, err := client.PaginateField[groupDTO](ctx, c, groupsQuery, "serverGroupsPaginated", vars, client.PageOptions{SortBy: "id"})
	if err != nil || name == "" {
		return groups, err
	}
	exact := groups[:0:0]
	for _, g := range groups {
		if g.Name == name {
			exact = append(exact, g)
		}
	}
	return exact, nil
}

func findGroupID(ctx context.Context, c *client.Client, groupType, name string) (int64, error) {
	groups, err := listGroups(ctx, c, groupType, name)
	if err != nil {
		return 0, err
	}
	switch len(groups) {
	case 0:
		return 0, client.NewNotFound(fmt.Sprintf("server group %q", name))
	case 1:
		return groups[0].ID, nil
	}
	return 0, fmt.Errorf("%d server groups are named %q; qualify the lookup with a type (dns or dhcp)", len(groups), name)
}
