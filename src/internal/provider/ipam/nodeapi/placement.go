package nodeapi

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const maxPlacementDepth = 64

// DeepestContainerBelow returns the deepest container under parentID that would hold prefix, or nil when parentID is its immediate parent.
func DeepestContainerBelow(ctx context.Context, c *client.Client, parentID int64, prefix netip.Prefix) (*Node, error) {
	var deepest *Node
	current := parentID
	for range maxPlacementDepth {
		children, err := Children(ctx, c, &current, "")
		if err != nil {
			return nil, fmt.Errorf("list children of node %d: %w", current, err)
		}
		next := holdingContainer(children, prefix)
		if next == nil {
			return deepest, nil
		}
		deepest, current = next, next.ID
	}
	return deepest, nil
}

func holdingContainer(children []Node, prefix netip.Prefix) *Node {
	for i := range children {
		child := children[i]
		if child.Type != TypeContainer {
			continue
		}
		if held, ok := child.Prefix(); ok && held.Bits() < prefix.Bits() && held.Contains(prefix.Addr()) {
			return &child
		}
	}
	return nil
}
