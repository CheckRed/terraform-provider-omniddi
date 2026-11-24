package view

import (
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, viewByIDQuery, viewsQuery)
}
