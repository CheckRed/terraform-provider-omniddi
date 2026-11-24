package framework

import (
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

type ApprovalMode string

const (
	ApprovalModeFail ApprovalMode = "fail"
	ApprovalModeWait ApprovalMode = "wait"

	DefaultOperationTimeout = 10 * time.Minute
)

type ProviderData struct {
	Client         *client.Client
	DefaultTimeout time.Duration
	ApprovalMode   ApprovalMode
	DefaultTags    map[string]string
}

func ConfigureProviderData(raw any, diags *diag.Diagnostics) *ProviderData {
	if raw == nil {
		return nil
	}
	pd, ok := raw.(*ProviderData)
	if !ok || pd == nil || pd.Client == nil {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *framework.ProviderData with a client, got %T. Please report this to the provider developers.", raw))
		return nil
	}
	return pd
}

func (pd *ProviderData) timeout() time.Duration {
	if pd == nil || pd.DefaultTimeout <= 0 {
		return DefaultOperationTimeout
	}
	return pd.DefaultTimeout
}
