package subnet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

const (
	subnetFields = `id networkAddr prefix isDhcp6 config serverGroupId pools optionData viewId keaSubnetId createdOn updatedOn subnet sharedNetworkId vlans { id } clientClasses { id }`

	byIDQuery = `query DhcpSubnetById($id: Int!) {
  subnetById(id: $id) { ` + subnetFields + ` }
}`

	reservationCountQuery = `query DhcpSubnetReservationCount($subnetId: Int, $pagination: PaginationInput) {
  hostReservationsPaginated(subnetId: $subnetId, pagination: $pagination) { total }
}`

	activeLeaseCountQuery = `query DhcpSubnetActiveLeaseCount($subnetId: Int, $pagination: PaginationInput) {
  leasesPaginated(subnetId: $subnetId, pagination: $pagination) { total }
}`

	activeLeaseState = "ACTIVE"
)

type idRef struct {
	ID int64 `json:"id"`
}

type dto struct {
	ID              int64           `json:"id"`
	NetworkAddr     *string         `json:"networkAddr"`
	Prefix          int64           `json:"prefix"`
	IsDHCP6         bool            `json:"isDhcp6"`
	Config          map[string]any  `json:"config"`
	ServerGroupID   *int64          `json:"serverGroupId"`
	Pools           json.RawMessage `json:"pools"`
	OptionData      json.RawMessage `json:"optionData"`
	ViewID          *int64          `json:"viewId"`
	KeaSubnetID     *int64          `json:"keaSubnetId"`
	CreatedOn       *string         `json:"createdOn"`
	UpdatedOn       *string         `json:"updatedOn"`
	Subnet          *string         `json:"subnet"`
	SharedNetworkID *int64          `json:"sharedNetworkId"`
	VLANs           []idRef         `json:"vlans"`
	ClientClasses   []idRef         `json:"clientClasses"`
}

func decodeObjects(raw json.RawMessage) []map[string]any {
	var out []map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func (d dto) pools() []map[string]any { return decodeObjects(d.Pools) }

func (d dto) options() []map[string]any { return decodeObjects(d.OptionData) }

func (d dto) cidr() (string, bool) {
	if d.Subnet != nil {
		if prefix, err := nodeapi.ParsePrefix(*d.Subnet); err == nil {
			return prefix.String(), true
		}
	}
	if d.NetworkAddr != nil {
		if prefix, err := nodeapi.ParsePrefix(fmt.Sprintf("%s/%d", *d.NetworkAddr, d.Prefix)); err == nil {
			return prefix.String(), true
		}
	}
	return "", false
}

func (d dto) configInt(key string) *int64 {
	switch n := d.Config[key].(type) {
	case float64:
		v := int64(n)
		return &v
	case int64:
		return &n
	}
	return nil
}

func (d dto) configString(key string) *string {
	if s, ok := d.Config[key].(string); ok && s != "" {
		return &s
	}
	return nil
}

func (d dto) configBool(key string) *bool {
	if b, ok := d.Config[key].(bool); ok {
		return &b
	}
	return nil
}

func (d dto) ddnsConfig() map[string]any {
	cfg, _ := d.Config["ddns_config"].(map[string]any)
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func fetchByID(ctx context.Context, c *client.Client, id int64) (*dto, error) {
	var out struct {
		Subnet *dto `json:"subnetById"`
	}
	if err := c.Query(ctx, byIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Subnet == nil {
		return nil, client.NewNotFound("DHCP subnet")
	}
	return out.Subnet, nil
}

func countTotal(ctx context.Context, c *client.Client, query, field string, vars map[string]any) (int, error) {
	var out map[string]client.Page[json.RawMessage]
	if err := c.Query(ctx, query, vars, &out); err != nil {
		return 0, err
	}
	return out[field].Total, nil
}

func firstRowOnly(filters ...map[string]any) map[string]any {
	pagination := map[string]any{"limit": 1, "offset": 0}
	if len(filters) > 0 {
		pagination["filters"] = filters
	}
	return pagination
}

func countReservations(ctx context.Context, c *client.Client, subnetID int64) (int, error) {
	vars := map[string]any{"subnetId": subnetID, "pagination": firstRowOnly()}
	return countTotal(ctx, c, reservationCountQuery, "hostReservationsPaginated", vars)
}

func countActiveLeases(ctx context.Context, c *client.Client, subnetID int64) (int, error) {
	vars := map[string]any{"subnetId": subnetID, "pagination": firstRowOnly(dhcpkit.EqFilter("state", activeLeaseState))}
	return countTotal(ctx, c, activeLeaseCountQuery, "leasesPaginated", vars)
}

func resolveDHCPCIDR(ctx context.Context, c *client.Client, cidr string) (int64, error) {
	node, err := nodeapi.FindSubnetByCIDR(ctx, c, cidr)
	if err != nil {
		return 0, err
	}
	configured, err := nodeapi.HasDHCPConfig(ctx, c, node.ID)
	if err != nil {
		return 0, err
	}
	if !configured {
		return 0, client.NewNotFound("DHCP subnet " + cidr + " (the IPAM subnet exists but has no DHCP service)")
	}
	return node.ID, nil
}
