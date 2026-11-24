package reservation

import (
	"context"
	"net/netip"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dhcp/dhcpkit"
)

const (
	identifierMAC  = "Mac Address"
	identifierDUID = "DUID"

	reservationFields = `id name subnet_id server_group_id identifier_type identifier ip_address is_dhcp6 subnet description`

	byIDQuery = `query HostReservationById($id: Int!) {
  hostReservationById(id: $id) { ` + reservationFields + ` }
}`

	listQuery = `query HostReservations($pagination: PaginationInput, $subnetId: Int) {
  hostReservationsPaginated(pagination: $pagination, subnetId: $subnetId) {
    items { ` + reservationFields + ` }
    hasMore
  }
}`
)

var (
	macPattern  = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}$`)
	duidPattern = regexp.MustCompile(`^[0-9A-Fa-f:]+$`)
)

type dto struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	SubnetID       int64   `json:"subnet_id"`
	ServerGroupID  *int64  `json:"server_group_id"`
	IdentifierType *string `json:"identifier_type"`
	Identifier     *string `json:"identifier"`
	IPAddress      *string `json:"ip_address"`
	IsDHCP6        bool    `json:"is_dhcp6"`
	Subnet         *string `json:"subnet"`
	Description    *string `json:"description"`
}

func identifierTypeFromWire(raw *string) types.String {
	if raw == nil || *raw == "" {
		return types.StringNull()
	}
	switch strings.ToUpper(strings.ReplaceAll(*raw, " ", "_")) {
	case "MAC_ADDRESS":
		return types.StringValue(identifierMAC)
	case "DUID":
		return types.StringValue(identifierDUID)
	}
	return types.StringValue(*raw)
}

func canonicalIdentifier(idType, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if idType == identifierMAC {
		return strings.ReplaceAll(value, "-", ":")
	}
	return value
}

func identifierFromWire(raw *string, idType, prior types.String) types.String {
	if raw == nil || *raw == "" {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() && canonicalIdentifier(idType.ValueString(), prior.ValueString()) == canonicalIdentifier(idType.ValueString(), *raw) {
		return prior
	}
	return types.StringValue(*raw)
}

func fetchByID(ctx context.Context, c *client.Client, id int64) (*dto, error) {
	var out struct {
		Reservation *dto `json:"hostReservationById"`
	}
	if err := c.Query(ctx, byIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Reservation == nil {
		return nil, client.NewNotFound("host reservation")
	}
	return out.Reservation, nil
}

func findAll(ctx context.Context, c *client.Client, filters []map[string]any, subnetID *int64) ([]dto, error) {
	vars := map[string]any{}
	if subnetID != nil {
		vars["subnetId"] = *subnetID
	}
	return dhcpkit.FetchAll[dto](ctx, c, listQuery, "hostReservationsPaginated", vars, filters)
}

func findByName(ctx context.Context, c *client.Client, name string) (*dto, error) {
	items, err := findAll(ctx, c, []map[string]any{dhcpkit.EqFilter("name", name)}, nil)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Name == name {
			return &items[i], nil
		}
	}
	return nil, client.NewNotFound("host reservation")
}

func canonicalIP(ip string) string {
	if addr, err := netip.ParseAddr(ip); err == nil {
		return addr.String()
	}
	return ip
}

func sameSubnet(remote *string, cidr string) bool {
	if remote == nil {
		return false
	}
	a, errA := netip.ParsePrefix(*remote)
	b, errB := netip.ParsePrefix(cidr)
	return errA == nil && errB == nil && a.Masked() == b.Masked()
}

func parseImportKey(key string) (cidr, name string) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) == 3 {
		if _, err := netip.ParsePrefix(parts[0] + "/" + parts[1]); err == nil {
			return parts[0] + "/" + parts[1], parts[2]
		}
	}
	return "", key
}
