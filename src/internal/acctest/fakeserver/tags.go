package fakeserver

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
)

type TagDefinition struct {
	ID         int64
	Level      string
	Name       string
	DataType   string
	MultiValue bool
	AppliesTo  []string
}

type tagKey struct {
	objectType string
	objectID   int64
	level      string
	definition int64
}

type tagValue struct {
	values []string
	origin string
}

type tagState struct {
	mu          sync.Mutex
	definitions []TagDefinition
	values      map[tagKey]tagValue
}

func (s *Server) initTags() {
	s.tags.values = map[tagKey]tagValue{}
	s.HandleREST(http.MethodPut, "/api/v1/tags/values", s.putTagValues)
	s.HandleGraphQL("tagDefinitionsForType", func(args map[string]any) (any, error) {
		objectType := fmt.Sprint(args["objectType"])
		s.tags.mu.Lock()
		defer s.tags.mu.Unlock()
		out := []map[string]any{}
		for _, d := range s.tags.definitions {
			if len(d.AppliesTo) == 0 || slices.Contains(d.AppliesTo, objectType) {
				out = append(out, map[string]any{"id": d.ID, "level": d.Level, "name": d.Name, "dataType": d.DataType, "multiValue": d.MultiValue})
			}
		}
		return out, nil
	})
	s.HandleGraphQL("tagValues", func(args map[string]any) (any, error) {
		objectType := fmt.Sprint(args["objectType"])
		objectID, _ := toInt64(args["objectId"])
		return s.tagValuesFor(objectType, objectID), nil
	})
}

func (s *Server) AddTagDefinition(d TagDefinition) {
	s.tags.mu.Lock()
	defer s.tags.mu.Unlock()
	if d.Level == "" {
		d.Level = "SPACE"
	}
	if d.DataType == "" {
		d.DataType = "STRING"
	}
	s.tags.definitions = append(s.tags.definitions, d)
}

func (s *Server) SetInheritedTag(objectType string, objectID int64, definitionID int64, values ...string) {
	s.tags.mu.Lock()
	defer s.tags.mu.Unlock()
	level := "SPACE"
	for _, d := range s.tags.definitions {
		if d.ID == definitionID {
			level = d.Level
		}
	}
	s.tags.values[tagKey{objectType, objectID, level, definitionID}] = tagValue{values: values, origin: "INHERITED"}
}

func (s *Server) TagValues(objectType string, objectID int64) map[string][]string {
	out := map[string][]string{}
	for _, item := range s.tagValuesFor(objectType, objectID) {
		if item["origin"] == "OWN" {
			out[item["name"].(string)] = item["values"].([]string)
		}
	}
	return out
}

func (s *Server) putTagValues(r *Request) Response {
	var body struct {
		ObjectType   string   `json:"object_type"`
		ObjectID     int64    `json:"object_id"`
		Level        string   `json:"level"`
		DefinitionID int64    `json:"definition_id"`
		Values       []string `json:"values"`
	}
	if err := r.JSON(&body); err != nil {
		return Detail(http.StatusBadRequest, "Field required")
	}
	s.tags.mu.Lock()
	defer s.tags.mu.Unlock()
	known := slices.ContainsFunc(s.tags.definitions, func(d TagDefinition) bool {
		return d.ID == body.DefinitionID && d.Level == body.Level
	})
	if !known {
		return Detail(http.StatusNotFound, "Tag definition not found")
	}
	key := tagKey{body.ObjectType, body.ObjectID, body.Level, body.DefinitionID}
	if len(body.Values) == 0 {
		delete(s.tags.values, key)
	} else {
		s.tags.values[key] = tagValue{values: body.Values, origin: "OWN"}
	}
	return JSON(http.StatusOK, map[string]any{"values": body.Values})
}

func (s *Server) tagValuesFor(objectType string, objectID int64) []map[string]any {
	s.tags.mu.Lock()
	defer s.tags.mu.Unlock()
	out := []map[string]any{}
	for key, val := range s.tags.values {
		if key.objectType != objectType || key.objectID != objectID {
			continue
		}
		for _, d := range s.tags.definitions {
			if d.ID == key.definition && d.Level == key.level {
				out = append(out, map[string]any{
					"definitionId": d.ID, "level": d.Level, "name": d.Name, "dataType": d.DataType,
					"multiValue": d.MultiValue, "isRequired": false, "values": val.values, "origin": val.origin,
				})
			}
		}
	}
	return out
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}
