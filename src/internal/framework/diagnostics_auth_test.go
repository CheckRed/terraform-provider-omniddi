package framework_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CheckRed/terraform-provider-omniddi/internal/client"
	"github.com/CheckRed/terraform-provider-omniddi/internal/framework"
)

func TestAuthErrorsCarryAnActionableHint(t *testing.T) {
	cases := []struct {
		name   string
		status int
		path   string
		detail string
		want   string
	}{
		{"session rejected after re-login", http.StatusUnauthorized, "/zones/", "Could not validate credentials", "rejected the session even after signing in again"},
		{"malformed space header", http.StatusBadRequest, "/graphql", "X-Network-Space-Id must be a network space id", "Check network_space_id"},
		{"space the owner cannot use", http.StatusForbidden, "/graphql", "Network space not available", "cannot use the network space"},
		{"default space unavailable", http.StatusForbidden, "/graphql", "The Default network space is not available; name a network space in X-Network-Space-Id", "Set network_space_id"},
		{"missing permission", http.StatusForbidden, "/records/", "Permission denied", "lacks the OmniDDI permission"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &client.APIError{StatusCode: tc.status, Method: http.MethodPost, Path: tc.path, Detail: tc.detail}
			if got := framework.ErrorDetail(err); !strings.Contains(got, tc.want) {
				t.Errorf("detail = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestLoginFailuresCarryAnActionableHint(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   string
	}{
		{"wrong credentials", http.StatusUnauthorized, "OMNIDDI_PASSWORD"},
		{"rate limited", http.StatusTooManyRequests, "fewer parallel"},
		{"mfa or onboarding", http.StatusOK, "Authentication failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &client.AuthError{Message: "login failed", StatusCode: tc.status, RequestID: "req-1"}
			got := framework.ErrorDetail(err)
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "req-1") {
				t.Errorf("detail = %q, want it to contain %q and the request id", got, tc.want)
			}
		})
	}
}

func TestOtherAPIErrorsHaveNoAuthHint(t *testing.T) {
	err := &client.APIError{StatusCode: http.StatusBadRequest, Method: http.MethodPost, Path: "/records/", Detail: "Record already exists"}
	if got := framework.ErrorDetail(err); strings.Contains(got, "sign") || strings.Contains(got, "network_space_id") {
		t.Errorf("unexpected auth hint in %q", got)
	}
}
