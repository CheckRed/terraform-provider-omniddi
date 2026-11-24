package dnskit

import (
	"context"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func DeleteMatcher(id int64) func(map[string]any) bool {
	return func(details map[string]any) bool {
		target, _ := details["delete"].(map[string]any)
		return target["id"] == float64(id)
	}
}

func RecoverFunc(lookup func(ctx context.Context) (int64, error)) func(context.Context) (int64, bool, error) {
	return func(ctx context.Context) (int64, bool, error) {
		id, err := lookup(ctx)
		if client.IsNotFound(err) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
}

func RequireTaskHandle(outcome *framework.WriteOutcome) error {
	if outcome.Result == nil {
		return nil
	}
	return outcome.Result.RequireCorrelationID()
}
