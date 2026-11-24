package client

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
)

const (
	InheritedTagKey = "@inherited"

	TagObjectACL            = "ACL"
	TagObjectDNSRecord      = "DNS_RECORD"
	TagObjectDNSServerGroup = "DNS_SERVER_GROUP"
	TagObjectDNSView        = "DNS_VIEW"
	TagObjectDNSZone        = "DNS_ZONE"
	TagObjectHostReserv     = "HOST_RESERVATION"
	TagObjectNetworkContain = "NETWORK_CONTAINER"
	TagObjectSharedNetwork  = "SHARED_NETWORK"
	TagObjectSubnet         = "SUBNET"

	tagOriginInherited = "INHERITED"
)

const (
	TagDefinitionsForTypeQuery = `query TagDefinitionsForType($objectType: TagObjectType!) {
  tagDefinitionsForType(objectType: $objectType) {
    id
    level
    name
    dataType
    multiValue
  }
}`

	TagValuesQuery = `query TagValues($objectType: TagObjectType!, $objectId: BigInt!) {
  tagValues(objectType: $objectType, objectId: $objectId) {
    definitionId
    level
    name
    multiValue
    values
    origin
  }
}`
)

type TagDefinition struct {
	ID         int64  `json:"id"`
	Level      string `json:"level"`
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	MultiValue bool   `json:"multiValue"`
}

type ObjectTag struct {
	DefinitionID int64    `json:"definitionId"`
	Level        string   `json:"level"`
	Name         string   `json:"name"`
	MultiValue   bool     `json:"multiValue"`
	Values       []string `json:"values"`
	Origin       string   `json:"origin"`
}

type UnknownTagsError struct {
	Names []string
}

func (e *UnknownTagsError) Error() string {
	return fmt.Sprintf("tag definition(s) not found: %s; create them in OmniDDI before using them", strings.Join(e.Names, ", "))
}

func (c *Client) TagDefinitions(ctx context.Context, objectType string) ([]TagDefinition, error) {
	var out struct {
		Definitions []TagDefinition `json:"tagDefinitionsForType"`
	}
	vars := map[string]any{"objectType": objectType}
	if err := c.Query(ctx, TagDefinitionsForTypeQuery, vars, &out); err != nil {
		return nil, err
	}
	return out.Definitions, nil
}

func (c *Client) GetTagValues(ctx context.Context, objectType string, objectID int64) ([]ObjectTag, error) {
	var out struct {
		Tags []ObjectTag `json:"tagValues"`
	}
	vars := map[string]any{"objectType": objectType, "objectId": objectID}
	if err := c.Query(ctx, TagValuesQuery, vars, &out); err != nil {
		return nil, err
	}
	own := make([]ObjectTag, 0, len(out.Tags))
	for _, tag := range out.Tags {
		if tag.Origin != tagOriginInherited {
			own = append(own, tag)
		}
	}
	return own, nil
}

func (c *Client) SetTagValues(ctx context.Context, objectType string, objectID int64, level string, definitionID int64, values []string) error {
	if values == nil {
		values = []string{}
	}
	body := map[string]any{
		"object_type":   objectType,
		"object_id":     objectID,
		"level":         level,
		"definition_id": definitionID,
		"values":        values,
	}
	_, err := c.Write(ctx, http.MethodPut, "/tags/values", body)
	return err
}

func (c *Client) ReadTags(ctx context.Context, objectType string, objectID int64) (map[string][]string, error) {
	tags, err := c.GetTagValues(ctx, objectType, objectID)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(tags))
	for _, tag := range tags {
		if tag.Name != InheritedTagKey && len(tag.Values) > 0 {
			out[tag.Name] = tag.Values
		}
	}
	return out, nil
}

func (c *Client) ApplyTags(ctx context.Context, objectType string, objectID int64, desired map[string][]string) error {
	current, err := c.GetTagValues(ctx, objectType, objectID)
	if err != nil {
		return err
	}
	definitions, err := c.TagDefinitions(ctx, objectType)
	if err != nil {
		return err
	}
	byName := make(map[string]TagDefinition, len(definitions))
	for _, def := range definitions {
		byName[def.Name] = def
	}

	var missing []string
	for name := range desired {
		if _, ok := byName[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &UnknownTagsError{Names: missing}
	}

	heldTags := make(map[string]ObjectTag, len(current))
	held := make(map[string][]string, len(current))
	for _, tag := range current {
		heldTags[tag.Name] = tag
		held[tag.Name] = tag.Values
	}
	for _, name := range changedTagNames(held, desired) {
		level, definitionID := byName[name].Level, byName[name].ID
		if existing, ok := heldTags[name]; ok {
			level, definitionID = existing.Level, existing.DefinitionID
		}
		if err := c.SetTagValues(ctx, objectType, objectID, level, definitionID, desired[name]); err != nil {
			return fmt.Errorf("set tag %q: %w", name, err)
		}
	}
	return nil
}

func changedTagNames(held, desired map[string][]string) []string {
	var names []string
	for name, values := range desired {
		if !slices.Equal(held[name], values) {
			names = append(names, name)
		}
	}
	for name, values := range held {
		if _, kept := desired[name]; !kept && len(values) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
