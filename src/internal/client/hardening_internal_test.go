package client

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNotFoundSemantics(t *testing.T) {
	jsonBody := `{"detail": "Zone not found"}`
	htmlBody := "<html><body><h1>404 Not Found</h1><p>nginx</p></body></html>"
	tests := []struct {
		name   string
		method string
		path   string
		status int
		body   string
		want   bool
	}{
		{"GET json detail", http.MethodGet, "/zones/1", 404, jsonBody, true},
		{"GET json code only", http.MethodGet, "/zones/1", 404, `{"code": "NOT_FOUND"}`, true},
		{"DELETE json detail", http.MethodDelete, "/zones/1", 404, jsonBody, true},
		{"GET unknown route", http.MethodGet, "/zones/1", 404, `{"detail": "Not Found"}`, false},
		{"GET html", http.MethodGet, "/zones/1", 404, htmlBody, false},
		{"GET plain text", http.MethodGet, "/zones/1", 404, "not found", false},
		{"GET empty body", http.MethodGet, "/zones/1", 404, "", false},
		{"GET json without detail", http.MethodGet, "/zones/1", 404, `{"message": "x"}`, false},
		{"graphql json", http.MethodPost, graphQLPath, 404, jsonBody, false},
		{"graphql get", http.MethodGet, graphQLPath, 404, jsonBody, false},
		{"PUT json", http.MethodPut, "/zones/1", 404, jsonBody, false},
		{"POST json", http.MethodPost, "/zones/", 404, jsonBody, false},
		{"GET 500", http.MethodGet, "/zones/1", 500, jsonBody, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := apiErrorFrom(tc.method, tc.path, resp(tc.status, tc.body))
			if IsNotFound(err) != tc.want {
				t.Errorf("IsNotFound = %v, want %v", IsNotFound(err), tc.want)
			}
		})
	}
}

func TestSyntheticAndTypedNotFound(t *testing.T) {
	if !IsNotFound(&APIError{StatusCode: 404, Detail: "view not found"}) {
		t.Error("a literal APIError{404} stays not-found for resource packages")
	}
	if IsNotFound(&APIError{StatusCode: 404, Method: http.MethodPost, Path: "/x"}) {
		t.Error("a real 404 on a POST is not not-found")
	}
	nf := NewNotFound("zone 7")
	var typed *ObjectNotFoundError
	if !IsNotFound(nf) || !errors.As(nf, &typed) || typed.What != "zone 7" || nf.Error() != "zone 7 not found" {
		t.Errorf("NewNotFound = %v", nf)
	}
	if !IsNotFound(fmt.Errorf("lookup: %w", nf)) {
		t.Error("wrapped not-found must still match")
	}
	if NewNotFound("").Error() != "object not found" {
		t.Error("empty subject text")
	}
}

func TestDuplicateAndAmbiguousClassification(t *testing.T) {
	if !IsDuplicate(&APIError{StatusCode: 409}) || !IsDuplicate(&APIError{StatusCode: 400, Detail: "Zone already exists"}) {
		t.Error("409 and 'already exists' are duplicates")
	}
	if IsDuplicate(&APIError{StatusCode: 400, Detail: "bad ttl"}) || IsDuplicate(errors.New("x")) {
		t.Error("other errors are not duplicates")
	}
	for _, err := range []error{
		&transportError{Err: errors.New("EOF")}, &ResponseTooLargeError{}, &APIError{StatusCode: 504}, &APIError{StatusCode: 500},
	} {
		if !IsAmbiguousWriteFailure(err) {
			t.Errorf("%T must be ambiguous", err)
		}
	}
	if IsAmbiguousWriteFailure(&APIError{StatusCode: 400}) || IsAmbiguousWriteFailure(errors.New("x")) {
		t.Error("client errors are definitive")
	}
}

