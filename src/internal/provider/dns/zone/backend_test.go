package zone_test

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
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
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/dns/zone"
)

const seededViewName = "internal"

type zoneRecord struct {
	ID          int64
	ViewID      int64
	Name        string
	Type        string
	Scope       string
	Config      map[string]any
	ACLID       any
	TsigZT      any
	TsigDDNS    any
	TsigSync    any
	BUs         []int64
	UserRecords int64
}

type zoneBackend struct {
	srv       *fakeserver.Server
	mu        sync.Mutex
	zones     map[int64]*zoneRecord
	taskFinal []string
	syncLogs  string
	logs      string
	gate      func(method string, body map[string]any) (fakeserver.Response, bool)
	legacy    bool
	viewID    int64
}

func newZoneBackend(t *testing.T) *zoneBackend {
	t.Helper()
	srv := fakeserver.New(t)
	b := &zoneBackend{srv: srv, zones: map[int64]*zoneRecord{}, taskFinal: []string{fakeserver.StatusCompleted}}
	b.viewID = srv.NextID()
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})

	srv.HandleREST(http.MethodPost, "/api/v1/zones/", b.create)
	srv.HandleREST(http.MethodPut, "/api/v1/zones/{id}", b.update)
	srv.HandleREST(http.MethodDelete, "/api/v1/zones/{id}", b.remove)
	srv.HandleGraphQL("zoneById", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["id"].(float64)
		z, ok := b.zones[int64(id)]
		if !ok {
			return nil, nil
		}
		return b.zoneJSON(z), nil
	})
	srv.HandleGraphQL("zonesPaginated", b.page)
	srv.HandleGraphQL("viewsPaginated", func(args map[string]any) (any, error) {
		pagination, _ := args["pagination"].(map[string]any)
		filters, _ := pagination["filters"].([]any)
		items := []any{}
		for _, raw := range filters {
			f, _ := raw.(map[string]any)
			if f["field"] == "name" && f["value"] == seededViewName {
				items = append(items, map[string]any{"id": b.viewID, "name": seededViewName})
			}
		}
		return map[string]any{"items": items, "total": len(items), "hasMore": false}, nil
	})
	srv.HandleGraphQL("recordsPaginated", func(args map[string]any) (any, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id, _ := args["zoneId"].(float64)
		if z, ok := b.zones[int64(id)]; ok {
			return map[string]any{"total": z.UserRecords}, nil
		}
		return map[string]any{"total": 0}, nil
	})
	return b
}

