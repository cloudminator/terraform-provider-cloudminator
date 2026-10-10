// Package client is a small typed HTTP client for the Cloudminator platform API
// (platformd). It knows the uniform resource envelope (ADR 0009), the
// `POST …/{id}:{action}` action style, API-key bearer auth (ADR 0019) and the
// async lifecycle (writes return 202; status.phase converges).
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one platformd endpoint with one API key.
type Client struct {
	baseURL    string
	apiKey     string
	userAgent  string
	httpClient *http.Client

	// MaxRetries bounds retries of transient failures (429, 502, 503, 504 and
	// transport errors). RetryWait is the base backoff.
	MaxRetries int
	RetryWait  time.Duration
}

// New returns a client for baseURL (e.g. https://platform.example.com).
func New(baseURL, apiKey, version string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: must be an absolute URL such as https://platform.example.com", baseURL)
	}
	if version == "" {
		version = "dev"
	}
	return &Client{
		baseURL:    u.String(),
		apiKey:     apiKey,
		userAgent:  "terraform-provider-cloudminator/" + version,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 4,
		RetryWait:  time.Second,
	}, nil
}

// APIError is a non-2xx response. Message is the platform's {"error": "..."}.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// IsConflict reports whether err is a 409 from the API.
func IsConflict(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusConflict
}

// request is one logical API call; it may be sent several times on retry.
type request struct {
	method  string
	path    string
	body    any
	out     any
	headers map[string]string
	// retryUnsafe permits retrying a non-idempotent method. Only set when the
	// call carries an Idempotency-Key, so a replay cannot create a twin.
	retryUnsafe bool
}

func (c *Client) do(ctx context.Context, r request) error {
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		payload = b
	}
	retryable := r.retryUnsafe || r.method == http.MethodGet || r.method == http.MethodDelete || r.method == http.MethodPut
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			wait := c.RetryWait * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return errors.Join(ctx.Err(), lastErr)
			case <-time.After(wait):
			}
		}
		err, transient := c.send(ctx, r, payload)
		if err == nil {
			return nil
		}
		lastErr = err
		if !transient || !retryable || attempt >= c.MaxRetries {
			return err
		}
	}
}

func (c *Client) send(ctx context.Context, r request, payload []byte) (err error, transient bool) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.baseURL+r.path, body)
	if err != nil {
		return err, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return err, false
		}
		return err, true
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err, true
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{StatusCode: resp.StatusCode, Method: r.method, Path: r.path}
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			ae.Message = e.Error
		} else {
			ae.Message = strings.TrimSpace(string(raw))
		}
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return ae, true
		case http.StatusConflict:
			// An earlier attempt with the same Idempotency-Key is still being
			// processed; retrying replays its result once it lands.
			return ae, r.retryUnsafe && strings.Contains(ae.Message, "in progress")
		}
		return ae, false
	}
	if r.out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, r.out); err != nil {
			return fmt.Errorf("decode %s %s response: %w", r.method, r.path, err), false
		}
	}
	return nil, false
}

// Get performs GET path and decodes into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, request{method: http.MethodGet, path: path, out: out})
}

// Post performs POST path with body and decodes into out. Not retried.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPost, path: path, body: body, out: out})
}

// Patch performs PATCH path with body and decodes into out. Not retried.
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPatch, path: path, body: body, out: out})
}

// Delete performs DELETE path.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: path})
}

// PostIdempotent performs a create with a fresh Idempotency-Key that is reused
// across this call's retries, so a retried create replays rather than duplicates.
func (c *Client) PostIdempotent(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{
		method: http.MethodPost, path: path, body: body, out: out,
		headers:     map[string]string{"Idempotency-Key": newIdempotencyKey()},
		retryUnsafe: true,
	})
}

func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "tf-" + hex.EncodeToString(b[:])
}

// esc path-escapes one segment.
func esc(s string) string { return url.PathEscape(s) }
