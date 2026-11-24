package dnskit

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
)

type BodyBuilder struct {
	Body   map[string]any
	update bool
}

func NewBodyBuilder(update bool) *BodyBuilder {
	return &BodyBuilder{Body: map[string]any{}, update: update}
}

func (b *BodyBuilder) Put(key string, plan, prior attr.Value, value func() any) {
	switch {
	case plan.IsUnknown():
	case plan.IsNull():
		if b.update && !prior.IsNull() {
			b.Body[key] = nil
		}
	case !b.update || !plan.Equal(prior):
		b.Body[key] = value()
	}
}

func (b *BodyBuilder) PutIfSet(key string, plan, prior attr.Value, value func() any) {
	if !plan.IsNull() {
		b.Put(key, plan, prior, value)
	}
}
