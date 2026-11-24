package nodeapi_test

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/omniddigql"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/ipamtest"
	"github.com/CheckRed/terraform-provider-omniddi/internal/provider/ipam/nodeapi"
)

func newClient(t *testing.T, b *ipamtest.Backend) *client.Client {
	t.Helper()
	c, err := client.New(context.Background(), client.Config{
		Endpoint:       b.Srv.URL(),
		Username:       fakeserver.DefaultUsername,
		Password:       fakeserver.DefaultPassword,
		RequestTimeout: 5 * time.Second,
		RetryBackoff:   client.Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

func ptr[T any](v T) *T { return &v }

func TestQueriesMatchBackendSchema(t *testing.T) {
	omniddigql.RequireValid(t, nodeapi.AllQueries...)
}

func TestFetchAndFetchOfType(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ctx := context.Background()
	id := b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/8", Label: "corp"})

	node, err := nodeapi.Fetch(ctx, c, id)
	if err != nil || node.Label != "corp" || node.CIDR == nil || *node.CIDR != "10.0.0.0/8" {
		t.Fatalf("Fetch = %+v, %v", node, err)
	}
	if prefix, ok := node.Prefix(); !ok || prefix.Bits() != 8 {
		t.Errorf("Prefix = %v, %v", prefix, ok)
	}
	if _, err := nodeapi.Fetch(ctx, c, 9999); !client.IsNotFound(err) {
		t.Errorf("missing node error = %v, want not found", err)
	}
	if _, err := nodeapi.FetchOfType(ctx, c, id, nodeapi.TypeSubnet); err == nil {
		t.Errorf("FetchOfType accepted the wrong type")
	}
	if _, err := nodeapi.FetchOfType(ctx, c, id, nodeapi.TypeContainer); err != nil {
		t.Errorf("FetchOfType: %v", err)
	}
	if _, ok := (nodeapi.Node{}).Prefix(); ok {
		t.Errorf("a node without a CIDR must have no prefix")
	}
}

func seedTree(b *ipamtest.Backend) map[string]int64 {
	ids := map[string]int64{}
	ids["a"] = b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.0.0.0/8", Label: "a"})
	ids["b"] = b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "10.1.0.0/16", Label: "b"})
	ids["s"] = b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: "10.1.1.0/24", Label: "s"})
	ids["v6"] = b.Seed(ipamtest.Node{Type: "CONTAINER", CIDR: "2001:db8::/32", Label: "v6"})
	return ids
}

func TestResolveCIDRDescendsTheTree(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ids := seedTree(b)
	ctx := context.Background()

	for cidr, want := range map[string]int64{
		"10.0.0.0/8": ids["a"], "10.1.0.0/16": ids["b"], "10.1.1.0/24": ids["s"], "2001:DB8::/32": ids["v6"],
	} {
		node, err := nodeapi.ResolveCIDR(ctx, c, cidr, "")
		if err != nil || node.ID != want {
			t.Errorf("ResolveCIDR(%s) = %+v, %v; want id %d", cidr, node, err, want)
		}
	}
	if _, err := nodeapi.ResolveCIDR(ctx, c, "10.1.1.0/24", nodeapi.TypeContainer); !client.IsNotFound(err) {
		t.Errorf("type mismatch error = %v, want not found", err)
	}
	if _, err := nodeapi.ResolveCIDR(ctx, c, "172.16.0.0/12", ""); !client.IsNotFound(err) {
		t.Errorf("missing CIDR error = %v, want not found", err)
	}
	if _, err := nodeapi.ResolveCIDR(ctx, c, "nonsense", ""); err == nil {
		t.Errorf("invalid CIDR accepted")
	}
}

