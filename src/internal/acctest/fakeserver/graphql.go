package fakeserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
)

const (
	graphQLRoute   = "/graphql"
	CodeNotFound   = "RESOURCE_NOT_FOUND"
	CodeForbidden  = "FORBIDDEN"
	codeBadRequest = "BAD_USER_INPUT"
)

type GraphQLHandler func(args map[string]any) (any, error)

type GraphQLError struct {
	Message string
	Code    string
}

func (e *GraphQLError) Error() string { return e.Message }

func NotFound(message string) error { return &GraphQLError{Message: message, Code: CodeNotFound} }

func (s *Server) HandleGraphQL(topLevelField string, fn GraphQLHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gql[topLevelField] = fn
}

func (s *Server) serveGraphQL(req *Request) Response {
	if denied := s.authenticate(req); denied != nil {
		return *denied
	}
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := req.JSON(&body); err != nil {
		return Detail(http.StatusBadRequest, "invalid GraphQL request body")
	}
	if err := s.validateQuery(body.Query); err != nil {
		s.t.Errorf("fakeserver: GraphQL operation does not match the backend schema: %v\n%s", err, body.Query)
		return gqlErrors(&GraphQLError{Message: err.Error(), Code: codeBadRequest}, nil)
	}
	doc, perr := parser.ParseQuery(&ast.Source{Name: "request", Input: body.Query})
	if perr != nil {
		return gqlErrors(&GraphQLError{Message: perr.Error(), Code: codeBadRequest}, nil)
	}

	data := map[string]any{}
	var errs []map[string]any
	for _, op := range doc.Operations {
		for _, sel := range op.SelectionSet {
			field, ok := sel.(*ast.Field)
			if !ok {
				continue
			}
			key := field.Alias
			if key == "" {
				key = field.Name
			}
			value, err := s.resolveField(field, body.Variables)
			if err != nil {
				data[key] = nil
				errs = append(errs, gqlErrorEntry(err, key))
				continue
			}
			data[key] = value
		}
	}
	out := map[string]any{"data": data}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	return JSON(http.StatusOK, out)
}

func (s *Server) resolveField(field *ast.Field, vars map[string]any) (any, error) {
	s.mu.Lock()
	handler, ok := s.gql[field.Name]
	s.mu.Unlock()
	if !ok {
		s.t.Logf("fakeserver: no GraphQL handler for field %q", field.Name)
		return nil, &GraphQLError{Message: "no fake handler for GraphQL field " + field.Name, Code: codeBadRequest}
	}
	return handler(argumentMap(field, vars))
}

func gqlErrorEntry(err error, field string) map[string]any {
	code := ""
	message := err.Error()
	var ge *GraphQLError
	if errors.As(err, &ge) {
		code = ge.Code
	}
	entry := map[string]any{"message": message, "path": []any{field}}
	if code != "" {
		entry["extensions"] = map[string]any{"code": code}
	}
	return entry
}

func gqlErrors(err error, data any) Response {
	raw, _ := json.Marshal(map[string]any{"data": data, "errors": []map[string]any{gqlErrorEntry(err, "")}})
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return JSON(http.StatusOK, body)
}

func argumentMap(field *ast.Field, vars map[string]any) map[string]any {
	args := make(map[string]any, len(field.Arguments))
	for _, arg := range field.Arguments {
		value, err := arg.Value.Value(vars)
		if n, isInt := value.(int64); isInt {
			value = float64(n)
		}
		if err == nil {
			args[arg.Name] = value
		}
	}
	return args
}

func (s *Server) validateQuery(query string) error {
	if s.noCheck {
		return nil
	}
	return omniddigql.Validate(query)
}