func (b *zoneBackend) zoneJSON(z *zoneRecord) map[string]any {
	cfg := z.Config
	pick := func(key string, fallback any) any {
		if v, ok := cfg[key]; ok {
			return v
		}
		return fallback
	}
	soa := map[string]any{
		"ttl": pick("ttl", 3600), "refresh": pick("refresh", 3600), "retry": pick("retry", 600),
		"expire": strconv.Itoa(int(toFloat(pick("expiry", 604800)))), "nttl": pick("minimum", 300),
		"serial": 2026010101, "primaryNameServer": pick("primary_name_server", nil),
	}
	units := []any{}
	for _, id := range z.BUs {
		units = append(units, map[string]any{"id": id})
	}
	var primary any
	if len(z.BUs) > 0 {
		primary = map[string]any{"id": z.BUs[0]}
	}
	return map[string]any{
		"id": z.ID, "name": z.Name, "zoneType": z.Type, "networkScope": z.Scope, "viewId": z.ViewID, "viewName": seededViewName,
		"recordsCount": z.UserRecords + 2, "configJson": cfg, "createdOn": "2026-01-01T00:00:00", "updatedOn": "2026-01-02T00:00:00",
		"soa": soa, "zoneTransferTsigKeyId": z.TsigZT, "dynamicDnsTsigKeyId": z.TsigDDNS, "secondarySyncTsigKeyId": z.TsigSync,
		"businessUnits": units, "primaryBusinessUnit": primary,
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

func (b *zoneBackend) page(args map[string]any) (any, error) {
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
	viewFilter, hasView := args["viewId"].(float64)
	filters, _ := pagination["filters"].([]any)
	var matched []*zoneRecord
	for _, z := range b.zones {
		if hasView && z.ViewID != int64(viewFilter) {
			continue
		}
		ok := true
		for _, raw := range filters {
			f, _ := raw.(map[string]any)
			switch f["field"] {
			case "name":
				ok = ok && z.Name == f["value"]
			case "zoneType":
				ok = ok && z.Type == f["value"]
			}
		}
		if ok {
			matched = append(matched, z)
		}
	}
	slices.SortFunc(matched, func(a, c *zoneRecord) int { return cmp.Compare(a.ID, c.ID) })
	end := min(offset+limit, len(matched))
	items := []any{}
	if offset < len(matched) {
		for _, z := range matched[offset:end] {
			items = append(items, b.zoneJSON(z))
		}
	}
	return map[string]any{"items": items, "total": len(matched), "hasMore": end < len(matched)}, nil
}

func (b *zoneBackend) tasks() fakeserver.TaskHandle {
	if b.logs != "" {
		return b.srv.StageTaskScript(b.logs, b.taskFinal...)
	}
	return b.srv.StageTasks(b.taskFinal...)
}

func reverseName(cidr string) string {
	if prefix, err := netip.ParsePrefix(cidr); err == nil && prefix.Addr().Is6() {
		return reverseNameV6(prefix)
	}
	address, bits, _ := strings.Cut(cidr, "/")
	octets := strings.Split(address, ".")
	n, _ := strconv.Atoi(bits)
	labels := []string{}
	for i := n/8 - 1; i >= 0; i-- {
		labels = append(labels, octets[i])
	}
	return strings.Join(append(labels, "in-addr", "arpa"), ".")
}

func (b *zoneBackend) apply(z *zoneRecord, body map[string]any) {
	for _, key := range []string{"ttl", "refresh", "retry", "expiry", "minimum", "email", "primary_name_server", "policy", "protocol", "cidr", "auto_create_ptr_records", "secure_sync"} {
		if v, ok := body[key]; ok && v != nil {
			z.Config[key] = v
		}
	}
	for flag, block := range map[string]string{"enable_dynamic_dns": "dynamic_dns", "enable_dnssec": "dnssec", "enable_zone_transfer": "zone_transfer"} {
		if v, ok := body[flag]; ok && v != nil {
			z.Config[block] = map[string]any{"enable": v}
		}
	}
	if raw, ok := body["primary_servers"].([]any); ok {
		servers := []any{}
		for _, item := range raw {
			entry, _ := item.(map[string]any)
			stored := map[string]any{"ip": entry["ip"], "port": entry["port"], "serverName": entry["ip"]}
			if entry["secure_sync"] == true {
				stored["secure_sync"] = true
				z.TsigSync = entry["tsig_key_id"]
			}
			servers = append(servers, stored)
		}
		z.Config["primary_servers"] = servers
	}
	if raw, ok := body["nameservers"].([]any); ok {
		servers := []any{}
		for _, ip := range raw {
			servers = append(servers, map[string]any{"ip": ip, "port": 53, "serverName": ip})
		}
		z.Config["nameservers"] = servers
	}
	if raw, ok := body["business_unit_ids"].([]any); ok {
		z.BUs = nil
		for _, id := range raw {
			z.BUs = append(z.BUs, int64(toFloat(id)))
		}
	}
	for key, target := range map[string]*any{"zone_acl_id": &z.ACLID, "zone_transfer_tsig_key_id": &z.TsigZT, "dynamic_dns_tsig_key_id": &z.TsigDDNS} {
		if v, ok := body[key]; ok {
			*target = v
		}
	}
	if v, ok := body["secondary_sync_tsig_key_id"]; ok {
		z.TsigSync = v
	}
}

func (b *zoneBackend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	if b.gate != nil {
		if resp, held := b.gate(http.MethodPost, body); held {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	name, _ := body["name"].(string)
	if name == "" {
		cidr, _ := body["cidr"].(string)
		name = reverseName(cidr)
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	viewID := int64(toFloat(body["view_id"]))
	for _, existing := range b.zones {
		if existing.Name == name && existing.ViewID == viewID {
			return fakeserver.Detail(http.StatusConflict, fmt.Sprintf("Zone '%s' already exists in view '%s'", name, seededViewName))
		}
	}
	zoneType, _ := body["zone_type"].(string)
	scope, _ := body["network_scope"].(string)
	z := &zoneRecord{ID: b.srv.NextID(), ViewID: viewID, Name: name, Type: zoneType, Scope: scope, Config: map[string]any{}, BUs: []int64{1}}
	b.apply(z, body)
	b.zones[z.ID] = z
	if b.legacy {
		return fakeserver.JSON(http.StatusCreated, map[string]any{"id": 999, "name": name, "group_id": 0, "config_json": map[string]any{}})
	}
	extra := map[string]any{"zone_id": z.ID, "zone_name": name}
	main, sync := b.tasks(), b.tasks()
	if b.syncLogs != "" {
		sync = b.srv.StageTaskScript(b.syncLogs, fakeserver.StatusIncomplete)
	}
	extra["sync_task_ids"], extra["sync_correlation_id"] = sync.TaskIDs, sync.CorrelationID
	return fakeserver.JSON(http.StatusAccepted, main.Merge(extra))
}

func (b *zoneBackend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	body := r.Map()
	if b.gate != nil {
		if resp, held := b.gate(http.MethodPut, body); held {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	z, ok := b.zones[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Zone not found")
	}
	b.apply(z, body)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"zone_id": id}))
}

func (b *zoneBackend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	if b.gate != nil {
		if resp, held := b.gate(http.MethodDelete, nil); held {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.zones[id]; !ok {
		return fakeserver.Detail(http.StatusNotFound, "Zone not found")
	}
	delete(b.zones, id)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"status": "queued"}))
}

func (b *zoneBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.zones)
}

func (b *zoneBackend) seed(name string, viewID int64) *zoneRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	z := &zoneRecord{ID: b.srv.NextID(), ViewID: viewID, Name: name, Type: "Primary", Scope: "Internal", Config: map[string]any{}, BUs: []int64{1}}
	b.zones[z.ID] = z
	return z
}

func (b *zoneBackend) first() *zoneRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, z := range b.zones {
		return z
	}
	return nil
}

