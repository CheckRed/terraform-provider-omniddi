package dhcptest

import (
	"fmt"
	"strings"
)

func PaginationFilters(args map[string]any) []map[string]any {
	pagination, _ := args["pagination"].(map[string]any)
	raw, _ := pagination["filters"].([]any)
	filters := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if f, ok := item.(map[string]any); ok {
			filters = append(filters, f)
		}
	}
	return filters
}

func MatchesFilters(args map[string]any, row map[string]any) bool {
	for _, f := range PaginationFilters(args) {
		field, _ := f["field"].(string)
		if strings.HasPrefix(field, "tag.") {
			continue
		}
		if f["operator"] != "EQ" {
			return false
		}
		if fmt.Sprint(row[field]) != fmt.Sprint(f["value"]) {
			return false
		}
	}
	return true
}

func Page(rows []map[string]any) map[string]any {
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, row)
	}
	return map[string]any{"items": items, "total": len(items), "hasMore": false}
}
