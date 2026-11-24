package dhcpkit

import (
	"context"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func RecoverBy(find func(ctx context.Context) (int64, error)) func(ctx context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := find(ctx)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}
