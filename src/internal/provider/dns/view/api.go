package view

import (
	"context"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/dnskit"
)

const (
	viewsPath = "/views/"
	pageSize  = 200

	viewFields = `id name description priority zoneCount serverGroupIds matchDestinationsAclId matchRecursiveOnly ` +
		`recursion allowRecursionAclId forwarders { ip port } forwardOnly maxCacheTtl maxNcacheTtl ` +
		`allowQueryAclId allowUpdateAclId aclDetails { id } createdOn updatedOn`

	viewByIDQuery = `query ViewByID($id: Int!) { viewById(id: $id) { ` + viewFields + ` } }`
	viewsQuery    = `query Views($pagination: PaginationInput) { viewsPaginated(pagination: $pagination) { items { ` +
		viewFields + ` } total hasMore } }`
)

type forwarderDTO struct {
	IP   string `json:"ip"`
	Port int64  `json:"port"`
}

type viewDTO struct {
	ID                     int64          `json:"id"`
	Name                   string         `json:"name"`
	Description            *string        `json:"description"`
	Priority               *int64         `json:"priority"`
	ZoneCount              *int64         `json:"zoneCount"`
	ServerGroupIDs         []int64        `json:"serverGroupIds"`
	MatchDestinationsACLID *int64         `json:"matchDestinationsAclId"`
	MatchRecursiveOnly     *bool          `json:"matchRecursiveOnly"`
	Recursion              *bool          `json:"recursion"`
	AllowRecursionACLID    *int64         `json:"allowRecursionAclId"`
	Forwarders             []forwarderDTO `json:"forwarders"`
	ForwardOnly            *bool          `json:"forwardOnly"`
	MaxCacheTTL            *int64         `json:"maxCacheTtl"`
	MaxNcacheTTL           *int64         `json:"maxNcacheTtl"`
	AllowQueryACLID        *int64         `json:"allowQueryAclId"`
	AllowUpdateACLID       *int64         `json:"allowUpdateAclId"`
	ACLDetails             *struct {
		ID int64 `json:"id"`
	} `json:"aclDetails"`
	CreatedOn *string `json:"createdOn"`
	UpdatedOn *string `json:"updatedOn"`
}

func (d *viewDTO) viewACLID() *int64 {
	if d.ACLDetails == nil {
		return nil
	}
	return &d.ACLDetails.ID
}

func fetchView(ctx context.Context, c *client.Client, id int64) (*viewDTO, error) {
	var out struct {
		View *viewDTO `json:"viewById"`
	}
	if err := c.Query(ctx, viewByIDQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.View == nil {
		return nil, client.NewNotFound("view")
	}
	return out.View, nil
}

func listViews(ctx context.Context, c *client.Client, filters []dnskit.QueryFilter) ([]viewDTO, error) {
	vars := map[string]any{}
	if len(filters) > 0 {
		vars["pagination"] = map[string]any{"filters": filters}
	}
	return client.PaginateField[viewDTO](ctx, c, viewsQuery, "viewsPaginated", vars, client.PageOptions{PageSize: pageSize, SortBy: "id"})
}

func findViewByName(ctx context.Context, c *client.Client, name string) (*viewDTO, error) {
	views, err := listViews(ctx, c, []dnskit.QueryFilter{dnskit.ExactFilter("name", name)})
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].Name == name {
			return &views[i], nil
		}
	}
	return nil, client.NewNotFound("view")
}
