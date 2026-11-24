package nodeapi

import (
	"context"
	"sort"
	"strings"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type WalkOptions struct {
	ParentID  *int64
	Recursive bool
	Search    string
	Tags      map[string]string
}

func MatchesSearch(n Node, search string) bool {
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}
	for _, field := range []string{n.Label, deref(n.CIDR), deref(n.Owner), deref(n.Description)} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func WalkContainers(ctx context.Context, c *client.Client, opts WalkOptions) ([]Node, error) {
	level, err := Children(ctx, c, opts.ParentID, "")
	if err != nil {
		return nil, err
	}
	var found []Node
	for depth := 0; len(level) > 0; depth++ {
		if depth >= maxTreeDepth {
			return nil, &TreeDepthError{Limit: maxTreeDepth}
		}
		var descend []Node
		for _, n := range level {
			if n.Type == TypeContainer && MatchesSearch(n, opts.Search) && MatchesTags(n.Tags, opts.Tags) {
				found = append(found, n)
			}
			if n.HasChildren && n.Type != TypeSubnet && (n.Type != TypeContainer || opts.Recursive) {
				descend = append(descend, n)
			}
		}
		level, err = childrenOfAll(ctx, c, descend)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found, nil
}
