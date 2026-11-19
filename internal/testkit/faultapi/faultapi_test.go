package faultapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/testkit/faultapi"
)

// result is what one fetch saw; the body is already read and closed.
type result struct {
	status      int
	header      http.Header
	finalURL    *url.URL
	body        []byte
	err         error
	transportOK bool // headers arrived
}

// fetch makes one GET with a 1 s client timeout and reads the whole body.
func fetch(t *testing.T, c *http.Client, base string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/sweep", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return result{err: err}
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return result{status: resp.StatusCode, header: resp.Header, finalURL: resp.Request.URL, body: b, err: err, transportOK: true}
}

func TestFaultAPI_Modes(t *testing.T) {
	for _, tc := range modeCases() {
		t.Run(tc.name, func(t *testing.T) {
			srv := faultapi.New(t, tc.opt)
			tc.check(t, fetch(t, srv.Client(), srv.URL))
		})
	}
}

type modeCase struct {
	name  string
	opt   faultapi.Option
	check func(t *testing.T, r result)
}

func modeCases() []modeCase { //nolint:gocyclo // one flat row per fault mode; splitting would hide the table
	return []modeCase{
		{faultapi.ModeStatus, faultapi.Status(500), func(t *testing.T, r result) {
			must(t, r.err == nil && r.status == 500, "want status 500, got %+v", r)
		}},
		{faultapi.ModeSlowBody, faultapi.SlowBody(3 * time.Second), func(t *testing.T, r result) {
			must(t, r.err != nil, "client with 1s timeout should fail reading the body")
		}},
		{faultapi.ModeSlowHeaders, faultapi.SlowHeaders(3 * time.Second), func(t *testing.T, r result) {
			must(t, r.err != nil && !r.transportOK, "client with 1s timeout should fail waiting for headers")
		}},
		{faultapi.ModeTruncate, faultapi.Truncate(10), func(t *testing.T, r result) {
			must(t, errors.Is(r.err, io.ErrUnexpectedEOF), "want unexpected EOF, got %v", r.err)
			must(t, len(r.body) == 10, "want 10 bytes before the cut, got %d", len(r.body))
		}},
		{faultapi.ModeWrongContentType, faultapi.WrongContentType(), func(t *testing.T, r result) {
			must(t, r.err == nil && strings.HasPrefix(r.header.Get("Content-Type"), "text/html"), "want text/html")
		}},
		{faultapi.ModeHugeBody, faultapi.HugeBody(1 << 20), func(t *testing.T, r result) {
			must(t, r.err == nil && len(r.body) == 1<<20, "want 1 MiB, got %d %v", len(r.body), r.err)
		}},
		{faultapi.ModeRedirect, faultapi.Redirect("/elsewhere"), func(t *testing.T, r result) {
			must(t, r.err == nil && r.finalURL.Path == "/elsewhere", "want to end on /elsewhere, got %+v", r)
		}},
		{faultapi.ModeRetryAfter, faultapi.RetryAfter(7), func(t *testing.T, r result) {
			must(t, r.err == nil && r.status == 429 && r.header.Get("Retry-After") == "7", "want 429 + Retry-After 7")
		}},
		{faultapi.ModeFlaky, faultapi.Flaky(1, 2), func(t *testing.T, r result) {
			must(t, r.err == nil && r.status == 503, "first request of 1-in-2 should fail with 503")
		}},
		{faultapi.ModeSchemaDrift, faultapi.SchemaDrift(), func(t *testing.T, r result) {
			must(t, r.err == nil && json.Valid(r.body) && strings.Contains(string(r.body), "unexpected"), "want valid JSON with unknown fields")
		}},
		{faultapi.ModeEvilStrings, faultapi.EvilStrings(), func(t *testing.T, r result) {
			must(t, r.err == nil && json.Valid(r.body) && strings.Contains(string(r.body), "onerror"), "want valid JSON with hostile names")
		}},
		{faultapi.ModeNaNCoords, faultapi.NaNCoords(), func(t *testing.T, r result) {
			must(t, r.err == nil && !json.Valid(r.body), "NaN body must not be valid JSON")
		}},
		{faultapi.ModeDeepJSON, faultapi.DeepJSON(20000), deepJSONCheck},
		{faultapi.ModeCloseMidBody, faultapi.CloseMidBody(), func(t *testing.T, r result) {
			must(t, errors.Is(r.err, io.ErrUnexpectedEOF), "want unexpected EOF, got %v", r.err)
		}},
	}
}

func deepJSONCheck(t *testing.T, r result) {
	must(t, r.err == nil && len(r.body) == 40000, "want 40000 bytes of brackets, got %d %v", len(r.body), r.err)
	var v any
	must(t, json.Unmarshal(r.body, &v) != nil, "encoding/json must refuse nesting this deep")
}

func TestFaultAPI_TLSBadCertIsRejectedByDefaultClient(t *testing.T) {
	srv := faultapi.New(t, faultapi.TLSBadCert())
	r := fetch(t, &http.Client{}, srv.URL)
	must(t, r.err != nil && strings.Contains(r.err.Error(), "certificate"), "default client must reject the cert, got %v", r.err)
}

func TestFaultAPI_HTTPDowngradeRedirectsToPlainHTTP(t *testing.T) {
	srv := faultapi.New(t, faultapi.HTTPDowngrade())
	r := fetch(t, srv.Client(), srv.URL)
	must(t, r.err == nil && r.finalURL.Scheme == "http", "client followed to %+v", r)
}

func TestFaultAPI_HealthyByDefaultAndCountsRequests(t *testing.T) {
	srv := faultapi.New(t)
	r := fetch(t, srv.Client(), srv.URL)
	must(t, r.err == nil && r.status == 200 && srv.Requests() == 1, "healthy server misbehaved: %+v", r)
	must(t, strings.HasPrefix(srv.URL, "http://127.0.0.1:"), "must bind loopback, got %s", srv.URL)
}

func must(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}
