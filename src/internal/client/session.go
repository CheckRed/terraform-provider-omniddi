package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	refreshFraction     = 0.8
	fallbackRefreshTTL  = 4 * time.Minute
	refreshRetryDelay   = 15 * time.Second
	errMFAUnsupported   = "the account has MFA enabled, which Terraform cannot answer; use a dedicated automation account without MFA"
	errOnboardingNeeded = "the account requires a password change or onboarding before it can be used by automation; complete it in OmniDDI first"
)

func validateCredentials(username, password string) error {
	hasControl := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
	switch {
	case username == "":
		return &ConfigError{Message: "a username is required"}
	case password == "":
		return &ConfigError{Message: "a password is required"}
	case strings.IndexFunc(username, hasControl) >= 0:
		return &ConfigError{Message: "username contains whitespace or control characters; remove any trailing newline"}
	}
	return nil
}

type tokenClaims struct {
	expires  time.Time
	issuedAt time.Time
	space    int64
}

func parseClaims(token string) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenClaims{}, errors.New("access token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, fmt.Errorf("decode access token claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return tokenClaims{}, fmt.Errorf("parse access token claims: %w", err)
	}
	return tokenClaims{
		expires:  unixClaim(claims["exp"]),
		issuedAt: unixClaim(claims["iat"]),
		space:    int64Claim(claims["ns"]),
	}, nil
}

func unixClaim(v any) time.Time {
	secs, ok := v.(float64)
	if !ok || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(secs), 0)
}

func int64Claim(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		parsed, _ := strconv.ParseInt(n, 10, 64)
		return parsed
	}
	return 0
}

type session struct {
	t        *transport
	baseURL  string
	username string
	password string
	wanted   int64
	now      func() time.Time
	flight   chan struct{}

	mu        sync.Mutex
	access    string
	expires   time.Time
	refresh   string
	refreshAt time.Time
	space     int64
	failed    error
}

func newSession(t *transport, cfg Config) *session {
	return &session{
		t: t, baseURL: cfg.Endpoint, username: cfg.Username, password: cfg.Password, wanted: cfg.NetworkSpaceID, now: cfg.Now,
		flight: make(chan struct{}, 1),
	}
}

func (s *session) networkSpaceID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.space != 0 {
		return s.space
	}
	return s.wanted
}

func (s *session) acquire(ctx context.Context) error {
	select {
	case s.flight <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) release() { <-s.flight }

func (s *session) fresh() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == "" || !s.now().Before(s.refreshAt) {
		return "", false
	}
	return s.access, true
}

func (s *session) current() (token string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access, s.expires
}

func (s *session) authorize(ctx context.Context, h http.Header) (string, error) {
	if token, ok := s.fresh(); ok {
		h.Set("Authorization", "Bearer "+token)
		return token, nil
	}
	if err := s.acquire(ctx); err != nil {
		return "", err
	}
	defer s.release()
	token, ok := s.fresh()
	if !ok {
		var err error
		if token, err = s.renewLocked(ctx); err != nil {
			return "", err
		}
	}
	h.Set("Authorization", "Bearer "+token)
	return token, nil
}

func (s *session) renewLocked(ctx context.Context) (string, error) {
	if err := s.terminal(); err != nil {
		return "", err
	}
	previous, expires := s.current()
	err := s.refreshLocked(ctx)
	if err == nil {
		token, _ := s.current()
		return token, nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	s.rememberIfTerminal(err)
	if previous != "" && transientAuthError(err) && s.now().Before(expires) {
		tflog.Warn(MaskSecrets(ctx), "token refresh failed; continuing with the current access token", map[string]any{"error": err.Error()})
		s.deferRefresh(expires)
		return previous, nil
	}
	s.dropAccess(previous)
	return "", err
}

func (s *session) deferRefresh(expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshAt = s.now().Add(refreshRetryDelay)
	if !expires.IsZero() && s.refreshAt.After(expires) {
		s.refreshAt = expires
	}
}

func (s *session) dropAccess(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" || s.access == token {
		s.access, s.expires, s.refreshAt = "", time.Time{}, time.Time{}
	}
}

func (s *session) terminal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// rememberIfTerminal keeps a rejected login or space switch so queued requests do not repeat it.
func (s *session) rememberIfTerminal(err error) {
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		return
	}
	switch authErr.StatusCode {
	case http.StatusOK, http.StatusUnauthorized, http.StatusForbidden:
		s.mu.Lock()
		defer s.mu.Unlock()
		s.failed = err
	}
}

func transientAuthError(err error) bool {
	var (
		transport *transportError
		authErr   *AuthError
	)
	switch {
	case errors.As(err, &transport):
		return true
	case errors.As(err, &authErr):
		return authErr.StatusCode >= http.StatusInternalServerError || authErr.StatusCode == http.StatusTooManyRequests ||
			authErr.StatusCode == http.StatusRequestTimeout
	}
	return false
}

func (s *session) reauthenticate(ctx context.Context, rejected string) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if token, _ := s.current(); token != rejected {
		return nil
	}
	if err := s.terminal(); err != nil {
		return err
	}
	if err := s.refreshLocked(ctx); err != nil {
		s.dropAccess(rejected)
		s.rememberIfTerminal(err)
		return err
	}
	return nil
}

