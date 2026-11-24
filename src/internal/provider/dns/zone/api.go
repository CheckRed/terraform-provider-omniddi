package zone

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

const (
	actionCreate = "create_zone"
	actionUpdate = "update_zone"
	actionDelete = "delete_zone"

	zonesPath = "/zones/"
	pageSize  = 200

	zoneFields = `id name zoneType networkScope viewId viewName recordsCount configJson createdOn updatedOn ` +
		`soa { ttl nttl serial refresh retry expire primaryNameServer } ` +
		`zoneTransferTsigKeyId dynamicDnsTsigKeyId secondarySyncTsigKeyId businessUnits { id } primaryBusinessUnit { id }`

	zoneByIDQuery = `query ZoneByID($id: Int!) { zoneById(id: $id) { ` + zoneFields + ` } }`
	zonesQuery    = `query Zones($pagination: PaginationInput, $viewId: Int) { zonesPaginated(pagination: $pagination, viewId: $viewId) { items { ` +
		zoneFields + ` } total hasMore } }`
	viewsByNameQuery = `query ViewsByName($pagination: PaginationInput) { viewsPaginated(pagination: $pagination) { items { id name } total hasMore } }`
	userRecordsQuery = `query ZoneUserRecords($zoneId: Int, $pagination: PaginationInput) { recordsPaginated(zoneId: $zoneId, pagination: $pagination) { total } }`
)

type flexInt int64

func (f *flexInt) UnmarshalJSON(raw []byte) error {
	var number json.Number
	text := strings.Trim(string(raw), `"`)
	if text == "" || text == "null" {
		return nil
	}
	number = json.Number(text)
	if n, err := number.Int64(); err == nil {
		*f = flexInt(n)
		return nil
	}
	value, err := number.Float64()
	if err != nil {
		return err
	}
	*f = flexInt(int64(value))
	return nil
}

func (f *flexInt) int64() *int64 {
	if f == nil {
		return nil
	}
	n := int64(*f)
	return &n
}

type idRef struct {
	ID int64 `json:"id"`
}

type soaDTO struct {
	TTL               *flexInt `json:"ttl"`
	Nttl              *flexInt `json:"nttl"`
	Serial            *flexInt `json:"serial"`
	Refresh           *flexInt `json:"refresh"`
	Retry             *flexInt `json:"retry"`
	Expire            *flexInt `json:"expire"`
	PrimaryNameServer *string  `json:"primaryNameServer"`
}

type zoneDTO struct {
	ID                     int64          `json:"id"`
	Name                   string         `json:"name"`
	ZoneType               *string        `json:"zoneType"`
	NetworkScope           *string        `json:"networkScope"`
	ViewID                 *int64         `json:"viewId"`
	ViewName               *string        `json:"viewName"`
	RecordsCount           *int64         `json:"recordsCount"`
	ConfigJSON             map[string]any `json:"configJson"`
	CreatedOn              *string        `json:"createdOn"`
	UpdatedOn              *string        `json:"updatedOn"`
	SOA                    *soaDTO        `json:"soa"`
	ZoneTransferTsigKeyID  *int64         `json:"zoneTransferTsigKeyId"`
	DynamicDNSTsigKeyID    *int64         `json:"dynamicDnsTsigKeyId"`
	SecondarySyncTsigKeyID *int64         `json:"secondarySyncTsigKeyId"`
	BusinessUnits          []idRef        `json:"businessUnits"`
	PrimaryBusinessUnit    *idRef         `json:"primaryBusinessUnit"`
}

func fetchZone(ctx context.Context, c *client.Client, id int64) (*zoneDTO, error) {
	var out struct {
		Zone *zoneDTO `json:"zoneById"`
	}
	if err := c.Query(ctx, zoneByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Zone == nil {
		return nil, client.NewNotFound("zone")
	}
	return out.Zone, nil
}

func listZones(ctx context.Context, c *client.Client, viewID *int64, filters []dnskit.QueryFilter) ([]zoneDTO, error) {
	vars := map[string]any{}
	if len(filters) > 0 {
		vars["pagination"] = map[string]any{"filters": filters}
	}
	if viewID != nil {
		vars["viewId"] = *viewID
	}
	return client.PaginateField[zoneDTO](ctx, c, zonesQuery, "zonesPaginated", vars, client.PageOptions{PageSize: pageSize, SortBy: "id"})
}

func findZone(ctx context.Context, c *client.Client, viewID int64, name string) (*zoneDTO, error) {
	zones, err := listZones(ctx, c, &viewID, []dnskit.QueryFilter{dnskit.ExactFilter("name", framework.NormalizeFQDN(name))})
	if err != nil {
		return nil, err
	}
	for i := range zones {
		if framework.NormalizeFQDN(zones[i].Name) == framework.NormalizeFQDN(name) {
			return &zones[i], nil
		}
	}
	return nil, client.NewNotFound("zone")
}

func resolveViewID(ctx context.Context, c *client.Client, name string) (int64, error) {
	type viewRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	vars := map[string]any{"pagination": map[string]any{"filters": []dnskit.QueryFilter{dnskit.ExactFilter("name", name)}}}
	views, err := client.PaginateField[viewRef](ctx, c, viewsByNameQuery, "viewsPaginated", vars, client.PageOptions{PageSize: pageSize, SortBy: "id"})
	if err != nil {
		return 0, err
	}
	for _, item := range views {
		if item.Name == name {
			return item.ID, nil
		}
	}
	return 0, client.NewNotFound("view")
}

func countUserRecords(ctx context.Context, c *client.Client, zoneID int64) (int64, error) {
	vars := map[string]any{
		"zoneId": zoneID,
		"pagination": map[string]any{
			"limit":   1,
			"filters": []dnskit.QueryFilter{{Field: "isAutoCreated", Operator: "EQ", Value: false}},
		},
	}
	var out struct {
		Page struct {
			Total int64 `json:"total"`
		} `json:"recordsPaginated"`
	}
	if err := c.Query(ctx, userRecordsQuery, vars, &out); err != nil {
		return 0, err
	}
	return out.Page.Total, nil
}
