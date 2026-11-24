package omniddigql

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

const SchemaEnv = "OMNIDDI_GRAPHQL_SCHEMA"

const federationDirectives = `
directive @key(fields: String!, resolvable: Boolean = true) repeatable on OBJECT | INTERFACE
directive @external on FIELD_DEFINITION | OBJECT
directive @requires(fields: String!) on FIELD_DEFINITION
directive @provides(fields: String!) on FIELD_DEFINITION
directive @extends on OBJECT | INTERFACE
directive @shareable repeatable on OBJECT | FIELD_DEFINITION
directive @inaccessible on FIELD_DEFINITION | OBJECT | INTERFACE | UNION | ARGUMENT_DEFINITION | SCALAR | ENUM | ENUM_VALUE | INPUT_OBJECT | INPUT_FIELD_DEFINITION
directive @override(from: String!) on FIELD_DEFINITION
directive @tag(name: String!) repeatable on FIELD_DEFINITION | OBJECT | INTERFACE | UNION | ARGUMENT_DEFINITION | SCALAR | ENUM | ENUM_VALUE | INPUT_OBJECT | INPUT_FIELD_DEFINITION
`

type loaded struct {
	schema *ast.Schema
	err    error
}

var (
	cacheMu sync.Mutex
	cache   = map[string]loaded{}
)

func Schema() (*ast.Schema, error) {
	path := os.Getenv(SchemaEnv)
	if path == "" {
		return nil, nil
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if hit, ok := cache[path]; ok {
		return hit.schema, hit.err
	}
	var entry loaded
	sdl, err := os.ReadFile(path) //nolint:gosec // path is the developer-set schema file in SchemaEnv
	if err != nil {
		entry.err = fmt.Errorf("read %s: %w", SchemaEnv, err)
	} else {
		entry.schema, entry.err = gqlparser.LoadSchema(&ast.Source{Name: path, Input: federationDirectives + string(sdl)})
	}
	cache[path] = entry
	return entry.schema, entry.err
}

func Enabled() bool { return os.Getenv(SchemaEnv) != "" }

func Validate(query string) error {
	s, err := Schema()
	if err != nil || s == nil {
		return err
	}
	if _, errs := gqlparser.LoadQueryWithRules(s, query, nil); len(errs) > 0 {
		return errs
	}
	return nil
}

func RequireValid(t testing.TB, queries ...string) {
	t.Helper()
	if !Enabled() {
		t.Skipf("%s is not set; skipping GraphQL schema validation", SchemaEnv)
	}
	if _, err := Schema(); err != nil {
		t.Fatalf("load GraphQL schema: %v", err)
	}
	for _, query := range queries {
		if err := Validate(query); err != nil {
			t.Errorf("invalid GraphQL operation: %v\n%s", err, query)
		}
	}
}
