package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	maxRetryAfter   = 30 * time.Second
	headerRequestID = "X-Request-ID"
	refreshCookie   = "refresh_token"
)

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	RequestID  string
}

type call struct {
	method string
	url    string
	header http.Header
	body   []byte
}

type transport struct {
	http      *http.Client
	slots     chan struct{}
	userAgent string
	backoff   Backoff
	retries   int
	maxBytes  int64
	secrets   []string
}

func newTransport(cfg Config, rt http.RoundTripper) *transport {
	return &transport{
		http: &http.Client{
			Transport: rt,
			Timeout:   cfg.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		slots:     make(chan struct{}, cfg.MaxConcurrency),
		userAgent: userAgent(cfg),
		backoff:   cfg.RetryBackoff,
		retries:   cfg.MaxRetries,
		maxBytes:  cfg.MaxResponseBytes,
		secrets:   cfg.secretValues(),
	}
}

func userAgent(cfg Config) string {
	version := cfg.ProviderVersion
	if version == "" {
		version = "dev"
	}
	tf := cfg.TerraformVersion
	if tf == "" {
		tf = "unknown"
	}
	return fmt.Sprintf("terraform-provider-omniddi/%s terraform/%s", version, tf)
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (t *transport) roundTrip(ctx context.Context, c call) (*Response, error) {
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	var body io.Reader
	if c.body != nil {
		body = bytes.NewReader(c.body)
	}
	req, err := http.NewRequestWithContext(ctx, c.method, c.url, body)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", c.method, err)
	}
	for name, values := range c.header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	requestID := newRequestID()
	req.Header.Set(headerRequestID, requestID)
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Accept", "application/json")
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	logCtx := MaskSecrets(ctx, t.secrets...)
	tflog.Debug(logCtx, "omniddi request", map[string]any{"method": c.method, "url": req.URL.Path, "request_id": requestID})
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, &transportError{Method: c.method, Path: req.URL.Path, RequestID: requestID, Err: err}
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, t.maxBytes+1))
	if err != nil {
		return nil, &transportError{Method: c.method, Path: req.URL.Path, RequestID: requestID, Err: err}
	}
	if int64(len(payload)) > t.maxBytes {
		return nil, &ResponseTooLargeError{Method: c.method, Path: req.URL.Path, Limit: t.maxBytes, RequestID: requestID}
	}
	tflog.Debug(logCtx, "omniddi response", map[string]any{"status": resp.StatusCode, "request_id": requestID})
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: payload, RequestID: requestID}, nil
}

type transportError struct {
	Method    string
	Path      string
	RequestID string
	Err       error
}

func (e *transportError) Error() string {
	return fmt.Sprintf("%s %s failed: %v [X-Request-ID %s]", e.Method, e.Path, e.Err, e.RequestID)
}

func (e *transportError) Unwrap() error { return e.Err }

func retryableStatus(status int) bool {
	switch status {
	case http.StatusNotImplemented, http.StatusHTTPVersionNotSupported:
		return false
	}
	return status == http.StatusTooManyRequests || status >= 500
}

func retryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(header); err == nil {
		return max(at.Sub(now), 0), true
	}
	return 0, false
}

func (t *transport) delay(attempt int, resp *Response) time.Duration {
	d := t.backoff.Min << attempt
	if d <= 0 || d > t.backoff.Max {
		d = t.backoff.Max
	}
	half := int64(d / 2)
	if half > 0 {
		if n, err := rand.Int(rand.Reader, big.NewInt(half)); err == nil {
			d = time.Duration(half + n.Int64())
		}
	}
	if resp != nil {
		if wait, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			if ra := min(wait, maxRetryAfter); ra > d {
				d = ra
			}
		}
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryTransient(resp *Response, err error) bool {
	if err != nil {
		var te *transportError
		return errors.As(err, &te)
	}
	return retryableStatus(resp.StatusCode)
}

func retryRateLimited(resp *Response, err error) bool {
	return err == nil && resp.StatusCode == http.StatusTooManyRequests
}

func (t *transport) withRetry(ctx context.Context, retryOn func(*Response, error) bool, attempt func() (*Response, error)) (*Response, error) {
	for n := 0; ; n++ {
		resp, err := attempt()
		if retryOn == nil || n >= t.retries || ctx.Err() != nil || !retryOn(resp, err) {
			return resp, err
		}
		if waitErr := sleep(ctx, t.delay(n, resp)); waitErr != nil {
			return resp, err
		}
	}
}
