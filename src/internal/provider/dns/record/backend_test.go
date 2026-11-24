package record_test

import (
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
)

const (
	testZoneID   = int64(7)
	testZoneName = "example.com"
	testViewName = "internal"
	testViewID   = int64(3)
	testZoneTTL  = int64(3600)
)

var perValueFlagTypes = map[string]bool{
	"A": true, "AAAA": true, "TXT": true, "SPF": true, "NS": true, "PTR": true, "MX": true, "SRV": true,
	"SSHFP": true, "TLSA": true, "DS": true, "NAPTR": true, "URI": true, "AFSDB": true, "HINFO": true, "ANAME": true,
}

type fakeRecord struct {
	ID          int64
	ZoneID      int64
	Name        string
	Type        string
	TTL         int64
	Values      map[string]any
	Configs     map[string]any
	AutoCreated bool
	Inactive    bool
}

type recordBackend struct {
	srv       *fakeserver.Server
	mu        sync.Mutex
	records   map[int64]*fakeRecord
	taskFinal []string
	logs      string
	approval  func(body map[string]any) (fakeserver.Response, bool)
	lastBody  map[string]any
	lastPage  map[string]any
}

func newRecordBackend(t *testing.T) *recordBackend {
	t.Helper()
	srv := fakeserver.New(t)
	b := &recordBackend{srv: srv, records: map[int64]*fakeRecord{}, taskFinal: []string{fakeserver.StatusCompleted}}
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})

	srv.HandleREST(http.MethodPost, "/api/v1/records/", b.create)
	srv.HandleREST(http.MethodPut, "/api/v1/records/{id}", b.update)
	srv.HandleREST(http.MethodDelete, "/api/v1/records/{id}", b.remove)
	srv.HandleGraphQL("recordById", b.recordByID)
	srv.HandleGraphQL("recordsPaginated", b.recordsPaginated)
	srv.HandleGraphQL("zonesPaginated", b.zonesPaginated)
	return b
}

func (b *recordBackend) tasks() fakeserver.TaskHandle {
	if b.logs != "" {
		return b.srv.StageTaskScript(b.logs, b.taskFinal...)
	}
	return b.srv.StageTasks(b.taskFinal...)
}

func (b *recordBackend) seed(rec fakeRecord) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec.ID = b.srv.NextID()
	if rec.ZoneID == 0 {
		rec.ZoneID = testZoneID
	}
	if rec.TTL == 0 {
		rec.TTL = testZoneTTL
	}
	b.records[rec.ID] = &rec
	return rec.ID
}

func (b *recordBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.records)
}

func (b *recordBackend) find(name, recordType string) *fakeRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rec := range b.records {
		if rec.Name == name && rec.Type == recordType {
			copied := *rec
			return &copied
		}
	}
	return nil
}

func (b *recordBackend) lastRequestBody() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastBody
}

func isScalar(value any) bool {
	switch value.(type) {
	case string, float64, int, int64:
		return true
	}
	return false
}

func allScalars(items []any) bool {
	for _, item := range items {
		if !isScalar(item) {
			return false
		}
	}
	return true
}

func metaFromFlags(flags []any, prior []any, count int) []any {
	meta := make([]any, count)
	for i := range meta {
		enabled := true
		switch {
		case flags != nil && i < len(flags):
			enabled, _ = flags[i].(bool)
		case flags == nil && i < len(prior):
			if entry, ok := prior[i].(map[string]any); ok {
				enabled, _ = entry["enabled"].(bool)
			}
		}
		meta[i] = map[string]any{"enabled": enabled}
	}
	return meta
}

func (b *recordBackend) applyData(rec *fakeRecord, body map[string]any, creating bool) {
	data, hasData := body["data"]
	flags, _ := body["enabled_flags"].([]any)
	var priorMeta []any
	if rec.Configs != nil {
		priorMeta, _ = rec.Configs["data_meta"].([]any)
	}
	if rec.Values != nil {
		priorMeta, _ = rec.Values["value_meta"].([]any)
	}
	if hasData {
		b.storeData(rec, data, creating)
	}
	switch {
	case rec.Configs != nil && rec.Configs["data"] != nil:
		count := 1
		if list, ok := rec.Configs["data"].([]any); ok {
			count = len(list)
		}
		rec.Configs["data_meta"] = metaFromFlags(flags, priorMeta, count)
	case rec.Values != nil:
		list, _ := rec.Values["values"].([]any)
		rec.Values["value_meta"] = metaFromFlags(flags, priorMeta, len(list))
	}
}

