package omniddigql_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

const miniSchema = `
type Query { zoneById(id: Int!): Zone }
type Zone { id: Int! name: String! }
`

func TestValidateAgainstSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.graphql")
	if err := os.WriteFile(path, []byte(miniSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(omniddigql.SchemaEnv, path)

	if !omniddigql.Enabled() {
		t.Fatal("validation must be enabled when the schema env is set")
	}
	if err := omniddigql.Validate(`query { zoneById(id: 1) { id name } }`); err != nil {
		t.Errorf("valid query rejected: %v", err)
	}
	for name, q := range map[string]string{
		"unknown field":  `query { zoneById(id: 1) { id nope } }`,
		"missing arg":    `query { zoneById { id } }`,
		"unknown root":   `query { zones { id } }`,
		"syntax":         `query {`,
		"wrong arg type": `query { zoneById(id: "x") { id } }`,
	} {
		if err := omniddigql.Validate(q); err == nil {
			t.Errorf("%s: invalid query accepted", name)
		}
	}
}
