package apiclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"time"
)

// Limits and defaults.
const (
	maxResponseBytes = 4 << 20
	gzipMinBytes     = 1024
	defaultTimeout   = 15 * time.Second
)

// Options configures New. The zero value of every field except BaseURL and APIKey has a sensible default.
type Options struct {
	BaseURL   string        // validated like config server_url
	APIKey    string        // never logged
	CAFile    string        // optional extra root for a self-signed LAN API
	UserAgent string        // "crimson-scout/1.0" or "crimson-ledger/1.0"
	Timeout   time.Duration // per attempt, default 15 s
	Now       func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error // injected for tests
}

// String, GoString and LogValue keep the key out of %v, %+v, %#v and slog output.
func (o Options) String() string {
	return "apiclient.Options{BaseURL:" + o.BaseURL + ", APIKey:[hidden]}"
}

// GoString keeps the key out of %#v.
func (o Options) GoString() string { return o.String() }

// LogValue hides the key from slog.
func (o Options) LogValue() slog.Value {
	return slog.StringValue(o.String())
}

// Client talks to the Crimson Ledger API. It is safe for concurrent use.
type Client struct {
	base  *url.URL
	key   string
	ua    string
	http  *http.Client
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	rnd   func() float64
	cache *etagCache
}

// String never includes the key.
func (c *Client) String() string { return "apiclient.Client{" + c.base.String() + "}" }

// GoString never includes the key.
func (c *Client) GoString() string { return c.String() }

// LogValue hides the key from slog.
func (c *Client) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// New validates o and builds a Client.
func New(o Options) (*Client, error) {
	base, err := newBase(o.BaseURL)
	if err != nil {
		return nil, err
	}
	tc, err := tlsConfig(o.CAFile)
	if err != nil {
		return nil, err
	}
	c := &Client{base: base, key: o.APIKey, ua: o.UserAgent, now: o.Now, sleep: o.Sleep, cache: newETagCache(etagCacheSize),
		rnd: rand.Float64} // #nosec G404 -- jitter, not security
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	c.http = newHTTPClient(base, tc, timeout)
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	return c, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// call describes one request. Its body is bytes so every attempt can replay it.
type call struct {
	method string
	path   string
	query  url.Values
	body   []byte
	gzip   bool
	cached bool // send If-None-Match and serve 304 from the cache
}

func (c *call) key() string {
	if len(c.query) == 0 {
		return c.path
	}
	return c.path + "?" + c.query.Encode() // Encode sorts by key: canonical
}

func (c *Client) build(ctx context.Context, cl call) (*http.Request, error) {
	u := *c.base
	u.Path += cl.path
	u.RawQuery = cl.query.Encode()
	var body io.Reader
	if cl.body != nil {
		body = bytes.NewReader(cl.body)
	}
	req, err := http.NewRequestWithContext(ctx, cl.method, u.String(), body)
	if err != nil {
		return nil, errors.New("could not build the request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.ua)
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	if cl.body != nil {
		req.Header.Set("Content-Type", "application/json")
		if cl.gzip {
			req.Header.Set("Content-Encoding", "gzip")
		}
	}
	if cl.cached {
		if etag, _, ok := c.cache.get(cl.key()); ok {
			req.Header.Set("If-None-Match", etag)
		}
	}
	return req, nil
}

// do runs cl with retries and decodes a 2xx JSON answer into out (nil to ignore the body).
// It reports notModified when a 304 was answered from the cache.
func (c *Client) do(ctx context.Context, cl call, out any) (notModified bool, err error) {
	for attempt := 0; ; attempt++ {
		req, err := c.build(ctx, cl)
		if err != nil {
			return false, err
		}
		resp, err := c.http.Do(req)
		var status int
		var ra string
		if resp != nil {
			status, ra = resp.StatusCode, resp.Header.Get("Retry-After")
		}
		d := classify(status, err, attempt, ra, c.now(), c.rnd)
		if !d.retry {
			return c.finish(cl, resp, err, out)
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
		}
		if err := c.sleep(ctx, d.wait); err != nil {
			return false, err
		}
	}
}

// scrub drops the request URL that net/http puts in its errors: only the cause is shown.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (c *Client) finish(cl call, resp *http.Response, err error, out any) (bool, error) {
	if err != nil {
		return false, fmt.Errorf("cannot reach the server: %w", scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && cl.cached {
		_, body, ok := c.cache.get(cl.key())
		if !ok {
			return false, errors.New("server answered not-modified but nothing is cached")
		}
		return true, decodeJSON(body, out)
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, errorFromBody(resp, body)
	}
	if !isJSON(resp.Header.Get("Content-Type")) {
		return false, ErrNotJSON
	}
	if err := decodeJSON(body, out); err != nil {
		return false, err
	}
	if etag := resp.Header.Get("ETag"); cl.cached && etag != "" {
		c.cache.put(cl.key(), etag, body)
	}
	return false, nil
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the server answer: %w", scrub(err))
	}
	if len(b) > maxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return b, nil
}

func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

// decodeJSON fills out. Unknown fields are allowed (a newer server may add some); wrong types are errors.
func decodeJSON(body []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("the server answer was not in the expected format")
	}
	return nil
}

func errorFromBody(resp *http.Response, body []byte) error {
	var eb struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	if isJSON(resp.Header.Get("Content-Type")) {
		_ = json.Unmarshal(body, &eb)
	}
	rid := eb.Error.RequestID
	if rid == "" {
		rid = resp.Header.Get("X-Request-Id")
	}
	return newAPIError(resp.StatusCode, eb.Error.Code, eb.Error.Message, rid)
}

// Check calls GET /v1/health (the "Test connection" button). It needs no key.
func (c *Client) Check(ctx context.Context) error {
	var h struct {
		OK bool `json:"ok"`
	}
	if _, err := c.do(ctx, call{method: http.MethodGet, path: "/v1/health"}, &h); err != nil {
		return err
	}
	if !h.OK {
		return errors.New("the server did not report itself healthy")
	}
	return nil
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
