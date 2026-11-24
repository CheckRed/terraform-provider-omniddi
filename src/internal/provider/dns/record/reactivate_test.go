package record

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func TestIsInactiveConflictOnlyMatchesTheSameRecordType(t *testing.T) {
	inactiveA := "An inactive A record named 'www' already exists in the zone. Removed from the live zone. Reactivate it from the zone's Records tab (Status: Inactive)."
	cnameBlocked := "CNAME records cannot coexist with other record types for the same name. An inactive A record named 'www' is present in the zone. Removed from the live zone. Reactivate it from the zone's Records tab (Status: Inactive)."
	cases := []struct {
		name       string
		err        error
		recordType string
		want       bool
	}{
		{"same type", &client.APIError{StatusCode: http.StatusBadRequest, Detail: inactiveA}, "A", true},
		{"wrapped", fmt.Errorf("create: %w", &client.APIError{StatusCode: http.StatusBadRequest, Detail: inactiveA}), "A", true},
		{"other type", &client.APIError{StatusCode: http.StatusBadRequest, Detail: inactiveA}, "AAAA", false},
		{"cname blocked by inactive record", &client.APIError{StatusCode: http.StatusBadRequest, Detail: cnameBlocked}, "CNAME", false},
		{"active duplicate", &client.APIError{StatusCode: http.StatusBadRequest, Detail: "Record with this name and type already exists in the zone"}, "A", false},
		{"not a bad request", &client.APIError{StatusCode: http.StatusConflict, Detail: inactiveA}, "A", false},
		{"plain error", errors.New(inactiveA), "A", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInactiveConflict(tc.err, tc.recordType); got != tc.want {
				t.Fatalf("isInactiveConflict = %v, want %v", got, tc.want)
			}
		})
	}
}