func (b *recordBackend) storeData(rec *fakeRecord, data any, creating bool) {
	switch typed := data.(type) {
	case []any:
		if allScalars(typed) {
			rec.Values, rec.Configs = map[string]any{"values": typed}, nil
			return
		}
		rec.Values, rec.Configs = nil, map[string]any{"data": typed}
	case map[string]any:
		merged := map[string]any{}
		if existing, ok := rec.configData().(map[string]any); ok && !creating {
			maps.Copy(merged, existing)
		}
		maps.Copy(merged, typed)
		rec.Values, rec.Configs = nil, map[string]any{"data": merged}
	default:
		rec.Values, rec.Configs = map[string]any{"values": []any{typed}}, nil
	}
}

func (r *fakeRecord) configData() any {
	if r.Configs == nil {
		return nil
	}
	return r.Configs["data"]
}

func (b *recordBackend) create(r *fakeserver.Request) fakeserver.Response {
	body := r.Map()
	b.mu.Lock()
	b.lastBody = body
	zoneID := int64(body["zone_id"].(float64))
	name, _ := body["name"].(string)
	name = strings.ToLower(name)
	recordType, _ := body["type"].(string)
	conflict := b.conflict(zoneID, name, recordType)
	b.mu.Unlock()
	if conflict != nil {
		return *conflict
	}
	if b.approval != nil {
		if resp, pending := b.approval(body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ttl := testZoneTTL
	if value, ok := body["ttl"].(float64); ok && value > 0 {
		ttl = int64(value)
	}
	rec := &fakeRecord{ID: b.srv.NextID(), ZoneID: zoneID, Name: name, Type: recordType, TTL: ttl}
	b.applyData(rec, body, true)
	b.records[rec.ID] = rec
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"zone_id": zoneID, "record_id": rec.ID}))
}

const cnameCoexistDetail = "CNAME records cannot coexist with other record types for the same name"

func inactiveDetail(recordType, name, presence string) string {
	return "An inactive " + recordType + " record named '" + name + "' " + presence +
		" in the zone. Removed from the live zone. Reactivate it from the zone's Records tab (Status: Inactive)."
}

// conflict mirrors the backend: duplicate zone+name+type first, then CNAME exclusivity; active matches win over inactive ones.
func (b *recordBackend) conflict(zoneID int64, name, recordType string) *fakeserver.Response {
	var sameType, cnameClash *fakeRecord
	for _, existing := range b.records {
		if existing.ZoneID != zoneID || existing.Name != name {
			continue
		}
		switch {
		case existing.Type == recordType:
			sameType = preferActive(sameType, existing)
		case recordType == "CNAME" || existing.Type == "CNAME":
			cnameClash = preferActive(cnameClash, existing)
		}
	}
	var resp fakeserver.Response
	switch {
	case sameType != nil && sameType.Inactive:
		resp = fakeserver.Detail(http.StatusBadRequest, inactiveDetail(recordType, name, "already exists"))
	case sameType != nil:
		resp = fakeserver.Detail(http.StatusBadRequest, "Record with this name and type already exists in the zone")
	case cnameClash != nil && cnameClash.Inactive:
		resp = fakeserver.Detail(http.StatusBadRequest, cnameCoexistDetail+". "+inactiveDetail(cnameClash.Type, name, "is present"))
	case cnameClash != nil && recordType == "CNAME":
		resp = fakeserver.Detail(http.StatusBadRequest, cnameCoexistDetail)
	case cnameClash != nil:
		resp = fakeserver.Detail(http.StatusBadRequest, "A CNAME record already exists for this name")
	default:
		return nil
	}
	return &resp
}

func preferActive(current, candidate *fakeRecord) *fakeRecord {
	if current == nil || (current.Inactive && !candidate.Inactive) {
		return candidate
	}
	return current
}

func (b *recordBackend) update(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	body := r.Map()
	b.mu.Lock()
	b.lastBody = body
	b.mu.Unlock()
	if b.approval != nil {
		if resp, pending := b.approval(body); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.records[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Record not found")
	}
	if active, has := body["is_active"]; has {
		if active != true || len(body) != 1 {
			return fakeserver.Detail(http.StatusBadRequest, "Records cannot be deactivated through an update")
		}
		rec.Inactive = false
		return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"record_id": id}))
	}
	if rec.Inactive {
		return fakeserver.Detail(http.StatusBadRequest, "This record is inactive and cannot be edited. Reactivate it from the Records tab first.")
	}
	if flags, has := body["enabled_flags"]; has && flags != nil && rec.AutoCreated {
		return fakeserver.Detail(http.StatusBadRequest, "Cannot toggle values on system-generated records")
	}
	if name, ok := body["name"].(string); ok {
		rec.Name = strings.ToLower(name)
	}
	if ttl, ok := body["ttl"].(float64); ok {
		rec.TTL = int64(ttl)
	}
	b.applyData(rec, body, false)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"record_id": id}))
}

