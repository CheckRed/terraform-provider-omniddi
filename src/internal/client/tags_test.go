package client_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/acctest/fakeserver"
	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
)

func tagFixture(t *testing.T) (*client.Client, *fakeserver.Server) {
	t.Helper()
	c, srv := newClient(t)
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 1, Name: "env"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 2, Name: "owner", Level: "GLOBAL"})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 3, Name: "teams", DataType: "LIST", MultiValue: true})
	srv.AddTagDefinition(fakeserver.TagDefinition{ID: 4, Name: "subnet-only", AppliesTo: []string{client.TagObjectSubnet}})
	return c, srv
}

func TestApplyTagsSetsChangesAndClears(t *testing.T) {
	c, srv := tagFixture(t)
	ctx := context.Background()

	err := c.ApplyTags(ctx, client.TagObjectDNSView, 7, map[string][]string{"env": {"prod"}, "owner": {"netops"}, "teams": {"a", "b"}})
	if err != nil {
		t.Fatalf("ApplyTags: %v", err)
	}
	want := map[string][]string{"env": {"prod"}, "owner": {"netops"}, "teams": {"a", "b"}}
	if got := srv.TagValues(client.TagObjectDNSView, 7); !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}

	puts := len(srv.RequestsFor("PUT", "/api/v1/tags/values"))
	if err := c.ApplyTags(ctx, client.TagObjectDNSView, 7, want); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.RequestsFor("PUT", "/api/v1/tags/values")); got != puts {
		t.Errorf("unchanged tags must not be rewritten (%d new PUTs)", got-puts)
	}

	err = c.ApplyTags(ctx, client.TagObjectDNSView, 7, map[string][]string{"env": {"dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.TagValues(client.TagObjectDNSView, 7); !reflect.DeepEqual(got, map[string][]string{"env": {"dev"}}) {
		t.Errorf("tags after update = %v", got)
	}
}

func TestApplyTagsRejectsUnknownDefinitions(t *testing.T) {
	c, srv := tagFixture(t)

	err := c.ApplyTags(context.Background(), client.TagObjectDNSView, 7, map[string][]string{"nope": {"x"}, "env": {"y"}, "subnet-only": {"z"}})
	var unknown *client.UnknownTagsError
	if !errors.As(err, &unknown) || !reflect.DeepEqual(unknown.Names, []string{"nope", "subnet-only"}) {
		t.Fatalf("err = %v, want UnknownTagsError for nope and subnet-only", err)
	}
	if len(srv.RequestsFor("PUT", "/api/v1/tags/values")) != 0 {
		t.Errorf("nothing may be written when a definition is missing")
	}
}

func TestReadTagsIgnoresInheritedValues(t *testing.T) {
	c, srv := tagFixture(t)
	ctx := context.Background()
	if err := c.ApplyTags(ctx, client.TagObjectSubnet, 5, map[string][]string{"env": {"prod"}}); err != nil {
		t.Fatal(err)
	}
	srv.SetInheritedTag(client.TagObjectSubnet, 5, 2, "inherited-owner")

	own, err := c.GetTagValues(ctx, client.TagObjectSubnet, 5)
	if err != nil || len(own) != 1 || own[0].Name != "env" || own[0].Origin != "OWN" {
		t.Fatalf("GetTagValues = %+v, %v", own, err)
	}
	read, err := c.ReadTags(ctx, client.TagObjectSubnet, 5)
	if err != nil || !reflect.DeepEqual(read, map[string][]string{"env": {"prod"}}) {
		t.Fatalf("ReadTags = %v, %v", read, err)
	}
}

func TestApplyTagsClearsUsingHeldDefinition(t *testing.T) {
	c, srv := tagFixture(t)
	ctx := context.Background()
	if err := c.ApplyTags(ctx, client.TagObjectDNSZone, 3, map[string][]string{"owner": {"x"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyTags(ctx, client.TagObjectDNSZone, 3, map[string][]string{}); err != nil {
		t.Fatal(err)
	}
	if got := srv.TagValues(client.TagObjectDNSZone, 3); len(got) != 0 {
		t.Errorf("tags not cleared: %v", got)
	}
}

func TestSetTagValuesSendsExpectedBody(t *testing.T) {
	c, srv := tagFixture(t)
	if err := c.SetTagValues(context.Background(), client.TagObjectDNSView, 9, "SPACE", 1, []string{"prod"}); err != nil {
		t.Fatal(err)
	}
	body := srv.RequestsFor("PUT", "/api/v1/tags/values")[0].Body
	for _, want := range []string{`"object_type":"DNS_VIEW"`, `"object_id":9`, `"level":"SPACE"`, `"definition_id":1`, `"values":["prod"]`} {
		if !contains(string(body), want) {
			t.Errorf("body %s lacks %s", body, want)
		}
	}
}

func TestSetTagValuesBackendFailure(t *testing.T) {
	c, _ := tagFixture(t)
	err := c.SetTagValues(context.Background(), client.TagObjectDNSView, 9, "SPACE", 999, []string{"x"})
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want APIError 404", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
