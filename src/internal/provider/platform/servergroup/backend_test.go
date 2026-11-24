package servergroup_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	omniprovider "github.com/CheckRed/terraform-provider-omniddi/internal/provider"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/platform/servergroup"
)

type member struct {
	ID   int64
	Role string
}

type group struct {
	ID           int64
	Name         string
	Type         string
	Description  *string
	DNSVendorID  *int64
	DHCPVendorID *int64
	ScopeID      *int64
	Members      []member
}

type backend struct {
	srv      *fakeserver.Server
	mu       sync.Mutex
	groups   map[int64]*group
	approval func(body map[string]any) (fakeserver.Response, bool)
}

type testProvider struct {
	*omniprovider.OmniDDIProvider
}

func (testProvider) Resources(context.Context) []func() resource.Resource { return nil }

func (testProvider) DataSources(context.Context) []func() datasource.DataSource {
	return servergroup.DataSources()
}

func fastClient(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func factories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastClient)().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = testProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	srv := fakeserver.New(t)
	b := &backend{srv: srv, groups: map[int64]*group{}}
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})

	srv.HandleREST(http.MethodPost, "/api/v1/server-groups/", b.create)
	srv.HandleREST(http.MethodPut, "/api/v1/server-groups/{id}", b.update)
	srv.HandleREST(http.MethodDelete, "/api/v1/server-groups/{id}", b.remove)
	srv.HandleGraphQL("serverGroupById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["id"].(float64)
		g, ok := b.groups[int64(id)]
		if !ok {
			return nil, nil
		}
		out := groupJSON(g)
		servers := []map[string]any{}
		for _, m := range g.Members {
			servers = append(servers, map[string]any{"id": m.ID, "sg_role": titleCase(m.Role)})
		}
		out["serversList"] = servers
		return out, nil
	})
	srv.HandleGraphQL("serverGroupsPaginated", b.paginated)
	return b
}

func titleCase(role string) any {
	if role == "" {
		return nil
	}
	words := strings.Fields(role)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func groupJSON(g *group) map[string]any {
	out := map[string]any{
		"id": g.ID, "name": g.Name, "description": g.Description, "type": g.Type,
		"dnsVendorId": g.DNSVendorID, "dhcpVendorId": g.DHCPVendorID, "protocol": nil, "protocolId": nil,
	}
	if g.ScopeID != nil {
		out["protocolId"] = strconv.FormatInt(*g.ScopeID, 10)
		out["protocol"] = "IPv4"
	}
	return out
}

func (b *backend) paginated(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	wantType, _ := args["type"].(string)
	wantName, limit, offset := "", 100, 0
	if pagination, ok := args["pagination"].(map[string]any); ok {
		filters, _ := pagination["filters"].([]any)
		for _, raw := range filters {
			if f, _ := raw.(map[string]any); f["field"] == "name" {
				wantName, _ = f["value"].(string)
			}
		}
		if v, ok := pagination["limit"].(float64); ok {
			limit = int(v)
		}
		if v, ok := pagination["offset"].(float64); ok {
			offset = int(v)
		}
	}
	matched := []*group{}
	for _, g := range b.groups {
		if (wantType == "" || wantType == g.Type) && (wantName == "" || strings.EqualFold(wantName, g.Name)) {
			matched = append(matched, g)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	end := min(offset+limit, len(matched))
	items := []map[string]any{}
	for _, g := range matched[min(offset, len(matched)):end] {
		items = append(items, groupJSON(g))
	}
	return map[string]any{"items": items, "total": len(matched), "hasMore": end < len(matched)}, nil
}

func (b *backend) seed(name, groupType string, members ...member) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := &group{ID: b.srv.NextID(), Name: name, Type: groupType, Members: members}
	b.groups[g.ID] = g
	return g.ID
}

func parseMembers(raw any) []member {
	var out []member
	list, _ := raw.([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		id, _ := entry["server_id"].(float64)
		role, _ := entry["role"].(string)
		out = append(out, member{ID: int64(id), Role: role})
	}
	return out
}

func intPtr(v any) *int64 {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	i := int64(f)
	return &i
}

func (b *backend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	if b.approval != nil {
		if resp, pending := b.approval(body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	g := &group{ID: b.srv.NextID(), Type: fmt.Sprint(body["type"]), Name: fmt.Sprint(body["name"])}
	for _, other := range b.groups {
		if other.Name == g.Name && other.Type == g.Type {
			return fakeserver.Detail(http.StatusBadRequest, "Server group name must be unique per type")
		}
	}
	if d, ok := body["description"].(string); ok {
		g.Description = &d
	}
	one := int64(1)
	if g.Type == "dns" {
		g.DNSVendorID = &one
	} else {
		g.DHCPVendorID = &one
	}
	if v := intPtr(body["dns_vendor_id"]); v != nil {
		g.DNSVendorID = v
	}
	if v := intPtr(body["dhcp_vendor_id"]); v != nil {
		g.DHCPVendorID = v
	}
	g.ScopeID = intPtr(body["scope_id"])
	g.Members = parseMembers(body["servers"])
	b.groups[g.ID] = g
	return fakeserver.JSON(http.StatusCreated, groupJSON(g))
}

func (b *backend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	body := r.Map()
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.groups[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Server group not found")
	}
	if name, ok := body["name"].(string); ok {
		g.Name = name
	}
	if d, ok := body["description"].(string); ok {
		g.Description = &d
	}
	if v := intPtr(body["dns_vendor_id"]); v != nil {
		g.DNSVendorID = v
	}
	if v := intPtr(body["dhcp_vendor_id"]); v != nil {
		g.DHCPVendorID = v
	}
	if v := intPtr(body["scope_id"]); v != nil {
		g.ScopeID = v
	}
	if _, ok := body["servers"]; ok {
		g.Members = parseMembers(body["servers"])
	}
	return fakeserver.JSON(http.StatusOK, groupJSON(g))
}

func (b *backend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.groups[id]; !ok {
		return fakeserver.Detail(http.StatusNotFound, "Server group not found")
	}
	delete(b.groups, id)
	return fakeserver.NoContent()
}

func fakeTag(id int64, name string) fakeserver.TagDefinition {
	return fakeserver.TagDefinition{ID: id, Name: name}
}
