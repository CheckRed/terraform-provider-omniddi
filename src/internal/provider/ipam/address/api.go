package address

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	allocatePath = "/ipam/next-free-ip/allocate"

	codeAllocationBusy = "ALLOCATION_BUSY"
	codeNoFreeIP       = "NO_FREE_IP"

	addressFields = `id ipAddress status hostname subnetId`

	ByIDQuery   = `query IpamAddressById($id: Int!) { unifiedIp(ipAddressId: $id) { ` + addressFields + ` } }`
	SearchQuery = `query IpamAddresses($scope: IpScopeInput, $pagination: PaginationInput) { ` +
		`managedIpsPaginated(scope: $scope, pagination: $pagination) { items { ` + addressFields + ` } hasMore } }`

	searchPageSize = 100
	maxSearchPages = 20
)

var AllQueries = []string{ByIDQuery, SearchQuery}

type dto struct {
	ID       int64   `json:"id"`
	IP       string  `json:"ipAddress"`
	Status   string  `json:"status"`
	Hostname *string `json:"hostname"`
	SubnetID *int64  `json:"subnetId"`
}

func (d dto) addr() (netip.Addr, bool) {
	if a, err := netip.ParseAddr(d.IP); err == nil {
		return a, true
	}
	if p, err := netip.ParsePrefix(d.IP); err == nil {
		return p.Addr(), true
	}
	return netip.Addr{}, false
}

func (d dto) inSubnet(subnetID int64) bool { return d.SubnetID != nil && *d.SubnetID == subnetID }

func (d dto) holds(name string) bool {
	return d.Hostname != nil && sameName(*d.Hostname, name)
}

type allocateResponse struct {
	Address  string `json:"address"`
	RecordID int64  `json:"record_id"`
	Status   string `json:"status"`
	Subnet   struct {
		ID   int64  `json:"id"`
		CIDR string `json:"cidr"`
	} `json:"subnet"`
}

func sameName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

func fetchByID(ctx context.Context, c *client.Client, id int64) (*dto, error) {
	var out struct {
		Address *dto `json:"unifiedIp"`
	}
	if err := c.Query(ctx, ByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Address == nil {
		return nil, client.NewNotFound("IP address")
	}
	return out.Address, nil
}

func search(ctx context.Context, c *client.Client, networkID int64, text string) ([]dto, error) {
	vars := map[string]any{}
	if networkID != 0 {
		vars["scope"] = map[string]any{"scopeNodeId": networkID, "includeDescendants": false}
	}
	var all []dto
	for page := 0; page < maxSearchPages; page++ {
		vars["pagination"] = map[string]any{"limit": searchPageSize, "offset": page * searchPageSize, "search": text}
		var out struct {
			Page struct {
				Items   []dto `json:"items"`
				HasMore bool  `json:"hasMore"`
			} `json:"managedIpsPaginated"`
		}
		if err := c.Query(ctx, SearchQuery, vars, &out); err != nil {
			return nil, err
		}
		for _, row := range out.Page.Items {
			if row.ID != 0 {
				all = append(all, row)
			}
		}
		if !out.Page.HasMore {
			return all, nil
		}
	}
	return nil, &client.PageOverflowError{Query: "managedIpsPaginated", PageSize: searchPageSize, MaxPages: maxSearchPages}
}

func statusLabel(enum string) string {
	if enum == "" {
		return ""
	}
	return strings.ToUpper(enum[:1]) + strings.ToLower(enum[1:])
}

func idsHolding(rows []dto, subnetID int64, name string) map[int64]bool {
	out := map[int64]bool{}
	for _, row := range rows {
		if row.inSubnet(subnetID) && row.holds(name) {
			out[row.ID] = true
		}
	}
	return out
}

func apiCode(err error) string {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

type unreadableResponseError struct{ cause error }

func (e *unreadableResponseError) Error() string {
	return "OmniDDI accepted the allocate request but its answer could not be read: " + e.cause.Error()
}

func (e *unreadableResponseError) Unwrap() error { return e.cause }

func ambiguous(err error) bool {
	var unreadable *unreadableResponseError
	return errors.As(err, &unreadable) || apiCode(err) == "" && client.IsAmbiguousWriteFailure(err)
}

type importKey struct {
	cidr string
	ip   netip.Addr
}

func parseImportKey(key string) (importKey, error) {
	if ip, err := netip.ParseAddr(key); err == nil {
		return importKey{ip: ip}, nil
	}
	if cut := strings.LastIndex(key, "/"); cut > 0 {
		prefix, perr := netip.ParsePrefix(key[:cut])
		ip, ierr := netip.ParseAddr(key[cut+1:])
		if perr == nil && ierr == nil {
			return importKey{cidr: prefix.Masked().String(), ip: ip}, nil
		}
	}
	return importKey{}, fmt.Errorf("import key %q must be an id, an IP address, or <subnet cidr>/<ip address>", key)
}

func matchIP(rows []dto, ip netip.Addr) []dto {
	var out []dto
	for _, row := range rows {
		if a, ok := row.addr(); ok && a == ip {
			out = append(out, row)
		}
	}
	return out
}

func allocateBody(subnetID int64, name string, exclude []string) map[string]any {
	body := map[string]any{"subnet_id": subnetID, "name": name}
	if len(exclude) > 0 {
		body["exclude"] = exclude
	}
	return body
}

func allocate(ctx context.Context, c *client.Client, body map[string]any) (*allocateResponse, error) {
	result, err := c.Write(ctx, http.MethodPost, allocatePath, body)
	if err != nil {
		return nil, err
	}
	var out allocateResponse
	if err := result.Decode(&out); err != nil {
		return nil, &unreadableResponseError{cause: err}
	}
	if out.RecordID == 0 || out.Address == "" {
		return nil, &unreadableResponseError{cause: errors.New("no address or record id in the response")}
	}
	return &out, nil
}