func TestChildrenAndAncestors(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ids := seedTree(b)
	ctx := context.Background()

	roots, err := nodeapi.Children(ctx, c, nil, "")
	if err != nil || len(roots) != 2 {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	parent := ids["a"]
	kids, err := nodeapi.Children(ctx, c, &parent, "")
	if err != nil || len(kids) != 1 || kids[0].ID != ids["b"] {
		t.Fatalf("children = %+v, %v", kids, err)
	}
	if found, _ := nodeapi.Children(ctx, c, &parent, "nomatch"); len(found) != 0 {
		t.Errorf("search should filter children, got %d", len(found))
	}
	ancestors, err := nodeapi.AncestorIDs(ctx, c, ids["s"])
	if err != nil || len(ancestors) != 3 {
		t.Fatalf("ancestors = %v, %v", ancestors, err)
	}
}

func TestListAndFindSubnets(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ids := seedTree(b)
	ctx := context.Background()
	for i := 0; i < 205; i++ {
		b.Seed(ipamtest.Node{Type: "SUBNET", CIDR: fmt.Sprintf("192.168.%d.0/24", i), Label: fmt.Sprintf("lan-%03d", i)})
	}

	all, err := nodeapi.ListSubnets(ctx, c, nodeapi.SubnetQuery{})
	if err != nil || len(all) != 206 {
		t.Fatalf("ListSubnets = %d subnets, %v; want 206 across two pages", len(all), err)
	}
	scope := ids["a"]
	scoped, err := nodeapi.ListSubnets(ctx, c, nodeapi.SubnetQuery{ScopeNodeID: &scope})
	if err != nil || len(scoped) != 1 || scoped[0].ID != ids["s"] {
		t.Errorf("scoped = %+v, %v", scoped, err)
	}
	filtered, err := nodeapi.ListSubnets(ctx, c, nodeapi.SubnetQuery{Filters: []nodeapi.SubnetFilter{{Field: "name", Operator: "EQ", Value: "lan-004"}}})
	if err != nil || len(filtered) != 1 {
		t.Errorf("filtered = %+v, %v", filtered, err)
	}

	node, err := nodeapi.FindSubnetByCIDR(ctx, c, "192.168.7.0/24")
	if err != nil || node.Label != "lan-007" {
		t.Errorf("FindSubnetByCIDR = %+v, %v", node, err)
	}
	if _, err := nodeapi.FindSubnetByCIDR(ctx, c, "10.250.0.0/24"); !client.IsNotFound(err) {
		t.Errorf("missing subnet error = %v", err)
	}
	if _, err := nodeapi.FindSubnetByCIDR(ctx, c, "bad"); err == nil {
		t.Errorf("invalid CIDR accepted")
	}
}

func TestWalkContainers(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ids := seedTree(b)
	ctx := context.Background()

	all, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true})
	if err != nil || len(all) != 3 {
		t.Fatalf("recursive walk = %d, %v", len(all), err)
	}
	top, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{})
	if err != nil || len(top) != 2 {
		t.Fatalf("non-recursive walk = %d, %v; want the two top-level containers", len(top), err)
	}
	parent := ids["a"]
	below, err := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{ParentID: &parent})
	if err != nil || len(below) != 1 || below[0].ID != ids["b"] {
		t.Errorf("walk below a = %+v, %v", below, err)
	}
	searched, _ := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true, Search: "V6"})
	if len(searched) != 1 || searched[0].ID != ids["v6"] {
		t.Errorf("search = %+v", searched)
	}
	b.Srv.AddTagDefinition(fakeserver.TagDefinition{ID: 9, Name: "zone"})
	if err := c.ApplyTags(ctx, client.TagObjectNetworkContain, ids["b"], map[string][]string{"zone": {"east"}}); err != nil {
		t.Fatalf("ApplyTags: %v", err)
	}
	tagged, _ := nodeapi.WalkContainers(ctx, c, nodeapi.WalkOptions{Recursive: true, Tags: map[string]string{"zone": "east"}})
	if len(tagged) != 1 || tagged[0].ID != ids["b"] {
		t.Errorf("tag filter = %+v", tagged)
	}
}

func TestCIDRHelpers(t *testing.T) {
	t.Parallel()
	prefix, err := nodeapi.ParsePrefix(" 10.0.0.5/24 ")
	if err != nil || prefix.String() != "10.0.0.0/24" || nodeapi.ProtocolOf(prefix) != nodeapi.ProtocolIPv4 {
		t.Errorf("ParsePrefix = %v, %v", prefix, err)
	}
	v6, _ := nodeapi.ParsePrefix("2001:db8::/32")
	if nodeapi.ProtocolOf(v6) != nodeapi.ProtocolIPv6 {
		t.Errorf("v6 protocol = %s", nodeapi.ProtocolOf(v6))
	}
	if _, err := nodeapi.ParsePrefix("x"); err == nil {
		t.Errorf("invalid prefix accepted")
	}
	for _, c := range []struct {
		addr string
		bits int
		ok   bool
	}{{"10.0.0.0", 24, true}, {"10.0.0.1", 24, false}, {"10.0.0.0", 33, false}, {"10.0.0.0", -1, false}, {"2001:db8::", 32, true}} {
		addr := mustAddr(c.addr)
		if got := nodeapi.CheckNetworkAddress(addr, c.bits) == nil; got != c.ok {
			t.Errorf("CheckNetworkAddress(%s,%d) ok=%v, want %v", c.addr, c.bits, got, c.ok)
		}
	}
	if nodeapi.CheckNetworkAddress(mustAddr("0.0.0.0"), 0) != nil {
		t.Errorf("0.0.0.0/0 is a valid network")
	}
}