func (b *recordBackend) remove(r *fakeserver.Request) fakeserver.Response {
	id, _ := strconv.ParseInt(r.Vars["id"], 10, 64)
	if b.approval != nil {
		if resp, pending := b.approval(map[string]any{"record_id": float64(id)}); pending {
			return resp
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.records[id]
	if !ok {
		return fakeserver.Detail(http.StatusNotFound, "Record not found")
	}
	if rec.AutoCreated {
		return fakeserver.Detail(http.StatusBadRequest, "Auto-created records (synced from DNS server) cannot be deleted")
	}
	delete(b.records, id)
	return fakeserver.JSON(http.StatusAccepted, b.tasks().Merge(map[string]any{"record_id": id}))
}

func (r *fakeRecord) gqlData() any {
	if r.Values != nil {
		return r.Values
	}
	return r.configData()
}

func (r *fakeRecord) gqlValues() any {
	if !perValueFlagTypes[r.Type] {
		return nil
	}
	var tokens, meta []any
	if r.configData() != nil {
		tokens, _ = r.configData().([]any)
		if tokens == nil {
			tokens = []any{r.configData()}
		}
		meta, _ = r.Configs["data_meta"].([]any)
	} else if r.Values != nil {
		tokens, _ = r.Values["values"].([]any)
		meta, _ = r.Values["value_meta"].([]any)
	}
	states := make([]any, len(tokens))
	for i, token := range tokens {
		enabled := true
		if i < len(meta) {
			if entry, ok := meta[i].(map[string]any); ok {
				enabled, _ = entry["enabled"].(bool)
			}
		}
		states[i] = map[string]any{"value": token, "enabled": enabled, "index": i}
	}
	return states
}

func (r *fakeRecord) gql() map[string]any {
	return map[string]any{
		"id": r.ID, "zoneId": r.ZoneID, "name": r.Name, "type": r.Type, "ttl": r.TTL, "effectiveTtl": r.TTL,
		"data": r.gqlData(), "zoneName": testZoneName, "viewId": testViewID, "viewName": testViewName,
		"isActive": !r.Inactive, "isAutoCreated": r.AutoCreated, "createdOn": "2026-01-01T00:00:00",
		"updatedOn": "2026-01-02T00:00:00", "recordSetOrdering": nil, "values": r.gqlValues(),
	}
}

func (b *recordBackend) recordByID(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, _ := args["id"].(float64)
	rec, ok := b.records[int64(id)]
	if !ok {
		return nil, nil
	}
	return rec.gql(), nil
}

func filterMatches(rec *fakeRecord, filter map[string]any) bool {
	if filter["field"] == "isActive" {
		return filter["value"] == !rec.Inactive
	}
	if filter["field"] != "name" {
		return true
	}
	switch filter["operator"] {
	case "EQ":
		return strings.EqualFold(rec.Name, filter["value"].(string))
	case "IS_NULL":
		return rec.Name == ""
	case "CONTAINS":
		return strings.Contains(rec.Name, filter["value"].(string))
	}
	return true
}

func hasActiveFilter(filters []any) bool {
	for _, raw := range filters {
		if filter, _ := raw.(map[string]any); filter["field"] == "isActive" {
			return true
		}
	}
	return false
}

func (b *recordBackend) recordsPaginated(args map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	zoneID, _ := args["zoneId"].(float64)
	recordType, _ := args["recordType"].(string)
	pagination, _ := args["pagination"].(map[string]any)
	b.lastPage = pagination
	filters, _ := pagination["filters"].([]any)
	if !hasActiveFilter(filters) {
		filters = append(append([]any{}, filters...), map[string]any{"field": "isActive", "operator": "EQ", "value": true})
	}
	items := []any{}
	for _, rec := range b.records {
		if rec.ZoneID != int64(zoneID) || (recordType != "" && rec.Type != recordType) {
			continue
		}
		matched := true
		for _, raw := range filters {
			filter, _ := raw.(map[string]any)
			matched = matched && filterMatches(rec, filter)
		}
		if matched {
			items = append(items, rec.gql())
		}
	}
	return map[string]any{"items": items, "total": len(items), "hasMore": false}, nil
}

func (b *recordBackend) zonesPaginated(args map[string]any) (any, error) {
	pagination, _ := args["pagination"].(map[string]any)
	filters, _ := pagination["filters"].([]any)
	for _, raw := range filters {
		filter, _ := raw.(map[string]any)
		if filter["field"] == "name" && !strings.EqualFold(filter["value"].(string), testZoneName) {
			return map[string]any{"items": []any{}}, nil
		}
	}
	return map[string]any{"items": []any{map[string]any{"id": testZoneID, "name": testZoneName, "viewName": testViewName}}}, nil
}

func (b *recordBackend) deactivate(name, recordType string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, rec := range b.records {
		if rec.Name == name && rec.Type == recordType {
			rec.Inactive = true
		}
	}
}
