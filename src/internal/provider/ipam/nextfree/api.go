package nextfree

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

const (
	ipPath     = "/ipam/next-free-ip"
	subnetPath = "/ipam/next-free-subnet"

	MaxCount   = 100
	MaxExclude = 100
)

var exhaustedCodes = map[string]bool{"NO_FREE_IP": true, "INSUFFICIENT_FREE_IPS": true, "NO_FREE_SUBNET": true}

type Target struct {
	ID   int64
	CIDR string
}

func (t Target) query(idKey, cidrKey string) url.Values {
	q := url.Values{}
	if t.ID != 0 {
		q.Set(idKey, strconv.FormatInt(t.ID, 10))
	}
	if t.CIDR != "" {
		q.Set(cidrKey, t.CIDR)
	}
	return q
}

type nodeRef struct {
	ID   int64  `json:"id"`
	CIDR string `json:"cidr"`
}

type IPs struct {
	Subnet    Target
	Addresses []string
}

type Block struct {
	Container Target
	CIDR      netip.Prefix
}

func IsExhausted(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && exhaustedCodes[apiErr.Code]
}

func FindIPs(ctx context.Context, c *client.Client, subnet Target, count int64, exclude []string) (*IPs, error) {
	q := subnet.query("subnet_id", "subnet_cidr")
	q.Set("count", strconv.FormatInt(count, 10))
	if len(exclude) > 0 {
		q["exclude"] = exclude
	}
	var out struct {
		Subnet    nodeRef  `json:"subnet"`
		Addresses []string `json:"addresses"`
	}
	if err := c.Get(ctx, ipPath, q, &out); err != nil {
		return nil, err
	}
	return &IPs{Subnet: Target(out.Subnet), Addresses: out.Addresses}, nil
}

func FindSubnet(ctx context.Context, c *client.Client, container Target, prefix int64, exclude []string) (*Block, error) {
	q := container.query("container_id", "container_cidr")
	q.Set("prefix_length", strconv.FormatInt(prefix, 10))
	if len(exclude) > 0 {
		q["exclude"] = exclude
	}
	var out struct {
		Container nodeRef `json:"container"`
		CIDR      string  `json:"cidr"`
	}
	if err := c.Get(ctx, subnetPath, q, &out); err != nil {
		return nil, err
	}
	block, err := netip.ParsePrefix(out.CIDR)
	if err != nil {
		return nil, fmt.Errorf("OmniDDI returned an invalid free block %q: %w", out.CIDR, err)
	}
	return &Block{Container: Target(out.Container), CIDR: block.Masked()}, nil
}
