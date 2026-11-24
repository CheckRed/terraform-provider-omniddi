package view_test

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/view"
)

var optionFields = map[string]string{
	"recursion":                 "recursion",
	"forward_only":              "forwardOnly",
	"match_recursive_only":      "matchRecursiveOnly",
	"allow_recursion_acl_id":    "allowRecursionAclId",
	"match_destinations_acl_id": "matchDestinationsAclId",
	"allow_query_acl_id":        "allowQueryAclId",
	"allow_update_acl_id":       "allowUpdateAclId",
	"max_cache_ttl":             "maxCacheTtl",
	"max_ncache_ttl":            "maxNcacheTtl",
	"forwarders":                "forwarders",
}

type viewRecord struct {
	ID          int64
	Name        string
	Description any
	Priority    int64
	Groups      []int64
	ACLID       any
	Zones       int64
	Options     map[string]any
}

type viewBackend struct {
	srv       *fakeserver.Server
	mu        sync.Mutex
	views     map[int64]*viewRecord
	taskFinal []string
	logs      string
	approval  func(body map[string]any) (fakeserver.Response, bool)
	omitIDs   bool
}

func newViewBackend(t *testing.T) *viewBackend {
	t.Helper()
	srv := fakeserver.New(t)
	b := &viewBackend{srv: srv, views: map[int64]*viewRecord{}, taskFinal: []string{fakeserver.StatusCompleted}}
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})

	srv.HandleREST(http.MethodPost, "/api/v1/views/create", b.create)
	srv.HandleREST(http.MethodPut, "/api/v1/views/{id}", b.update)
	srv.HandleREST(http.MethodDelete, "/api/v1/views/{id}", b.remove)
	srv.HandleGraphQL("viewById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["id"].(float64)
		v, ok := b.views[int64(id)]
		if !ok {
			return nil, nil
		}
		return viewJSON(v), nil
	})
	srv.HandleGraphQL("viewsPaginated", b.page)
	return b
}

func viewJSON(v *viewRecord) map[string]any {
	out := map[string]any{
		"id": v.ID, "name": v.Name, "description": v.Description, "priority": v.Priority,
		"zoneCount": v.Zones, "serverGroupIds": v.Groups, "createdOn": "2026-01-01T00:00:00", "updatedOn": "2026-01-02T00:00:00",
		"matchRecursiveOnly": false, "aclDetails": nil,
	}
	if v.ACLID != nil {
		out["aclDetails"] = map[string]any{"id": v.ACLID}
	}
	for field, graphql := range optionFields {
		if value, ok := v.Options[field]; ok {
			out[graphql] = value
		}
	}
	return out
}

func (b *viewBackend) page(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pagination, _ := args["pagination"].(map[string]any)
	limit, offset := 100, 0
	if v, ok := pagination["limit"].(float64); ok {
		limit = int(v)
	}
	if v, ok := pagination["offset"].(float64); ok {
		offset = int(v)
	}
	filters, _ := pagination["filters"].([]any)
	var matched []*viewRecord
	for _, v := range b.views {
		if matchesFilters(v, filters) {
			matched = append(matched, v)
		}
	}
	slices.SortFunc(matched, func(a, c *viewRecord) int { return cmp.Compare(a.ID, c.ID) })
	end := min(offset+limit, len(matched))
	items := []any{}
	if offset < len(matched) {
		for _, v := range matched[offset:end] {
			items = append(items, viewJSON(v))
		}
	}
	return map[string]any{"items": items, "total": len(matched), "hasMore": end < len(matched)}, nil
}

func matchesFilters(v *viewRecord, filters []any) bool {
	for _, raw := range filters {
		f, _ := raw.(map[string]any)
		switch f["field"] {
		case "name":
			if f["operator"] == "EQ" && v.Name != f["value"] {
				return false
			}
		case "priority":
			if f["operator"] == "EQ" && strconv.FormatInt(v.Priority, 10) != fmt.Sprint(f["value"]) {
				return false
			}
		}
	}
	return true
}

func (b *viewBackend) tasks() fakeserver.TaskHandle {
	if b.logs != "" {
		return b.srv.StageTaskScript(b.logs, b.taskFinal...)
	}
	return b.srv.StageTasks(b.taskFinal...)
}

func groupIDs(raw any) []int64 {
	var out []int64
	list, _ := raw.([]any)
	for _, g := range list {
		if n, ok := g.(float64); ok {
			out = append(out, int64(n))
		}
	}
	return out
}

func (b *viewBackend) apply(v *viewRecord, body map[string]any) {
	if value, ok := body["description"]; ok {
		v.Description = value
	}
	if value, ok := body["priority"].(float64); ok {
		v.Priority = int64(value)
	}
	if value, ok := body["view_acl_id"]; ok {
		v.ACLID = value
	}
	for field := range optionFields {
		if value, ok := body[field]; ok {
			v.Options[field] = value
		}
	}
}

func (b *viewBackend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	if b.approval != nil {
		if resp, pending := b.approval(body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	name, _ := body["name"].(string)
	for _, existing := range b.views {
		if existing.Name == name {
			return fakeserver.Detail(http.StatusConflict, "View name already exists: "+name)
		}
	}
	v := &viewRecord{ID: b.srv.NextID(), Name: name, Priority: 100, Groups: groupIDs(body["server_group_ids"]), Options: map[string]any{}}
	b.apply(v, body)
	b.views[v.ID] = v
	if b.omitIDs {
		return fakeserver.JSON(http.StatusAccepted, map[string]any{"view_id": v.ID})
	}
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"view_id": v.ID}))
}

func (b *viewBackend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.views[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "View not found")
	}
	b.apply(v, r.Map())
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(nil))
}

func (b *viewBackend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.views[id]; !ok {
		return fakeserver.Detail(http.StatusNotFound, "View not found")
	}
	delete(b.views, id)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"status": "queued"}))
}

func (b *viewBackend) seed(name string, priority int64) *viewRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := &viewRecord{ID: b.srv.NextID(), Name: name, Priority: priority, Groups: []int64{1}, Options: map[string]any{}}
	b.views[v.ID] = v
	return v
}

type viewProvider struct {
	*omniprovider.OmniDDIProvider
}

func (viewProvider) Resources(context.Context) []func() fwresource.Resource { return nil }

func (viewProvider) DataSources(context.Context) []func() datasource.DataSource {
	return view.DataSources()
}

func fastPolling(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func viewFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastPolling)().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = viewProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

func providerBlock(b *viewBackend) string {
	return acctest.ProviderConfigWith(b.srv.URL(), `  default_tags = { "env" = "prod" }
`)
}

func idOf(v *viewRecord) string { return strconv.FormatInt(v.ID, 10) }
