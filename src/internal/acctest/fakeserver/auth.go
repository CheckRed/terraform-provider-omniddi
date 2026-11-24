package fakeserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const refreshCookieName = "refresh_token"

type User struct {
	Email                  string
	Password               string
	MFARequired            bool
	RequiresPasswordChange bool
	Spaces                 []int64
}

type AuthCounts struct {
	Logins    int
	Refreshes int
	Switches  int
}

type accessToken struct {
	user    string
	ns      int64
	exp     time.Time
	expired bool
}

type refreshToken struct {
	user   string
	family string
	used   bool
}

type authState struct {
	mu       sync.Mutex
	users    map[string]User
	access   map[string]*accessToken
	refresh  map[string]*refreshToken
	revoked  map[string]bool
	counts   AuthCounts
	tokenSeq int
}

func (s *Server) initAuth() {
	s.auth = authState{
		users:   map[string]User{},
		access:  map[string]*accessToken{},
		refresh: map[string]*refreshToken{},
		revoked: map[string]bool{},
	}
	s.AddUser(User{Email: DefaultUsername, Password: DefaultPassword, Spaces: []int64{DefaultSpaceID, DefaultSpaceID + 1}})
}

// AddUser registers a login; its first space is where login lands.
func (s *Server) AddUser(u User) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if len(u.Spaces) == 0 {
		u.Spaces = []int64{DefaultSpaceID}
	}
	u.Spaces = slices.Clone(u.Spaces)
	s.auth.users[u.Email] = u
}

func (s *Server) ExpireAccessTokens() {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	for _, tok := range s.auth.access {
		tok.expired = true
	}
}

func (s *Server) RevokeRefreshTokens() {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	s.auth.refresh = map[string]*refreshToken{}
}

func (s *Server) AuthCounts() AuthCounts {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	return s.auth.counts
}

// RequestSpace returns the network space the request's bearer token was issued for, or 0.
func (s *Server) RequestSpace(req Request) int64 {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	token, _ := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if tok, ok := s.auth.access[token]; ok {
		return tok.ns
	}
	return 0
}

func (s *Server) mintAccess(user string, ns int64, exp time.Time, family string) string {
	a := &s.auth
	a.tokenSeq++
	claims := map[string]any{"sub": user, "email": user, "ns": ns, "exp": exp.Unix(), "iat": s.clock().Unix(), "fid": family, "jti": a.tokenSeq}
	payload, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	token := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(payload) + "." + enc.EncodeToString([]byte("synthetic-signature"))
	a.access[token] = &accessToken{user: user, ns: ns, exp: exp}
	return token
}

func (s *Server) mintRefresh(user, family string) string {
	a := &s.auth
	a.tokenSeq++
	token := fmt.Sprintf("refresh-%d", a.tokenSeq)
	a.refresh[token] = &refreshToken{user: user, family: family}
	return token
}

func refreshCookie(value string) http.Header {
	h := http.Header{}
	h.Add("Set-Cookie", (&http.Cookie{Name: refreshCookieName, Value: value, Path: "/api/v1/users", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
	return h
}

func (s *Server) serveBuiltinAuth(req *Request) (Response, bool) {
	if req.Method != http.MethodPost {
		return Response{}, false
	}
	switch req.Path {
	case "/api/v1/users/token":
		return s.login(req), true
	case "/api/v1/users/refresh":
		return s.refreshAccess(req), true
	case "/api/v1/network-spaces/switch":
		return s.switchSpace(req), true
	}
	return Response{}, false
}

func (s *Server) login(req *Request) Response {
	a := &s.auth
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counts.Logins++
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := req.JSON(&body); err != nil {
		return Detail(http.StatusBadRequest, "Field required")
	}
	user, ok := a.users[body.Email]
	if !ok || user.Password != body.Password {
		return Detail(http.StatusUnauthorized, "Incorrect email or password")
	}
	if user.MFARequired {
		return JSON(http.StatusOK, map[string]any{"mfa_required": true, "mfa_enabled": true, "email": user.Email, "message": "MFA is enabled for this account."})
	}
	family := fmt.Sprintf("fam-%d", a.tokenSeq+1)
	token := s.mintAccess(user.Email, user.Spaces[0], s.clock().Add(s.ttl), family)
	if user.RequiresPasswordChange {
		return JSON(http.StatusOK, map[string]any{"access_token": token, "token_type": "bearer", "requires_password_change": true, "onboarding_required": true})
	}
	return Response{Status: http.StatusOK, Header: refreshCookie(s.mintRefresh(user.Email, family)), Body: map[string]any{
		"access_token": token, "token_type": "bearer", "requires_password_change": false, "onboarding_required": false,
	}}
}

func (s *Server) refreshAccess(req *Request) Response {
	a := &s.auth
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counts.Refreshes++
	value := cookieValue(req.Header, refreshCookieName)
	if value == "" {
		return Detail(http.StatusForbidden, "Refresh token missing")
	}
	tok, ok := a.refresh[value]
	switch {
	case !ok:
		return Detail(http.StatusForbidden, "Invalid or expired refresh token")
	case tok.used || a.revoked[tok.family]:
		a.revoked[tok.family] = true
		return Detail(http.StatusForbidden, "Refresh token reuse detected. Please log in again.")
	}
	tok.used = true
	user := a.users[tok.user]
	ns := user.Spaces[0]
	var selection struct {
		NetworkSpaceID int64 `json:"network_space_id"`
	}
	if err := req.JSON(&selection); err == nil && slices.Contains(user.Spaces, selection.NetworkSpaceID) {
		ns = selection.NetworkSpaceID
	}
	token := s.mintAccess(tok.user, ns, s.clock().Add(s.ttl), tok.family)
	return Response{Status: http.StatusOK, Header: refreshCookie(s.mintRefresh(tok.user, tok.family)), Body: map[string]any{
		"access_token": token, "token_type": "bearer", "network_space_id": ns,
	}}
}

func (s *Server) switchSpace(req *Request) Response {
	a := &s.auth
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counts.Switches++
	current, denied := s.bearerLocked(req)
	if denied != nil {
		return *denied
	}
	var body struct {
		NetworkSpaceID int64 `json:"network_space_id"`
	}
	if err := req.JSON(&body); err != nil || !slices.Contains(a.users[current.user].Spaces, body.NetworkSpaceID) {
		return Detail(http.StatusForbidden, "Network space is unavailable")
	}
	token := s.mintAccess(current.user, body.NetworkSpaceID, current.exp, "switched")
	return JSON(http.StatusOK, map[string]any{"access_token": token, "token_type": "bearer", "network_space_id": body.NetworkSpaceID})
}

func (s *Server) authenticate(req *Request) *Response {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	_, denied := s.bearerLocked(req)
	return denied
}

func (s *Server) bearerLocked(req *Request) (*accessToken, *Response) {
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found {
		return nil, deny(http.StatusUnauthorized, "Not authenticated")
	}
	tok, ok := s.auth.access[token]
	if !ok || tok.expired || !s.clock().Before(tok.exp) {
		return nil, deny(http.StatusUnauthorized, "Could not validate credentials")
	}
	return tok, nil
}

func cookieValue(h http.Header, name string) string {
	for _, ck := range (&http.Request{Header: h}).Cookies() {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func deny(status int, detail string) *Response {
	r := Detail(status, detail)
	return &r
}