func TestSanitizeServerText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ansi", "\x1b[31mred\x1b[0m text", "red text"},
		{"osc title", "a\x1b]0;evil\x07b", "ab"},
		{"control chars", "a\x00b\x07c" + string(rune(0x202e)) + "d", "abcd"},
		{"newlines collapse", "line1\n\nline2\t tab", "line1 line2 tab"},
		{"html page", "<html><head><title>502 Bad Gateway</title><style>p{}</style></head><body><h1>502</h1><script>alert(1)</script>nginx &amp; co</body></html>", "502 Bad Gateway 502 nginx & co"},
		{"comparison kept", "value must be < 5 and > 3", "value must be < 5 and > 3"},
		{"api key", "bad key ddi_ak_1.abc-DEF_9.x rejected", "bad key ddi_ak_[REDACTED] rejected"},
		{"user api key", "bad key ddi_uk_Ab3-x_9.Z rejected", "bad key ddi_uk_[REDACTED] rejected"},
		{"bearer", "Authorization: Bearer abcdefghijklmnop.q-r", "Authorization: Bearer [REDACTED]"},
		{"raw jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln expired", "token [REDACTED-JWT] expired"},
		{"invalid utf8", "a\xffb", "a?b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeServerText(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeServerTextCapsLength(t *testing.T) {
	got := sanitizeServerText(strings.Repeat("é", 5000))
	if len(got) > maxServerTextBytes+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("len=%d suffix=%q", len(got), got[len(got)-3:])
	}
	if strings.ContainsRune(strings.TrimSuffix(got, "..."), '�') {
		t.Error("truncation must not split a rune")
	}
}

func TestSanitizeServerLogs(t *testing.T) {
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("line %d \x1b[32mok\x1b[0m", i))
	}
	got := sanitizeServerLogs(strings.Join(lines, "\n"))
	kept := strings.Split(got, "\n")
	if len(kept) != maxLogLines || kept[0] != "line 60 ok" || kept[len(kept)-1] != "line 99 ok" {
		t.Errorf("kept %d lines, first %q last %q", len(kept), kept[0], kept[len(kept)-1])
	}

	long := sanitizeServerLogs(strings.Repeat("x", 10_000) + "\nTAIL ddi_ak_1.secretvalue")
	if len(long) > maxServerTextBytes+3 || !strings.HasPrefix(long, "...") || !strings.Contains(long, "TAIL ddi_ak_[REDACTED]") {
		t.Errorf("long logs must keep the tail and redact secrets, got %d bytes: %q", len(long), long[len(long)-40:])
	}
}

func TestAPIErrorDetailIsSanitizedAndRedirectShowsOnlyTheHost(t *testing.T) {
	r := resp(502, "<html><body>Bad\x1b[31m Gateway ddi_ak_1.leak</body></html>")
	if got := apiErrorFrom("GET", "/x", r).Detail; got != "Bad Gateway ddi_ak_[REDACTED]" {
		t.Errorf("detail = %q", got)
	}
	r = resp(302, "")
	r.Header = http.Header{"Location": {"https://user:pw@idp.example.com/login?token=secret#frag"}}
	got := apiErrorFrom("GET", "/x", r).Detail
	if !strings.Contains(got, "idp.example.com") || strings.Contains(got, "secret") || strings.Contains(got, "login") {
		t.Errorf("redirect detail = %q", got)
	}
	r.Header = http.Header{"Location": {"::bad"}}
	if got := apiErrorFrom("GET", "/x", r).Detail; !strings.Contains(got, "unknown location") {
		t.Errorf("unparseable redirect detail = %q", got)
	}
}

func TestGraphQLAndWriteDetailsAreSanitized(t *testing.T) {
	result, err := classifyWrite("POST", "/x/", resp(200, `{"detail": "ok \u001b[1mbold\u001b[0m ddi_ak_1.zz"}`))
	if err != nil || result.Detail != "ok bold ddi_ak_[REDACTED]" {
		t.Errorf("detail = %q err=%v", result.Detail, err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if d, ok := retryAfter("5", now); !ok || d != 5*time.Second {
		t.Errorf("seconds: %v %v", d, ok)
	}
	if d, ok := retryAfter(now.Add(7*time.Second).UTC().Format(http.TimeFormat), now); !ok || d != 7*time.Second {
		t.Errorf("http-date: %v %v", d, ok)
	}
	if d, ok := retryAfter(now.Add(-time.Hour).UTC().Format(http.TimeFormat), now); !ok || d != 0 {
		t.Errorf("past http-date: %v %v", d, ok)
	}
	for _, bad := range []string{"", "soon", "-3"} {
		if _, ok := retryAfter(bad, now); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestRetryableStatus(t *testing.T) {
	for status, want := range map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true, 501: false, 505: false, 400: false, 404: false} {
		if retryableStatus(status) != want {
			t.Errorf("retryableStatus(%d) = %v", status, !want)
		}
	}
}

func TestConfigRetryDefaults(t *testing.T) {
	if got := (Config{}).withDefaults().MaxRetries; got != DefaultMaxRetries {
		t.Errorf("zero value must mean the default, got %d", got)
	}
	if got := (Config{MaxRetries: 5}).withDefaults().MaxRetries; got != 5 {
		t.Errorf("explicit value kept, got %d", got)
	}
	if got := (Config{MaxRetries: 5, NoRetries: true}).withDefaults().MaxRetries; got != 0 {
		t.Errorf("NoRetries must disable retries, got %d", got)
	}
	if got := (Config{}).withDefaults().MaxResponseBytes; got != DefaultMaxResponse {
		t.Errorf("response limit default, got %d", got)
	}
}
