package framework

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

type Operation int

const (
	OpCreate Operation = iota
	OpRead
	OpUpdate
	OpDelete
)

func TimeoutsAttribute(ctx context.Context) schema.Attribute {
	return timeouts.Attributes(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true})
}

func WithTimeout(ctx context.Context, op Operation, configured timeouts.Value, pd *ProviderData, diags *diag.Diagnostics) (context.Context, context.CancelFunc) {
	fallback := pd.timeout()
	var (
		d  time.Duration
		dd diag.Diagnostics
	)
	switch op {
	case OpCreate:
		d, dd = configured.Create(ctx, fallback)
	case OpRead:
		d, dd = configured.Read(ctx, fallback)
	case OpUpdate:
		d, dd = configured.Update(ctx, fallback)
	case OpDelete:
		d, dd = configured.Delete(ctx, fallback)
	}
	diags.Append(dd...)
	if dd.HasError() {
		d = fallback
	}
	if pd != nil && pd.Client != nil {
		ctx = pd.Client.MaskedContext(ctx)
	}
	return context.WithTimeout(ctx, d)
}
