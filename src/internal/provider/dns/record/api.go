package record

import (
	"context"
	"fmt"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

const (
	recordSelection = `id zoneId name type ttl effectiveTtl data zoneName viewId viewName isActive isAutoCreated createdOn updatedOn recordSetOrdering values { value enabled index }`

	recordByIDQuery = `query RecordById($id: Int!) { recordById(id: $id) { ` + recordSelection + ` } }`

	recordsInZoneQuery = `query RecordsInZone($zoneId: Int!, $recordType: String, $pagination: PaginationInput) {
  recordsPaginated(zoneId: $zoneId, recordType: $recordType, pagination: $pagination) {
    items { ` + recordSelection + ` }
    total
    hasMore
  }
}`

	recordKeyQuery = `query RecordByKey($zoneId: Int!, $recordType: String!, $pagination: PaginationInput) {
  recordsPaginated(zoneId: $zoneId, recordType: $recordType, pagination: $pagination) {
    items { id name }
  }
}`

	zoneLookupQuery = `query ZoneLookup($pagination: PaginationInput) {
  zonesPaginated(pagination: $pagination) {
    items { id name viewName }
  }
}`

	lookupPageSize = 200
	listPageSize   = 500
)

type valueState struct {
	Value   any  `json:"value"`
	Enabled bool `json:"enabled"`
	Index   int  `json:"index"`
}

type recordDTO struct {
	ID                int64        `json:"id"`
	ZoneID            int64        `json:"zoneId"`
	Name              *string      `json:"name"`
	Type              string       `json:"type"`
	TTL               *int64       `json:"ttl"`
	EffectiveTTL      *int64       `json:"effectiveTtl"`
	Data              any          `json:"data"`
	ZoneName          *string      `json:"zoneName"`
	ViewID            *int64       `json:"viewId"`
	ViewName          *string      `json:"viewName"`
	IsActive          *bool        `json:"isActive"`
	IsAutoCreated     *bool        `json:"isAutoCreated"`
	CreatedOn         *string      `json:"createdOn"`
	UpdatedOn         *string      `json:"updatedOn"`
	RecordSetOrdering *string      `json:"recordSetOrdering"`
	Values            []valueState `json:"values"`
}

type recordAPI struct {
	pd *framework.ProviderData
}

func (a recordAPI) fetch(ctx context.Context, id int64) (*recordDTO, error) {
	var out struct {
		Record *recordDTO `json:"recordById"`
	}
	if err := a.pd.Client.Query(ctx, recordByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Record == nil {
		return nil, client.NewNotFound("record")
	}
	if out.Record.IsActive != nil && !*out.Record.IsActive {
		return nil, &inactiveRecordError{id: id}
	}
	return out.Record, nil
}

func nameFilter(name string) map[string]any {
	return map[string]any{"field": "name", "operator": "EQ", "value": name}
}

func activeFilter(active bool) map[string]any {
	return map[string]any{"field": "isActive", "operator": "EQ", "value": active}
}

func (a recordAPI) idByKey(ctx context.Context, zoneID int64, name, recordType string) (int64, error) {
	return a.idByKeyAndState(ctx, zoneID, name, recordType, true)
}

func (a recordAPI) inactiveIDByKey(ctx context.Context, zoneID int64, name, recordType string) (int64, error) {
	return a.idByKeyAndState(ctx, zoneID, name, recordType, false)
}

func (a recordAPI) idByKeyAndState(ctx context.Context, zoneID int64, name, recordType string, active bool) (int64, error) {
	name = normalizeName(name)
	searches := []map[string]any{nameFilter(name)}
	if name == apexName {
		searches = append(searches, map[string]any{"field": "name", "operator": "IS_NULL"})
	}
	for _, filter := range searches {
		vars := map[string]any{
			"zoneId":     zoneID,
			"recordType": recordType,
			"pagination": map[string]any{"limit": lookupPageSize, "filters": []any{filter, activeFilter(active)}},
		}
		var out struct {
			Page struct {
				Items []struct {
					ID   int64   `json:"id"`
					Name *string `json:"name"`
				} `json:"items"`
			} `json:"recordsPaginated"`
		}
		if err := a.pd.Client.Query(ctx, recordKeyQuery, vars, &out); err != nil {
			return 0, err
		}
		for _, item := range out.Page.Items {
			if sameOwnerName(item.Name, name) {
				return item.ID, nil
			}
		}
	}
	return 0, client.NewNotFound(fmt.Sprintf("%s record %q", recordType, name))
}

func sameOwnerName(remote *string, want string) bool {
	return normalizeName(derefString(remote)) == want
}

func (a recordAPI) zoneIDByName(ctx context.Context, viewName, zoneName string) (int64, error) {
	vars := map[string]any{"pagination": map[string]any{
		"limit":   lookupPageSize,
		"filters": []any{map[string]any{"field": "name", "operator": "EQ", "value": zoneName}},
	}}
	var out struct {
		Page struct {
			Items []struct {
				ID       int64   `json:"id"`
				Name     string  `json:"name"`
				ViewName *string `json:"viewName"`
			} `json:"items"`
		} `json:"zonesPaginated"`
	}
	if err := a.pd.Client.Query(ctx, zoneLookupQuery, vars, &out); err != nil {
		return 0, err
	}
	for _, item := range out.Page.Items {
		inView := item.ViewName != nil && strings.EqualFold(*item.ViewName, viewName)
		if inView && framework.NormalizeFQDN(item.Name) == framework.NormalizeFQDN(zoneName) {
			return item.ID, nil
		}
	}
	return 0, client.NewNotFound(fmt.Sprintf("zone %q in view %q", zoneName, viewName))
}

func (a recordAPI) idByNaturalKey(ctx context.Context, key string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(key), "/")
	if len(parts) != 4 || slicesContainsEmpty(parts) {
		return 0, fmt.Errorf("expected a numeric id or a natural key of the form view/zone/name/type, got %q", key)
	}
	zoneID, err := a.zoneIDByName(ctx, parts[0], parts[1])
	if err != nil {
		return 0, err
	}
	return a.idByKey(ctx, zoneID, parts[2], strings.ToUpper(parts[3]))
}

func slicesContainsEmpty(parts []string) bool {
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return true
		}
	}
	return false
}

func (a recordAPI) recoverByKey(zoneID int64, name, recordType string) func(context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := a.idByKey(ctx, zoneID, name, recordType)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}