func TestNetworkCIDRValidator(t *testing.T) {
	t.Parallel()
	v := nodeapi.NetworkCIDR()
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) == "" {
		t.Errorf("validator must describe itself")
	}
	cases := map[string]bool{"10.0.0.0/16": true, "10.0.0.1/16": false, "garbage": false}
	for in, ok := range cases {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(in)}, resp)
		if resp.Diagnostics.HasError() == ok {
			t.Errorf("%q: error=%v, want valid=%v", in, resp.Diagnostics.HasError(), ok)
		}
	}
	for _, skipped := range []types.String{types.StringNull(), types.StringUnknown()} {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: skipped}, resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("null/unknown must be skipped")
		}
	}
}

func TestValueHelpers(t *testing.T) {
	t.Parallel()
	if got := nodeapi.OptionalString(ptr("x"), types.StringNull()); got.ValueString() != "x" {
		t.Errorf("OptionalString = %v", got)
	}
	if got := nodeapi.OptionalString(ptr(""), types.StringNull()); !got.IsNull() {
		t.Errorf("empty remote with null prior must be null, got %v", got)
	}
	if got := nodeapi.OptionalString(nil, types.StringValue("")); got.IsNull() || got.ValueString() != "" {
		t.Errorf("empty remote with empty prior must stay empty, got %v", got)
	}
	if got := nodeapi.ClearableString(types.StringNull()); got != "" {
		t.Errorf("ClearableString(null) = %q", got)
	}
	if got := nodeapi.ClearableString(types.StringValue("v")); got != "v" {
		t.Errorf("ClearableString = %q", got)
	}
	body := map[string]any{}
	nodeapi.SetIfConfigured(body, "a", types.StringValue("1"))
	nodeapi.SetIfConfigured(body, "b", types.StringNull())
	nodeapi.SetIfConfigured(body, "c", types.StringUnknown())
	if len(body) != 1 || body["a"] != "1" {
		t.Errorf("SetIfConfigured = %v", body)
	}
	if !nodeapi.StringOrNull(nil).IsNull() || !nodeapi.Int64OrNull(nil).IsNull() || nodeapi.Int64OrNull(ptr(int64(3))).ValueInt64() != 3 {
		t.Errorf("null helpers broken")
	}
}

func TestVLANFromAPI(t *testing.T) {
	t.Parallel()
	vlan, id := nodeapi.VLANFromAPI(ptr("100"), types.StringNull(), types.Int64Value(100))
	if !vlan.IsNull() || id.ValueInt64() != 100 {
		t.Errorf("numeric prior = %v, %v", vlan, id)
	}
	vlan, id = nodeapi.VLANFromAPI(ptr("lab"), types.StringNull(), types.Int64Value(100))
	if !vlan.IsNull() || !id.IsNull() {
		t.Errorf("non-numeric remote with numeric prior = %v, %v", vlan, id)
	}
	vlan, id = nodeapi.VLANFromAPI(nil, types.StringNull(), types.Int64Value(100))
	if !vlan.IsNull() || !id.IsNull() {
		t.Errorf("absent remote with numeric prior = %v, %v", vlan, id)
	}
	vlan, id = nodeapi.VLANFromAPI(ptr("lab"), types.StringNull(), types.Int64Null())
	if vlan.ValueString() != "lab" || !id.IsNull() {
		t.Errorf("string vlan = %v, %v", vlan, id)
	}
}

