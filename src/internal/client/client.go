package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	t        *transport
	auth     *session
	endpoint string
	taskPoll PollConfig
	apprPoll PollConfig
	now      func() time.Time
}

func New(_ context.Context, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if _, err := cfg.validate(); err != nil {
		return nil, err
	}
	rt, err := cfg.httpTransport()
	if err != nil {
		return nil, err
	}
	t := newTransport(cfg, rt)
	return &Client{
		t: t, auth: newSession(t, cfg), endpoint: cfg.Endpoint,
		taskPoll: cfg.TaskPoll, apprPoll: cfg.ApprovalPoll, now: cfg.Now,
	}, nil
}

func (c *Client) NetworkSpaceID() int64 { return c.auth.networkSpaceID() }

func (c *Client) apiURL(path string, query url.Values) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	full := c.endpoint + apiPrefix + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	return full
}

func (c *Client) authed(ctx context.Context, method, target string, body []byte) (*Response, error) {
	header := http.Header{}
	token, err := c.auth.authorize(ctx, header)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.roundTrip(ctx, call{method: method, url: target, header: header, body: body})
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if err := c.auth.reauthenticate(ctx, token); err != nil {
		return nil, err
	}
	header = http.Header{}
	if _, err := c.auth.authorize(ctx, header); err != nil {
		return nil, err
	}
	return c.t.roundTrip(ctx, call{method: method, url: target, header: header, body: body})
}

func (c *Client) send(ctx context.Context, method, target string, body []byte, safe bool) (*Response, error) {
	var retryOn func(*Response, error) bool
	if safe {
		retryOn = retryTransient
	}
	return c.t.withRetry(ctx, retryOn, func() (*Response, error) {
		return c.authed(ctx, method, target, body)
	})
}

func encodeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return payload, nil
}

func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.send(ctx, http.MethodGet, c.apiURL(path, query), nil, true)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiErrorFrom(http.MethodGet, path, resp)
	}
	return decodeInto(resp.Body, out)
}

func (c *Client) Write(ctx context.Context, method, path string, body any) (*WriteResult, error) {
	payload, err := encodeBody(body)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, method, c.apiURL(path, nil), payload, false)
	if err != nil {
		return nil, err
	}
	return classifyWrite(method, path, resp)
}

type HealthStatus struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (c *Client) Health(ctx context.Context) (*HealthStatus, error) {
	target := c.apiURL("/health/", nil)
	resp, err := c.t.withRetry(ctx, retryTransient, func() (*Response, error) {
		return c.t.roundTrip(ctx, call{method: http.MethodGet, url: target})
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiErrorFrom(http.MethodGet, "/health/", resp)
	}
	var out HealthStatus
	if err := decodeInto(resp.Body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func decodeInto(body []byte, out any) error {
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) PollSettings() PollConfig { return c.taskPoll }

func (c *Client) Authenticate(ctx context.Context) error {
	_, err := c.auth.authorize(ctx, http.Header{})
	return err
}