func (s *session) post(ctx context.Context, path string, body any, bearer, cookie string) (*Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode %s body: %w", path, err)
	}
	header := http.Header{}
	if bearer != "" {
		header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		header.Set("Cookie", refreshCookie+"="+cookie)
	}
	return s.t.withRetry(ctx, retryRateLimited, func() (*Response, error) {
		return s.t.roundTrip(ctx, call{method: http.MethodPost, url: s.baseURL + path, header: header, body: payload})
	})
}

func (s *session) setRefresh(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh = token
}

func (s *session) refreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

func (s *session) loginLocked(ctx context.Context) error {
	resp, err := s.post(ctx, apiPrefix+"/users/token", map[string]string{"email": s.username, "password": s.password}, "", "")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return s.authFailure("login", resp)
	}
	var body struct {
		AccessToken            string `json:"access_token"`
		MFARequired            bool   `json:"mfa_required"`
		OnboardingRequired     bool   `json:"onboarding_required"`
		RequiresPasswordChange bool   `json:"requires_password_change"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return &AuthError{Message: "login returned an unreadable response", StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	}
	switch {
	case body.MFARequired:
		return &AuthError{Message: errMFAUnsupported, StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	case body.OnboardingRequired || body.RequiresPasswordChange:
		return &AuthError{Message: errOnboardingNeeded, StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	case body.AccessToken == "":
		return &AuthError{Message: "login returned no access token", StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	}
	s.setRefresh(refreshCookieFrom(resp))
	return s.adoptTokenLocked(ctx, body.AccessToken)
}

func (s *session) refreshLocked(ctx context.Context) error {
	refresh := s.refreshToken()
	if refresh == "" {
		return s.loginLocked(ctx)
	}
	var selection any
	if s.wanted > 0 {
		selection = map[string]int64{"network_space_id": s.wanted}
	}
	resp, err := s.post(ctx, apiPrefix+"/users/refresh", selection, "", refresh)
	if err != nil {
		s.setRefresh("")
		return err
	}
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		s.setRefresh("")
		return s.loginLocked(ctx)
	case resp.StatusCode != http.StatusOK:
		return s.authFailure("token refresh", resp)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.AccessToken == "" {
		return &AuthError{Message: "token refresh returned no access token", StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	}
	if rotated := refreshCookieFrom(resp); rotated != "" {
		s.setRefresh(rotated)
	}
	return s.adoptTokenLocked(ctx, body.AccessToken)
}

func (s *session) adoptTokenLocked(ctx context.Context, token string) error {
	claims, err := parseClaims(token)
	if err != nil {
		return &AuthError{Message: err.Error()}
	}
	refreshAt := s.refreshDeadline(claims)
	if s.wanted > 0 && claims.space != s.wanted {
		switched, switchedClaims, err := s.switchSpace(ctx, token)
		if err != nil {
			return err
		}
		token, claims = switched, switchedClaims
		refreshAt = earlier(refreshAt, s.refreshDeadline(switchedClaims))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access, s.expires, s.space, s.refreshAt = token, claims.expires, claims.space, refreshAt
	return nil
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (s *session) refreshDeadline(c tokenClaims) time.Time {
	now := s.now()
	if c.expires.IsZero() {
		return now.Add(fallbackRefreshTTL)
	}
	start := c.issuedAt
	if start.IsZero() || start.After(now) {
		start = now
	}
	return start.Add(time.Duration(float64(c.expires.Sub(start)) * refreshFraction))
}

func (s *session) switchSpace(ctx context.Context, bearer string) (string, tokenClaims, error) {
	resp, err := s.post(ctx, apiPrefix+"/network-spaces/switch", map[string]int64{"network_space_id": s.wanted}, bearer, "")
	if err != nil {
		return "", tokenClaims{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", tokenClaims{}, s.authFailure(fmt.Sprintf("switch to network space %d", s.wanted), resp)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil || body.AccessToken == "" {
		return "", tokenClaims{}, &AuthError{Message: "network space switch returned no access token", StatusCode: resp.StatusCode, RequestID: resp.RequestID}
	}
	claims, err := parseClaims(body.AccessToken)
	if err != nil {
		return "", tokenClaims{}, &AuthError{Message: err.Error()}
	}
	if claims.space != s.wanted {
		return "", tokenClaims{}, &AuthError{Message: fmt.Sprintf("network space switch landed in space %d, not %d", claims.space, s.wanted)}
	}
	return body.AccessToken, claims, nil
}

func (s *session) authFailure(step string, resp *Response) *AuthError {
	apiErr := apiErrorFrom(http.MethodPost, step, resp)
	msg := fmt.Sprintf("%s failed with HTTP %d", step, resp.StatusCode)
	if apiErr.Detail != "" {
		msg += ": " + apiErr.Detail
	}
	return &AuthError{Message: msg, StatusCode: resp.StatusCode, RequestID: resp.RequestID}
}

func refreshCookieFrom(resp *Response) string {
	for _, ck := range (&http.Response{Header: resp.Header}).Cookies() {
		if ck.Name == refreshCookie && ck.MaxAge >= 0 {
			return ck.Value
		}
	}
	return ""
}