func TestReconcileParent(t *testing.T) {
	t.Parallel()
	b := ipamtest.New(t)
	c := newClient(t, b)
	ids := seedTree(b)
	ctx := context.Background()
	node, err := nodeapi.Fetch(ctx, c, ids["s"])
	if err != nil {
		t.Fatal(err)
	}
	actual := ids["b"]

	got, err := nodeapi.ReconcileParent(ctx, c, types.Int64Null(), node)
	if err != nil || got.ValueInt64() != actual {
		t.Errorf("null prior = %v, %v", got, err)
	}
	got, _ = nodeapi.ReconcileParent(ctx, c, types.Int64Value(actual), node)
	if got.ValueInt64() != actual {
		t.Errorf("matching prior = %v", got)
	}
	got, _ = nodeapi.ReconcileParent(ctx, c, types.Int64Value(ids["a"]), node)
	if got.ValueInt64() != ids["a"] {
		t.Errorf("ancestor prior must be kept, got %v", got)
	}
	got, _ = nodeapi.ReconcileParent(ctx, c, types.Int64Value(ids["v6"]), node)
	if got.ValueInt64() != actual {
		t.Errorf("unrelated prior must yield the actual parent, got %v", got)
	}
	b.Srv.FailNext("POST", "/graphql", 500, 10)
	if _, err := nodeapi.ReconcileParent(ctx, c, types.Int64Value(ids["a"]), node); err == nil {
		t.Errorf("ancestor lookup failure must be returned")
	}
}

func TestFlattenAndTags(t *testing.T) {
	t.Parallel()
	n := nodeapi.Node{
		ID: 5, Type: "SUBNET", ParentID: ptr(int64(1)), Label: "n", CIDR: ptr("10.0.0.0/24"), Description: ptr(""),
		Tags: map[string]any{"env": "prod", "teams": []any{"a", "b"}, "@inherited": []any{"env"}, "nil": nil},
	}
	model, diags := nodeapi.Flatten(context.Background(), n)
	if diags.HasError() {
		t.Fatalf("Flatten: %v", diags)
	}
	if model.ID.ValueInt64() != 5 || !model.Description.IsNull() || model.Tags.Elements()["env"].String() != `"prod"` {
		t.Errorf("model = %+v", model)
	}
	if len(model.TagsList.Elements()) != 1 || len(model.Tags.Elements()) != 1 {
		t.Errorf("tags split wrong: %v / %v", model.Tags, model.TagsList)
	}
	list, diags := nodeapi.FlattenList(context.Background(), []nodeapi.Node{n, n})
	if diags.HasError() || len(list.Elements()) != 2 {
		t.Errorf("FlattenList = %v, %v", list, diags)
	}
	if !nodeapi.MatchesTags(n.Tags, map[string]string{"env": "prod", "teams": "b"}) ||
		nodeapi.MatchesTags(n.Tags, map[string]string{"env": "dev"}) || nodeapi.MatchesTags(n.Tags, map[string]string{"x": "y"}) {
		t.Errorf("MatchesTags wrong")
	}
	if !nodeapi.MatchesTags(nil, nil) {
		t.Errorf("no filters must match")
	}
	if len(nodeapi.DataAttributes(true, true, true)) != len(nodeapi.DataAttrTypes()) {
		t.Errorf("schema attributes and object type disagree")
	}
}

func TestKeepConfiguredCIDRAndSearch(t *testing.T) {
	t.Parallel()
	if got := nodeapi.KeepConfiguredCIDR(types.StringValue("2001:DB8::/32"), types.StringValue("2001:db8::/32")); got.ValueString() != "2001:DB8::/32" {
		t.Errorf("equivalent CIDR must keep the configured spelling, got %v", got)
	}
	if got := nodeapi.KeepConfiguredCIDR(types.StringNull(), types.StringValue("10.0.0.0/8")); got.ValueString() != "10.0.0.0/8" {
		t.Errorf("null config = %v", got)
	}
	if got := nodeapi.KeepConfiguredCIDR(types.StringValue("10.0.0.0/8"), types.StringValue("10.0.0.0/9")); got.ValueString() != "10.0.0.0/9" {
		t.Errorf("different CIDR must use the fetched value, got %v", got)
	}
	n := nodeapi.Node{Label: "Core", Owner: ptr("NetOps")}
	if !nodeapi.MatchesSearch(n, "") || !nodeapi.MatchesSearch(n, " netops ") || nodeapi.MatchesSearch(n, "zzz") {
		t.Errorf("MatchesSearch wrong")
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
