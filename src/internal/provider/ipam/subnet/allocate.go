package subnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

const (
	nextFreePath          = "/ipam/next-free-subnet"
	maxAllocationAttempts = 3
)

func excludeList(ctx context.Context, set types.Set) ([]string, error) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}
	var values []cidrtypes.IPPrefix
	if diags := set.ElementsAs(ctx, &values, false); diags.HasError() {
		return nil, fmt.Errorf("read exclude: %s", diags)
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.ValueString())
	}
	return out, nil
}

func lookupBlock(ctx context.Context, c *client.Client, containerID, prefix int64, exclude []string) (netip.Prefix, error) {
	query := url.Values{
		"container_id":  {strconv.FormatInt(containerID, 10)},
		"prefix_length": {strconv.FormatInt(prefix, 10)},
	}
	if len(exclude) > 0 {
		query["exclude"] = exclude
	}
	var out struct {
		CIDR string `json:"cidr"`
	}
	if err := c.Get(ctx, nextFreePath, query, &out); err != nil {
		return netip.Prefix{}, fmt.Errorf("find the next free /%d in container %d: %w", prefix, containerID, err)
	}
	block, err := netip.ParsePrefix(out.CIDR)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("OmniDDI returned an invalid free block %q: %w", out.CIDR, err)
	}
	return block.Masked(), nil
}

type allocationRaceError struct {
	prefix, containerID int64
	last                error
}

func (e *allocationRaceError) Error() string {
	return fmt.Sprintf("another client took each free /%d chosen in container %d, so no subnet was created after %d attempts. Last error: %s",
		e.prefix, e.containerID, maxAllocationAttempts, e.last.Error())
}

func isBadRequest(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest
}

func (r *subnetResource) createAt(ctx context.Context, plan resourceModel) (*framework.WriteOutcome, error) {
	return framework.NewLifecycle(r.pd, types.StringNull()).Write(ctx, framework.WriteSpec{
		Method: http.MethodPost, Path: createPath, Body: createBody(plan), Recover: r.recoverByCIDR(plan.cidr()),
	})
}

func (r *subnetResource) createInNextFreeBlock(ctx context.Context, plan *resourceModel) (*framework.WriteOutcome, error) {
	exclude, err := excludeList(ctx, plan.Exclude)
	if err != nil {
		return nil, err
	}
	containerID, prefix := plan.ParentID.ValueInt64(), plan.Prefix.ValueInt64()
	block, err := lookupBlock(ctx, r.pd.Client, containerID, prefix, exclude)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		plan.NetworkAddr = iptypes.NewIPAddressValue(block.Addr().String())
		outcome, err := r.createAt(ctx, *plan)
		if err == nil || !isBadRequest(err) {
			return outcome, err
		}
		next, lookupErr := lookupBlock(ctx, r.pd.Client, containerID, prefix, exclude)
		if lookupErr != nil || next == block {
			return nil, err
		}
		if attempt == maxAllocationAttempts {
			return nil, &allocationRaceError{prefix: prefix, containerID: containerID, last: err}
		}
		block = next
	}
}