func (b *zoneBackend) setUserRecords(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, z := range b.zones {
		z.UserRecords = n
	}
}

func (b *zoneBackend) wipe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.zones = map[int64]*zoneRecord{}
}

type zoneProvider struct {
	*omniprovider.OmniDDIProvider
}

func (zoneProvider) Resources(context.Context) []func() fwresource.Resource { return zone.Resources() }

func (zoneProvider) DataSources(context.Context) []func() datasource.DataSource {
	return zone.DataSources()
}

func fastPolling(cfg *client.Config) {
	cfg.TaskPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, EmptyGrace: 300 * time.Millisecond}
	cfg.ApprovalPoll = client.PollConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	cfg.RetryBackoff = client.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
}

func zoneFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	base, _ := omniprovider.NewWithClientTuning("test", fastPolling)().(*omniprovider.OmniDDIProvider)
	var p provider.Provider = zoneProvider{OmniDDIProvider: base}
	return acctest.ProviderFactoriesFor(p)
}

func providerBlock(b *zoneBackend) string {
	return acctest.ProviderConfigWith(b.srv.URL(), `  default_tags = { "env" = "prod" }
`)
}

func zoneConfig(b *zoneBackend, body string) string {
	return providerBlock(b) + fmt.Sprintf("\nresource \"omniddi_dns_zone\" \"test\" {\n  view_id = %d\n%s\n}\n", b.viewID, body)
}

func lastBody(b *zoneBackend, method string, id int64) map[string]any {
	reqs := b.srv.RequestsFor(method, "/api/v1/zones/"+strconv.FormatInt(id, 10))
	if len(reqs) == 0 {
		return nil
	}
	return (&reqs[len(reqs)-1]).Map()
}

func sortedKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func reverseNameV6(prefix netip.Prefix) string {
	raw := prefix.Masked().Addr().As16()
	nibbles := make([]string, 0, 32)
	for _, octet := range raw {
		nibbles = append(nibbles, strconv.FormatUint(uint64(octet>>4), 16), strconv.FormatUint(uint64(octet&0xf), 16))
	}
	nibbles = nibbles[:prefix.Bits()/4]
	slices.Reverse(nibbles)
	return strings.Join(append(nibbles, "ip6", "arpa"), ".")
}
