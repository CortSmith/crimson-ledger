package awswire

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
	"crimsonledger/internal/store/sqlitestore"
)

var (
	testNow    = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	testPepper = bytes.Repeat([]byte{7}, 32)
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openStore(t *testing.T) store.Store {
	t.Helper()
	st, err := sqlitestore.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

type fakeSSM struct {
	val *string
	err error
}

func (f fakeSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: f.val}}, nil
}

func b64(b []byte) *string { s := base64.StdEncoding.EncodeToString(b); return &s }

func TestLoadPepper(t *testing.T) {
	short := b64(make([]byte, 16))
	bad := "not base64!!"
	tests := []struct {
		name  string
		api   fakeSSM
		param string
		ok    bool
	}{
		{"good", fakeSSM{val: b64(testPepper)}, "/p", true},
		{"ssm error", fakeSSM{err: errors.New("denied")}, "/p", false},
		{"no param name", fakeSSM{val: b64(testPepper)}, "", false},
		{"too short", fakeSSM{val: short}, "/p", false},
		{"not base64", fakeSSM{val: &bad}, "/p", false},
		{"no value", fakeSSM{}, "/p", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadPepper(context.Background(), tt.api, tt.param)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if tt.ok && !bytes.Equal(got, testPepper) {
				t.Error("wrong pepper")
			}
			if err != nil && strings.Contains(err.Error(), bad) {
				t.Error("error leaks the parameter value")
			}
		})
	}
}

// issueToken creates a key through the admin handler, like deploy.sh does.
func issueToken(t *testing.T, st store.Store, scope string) string {
	t.Helper()
	a := &Admin{Keys: st.Keys(), Pepper: testPepper, Now: func() time.Time { return testNow }, Log: quiet()}
	res := adminCall(t, a, `{"cmd":"keys.create","scope":"`+scope+`","label":"t"}`)
	if res.Status != 200 || res.Token == "" {
		t.Fatalf("create = %+v", res)
	}
	return res.Token
}

func adminCall(t *testing.T, a *Admin, event string) AdminResponse {
	t.Helper()
	res, err := a.Handle(context.Background(), json.RawMessage(event))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func event(method, path, token, ip string, body []byte, gzipped bool) events.APIGatewayV2HTTPRequest {
	h := map[string]string{"content-type": "application/json"}
	if token != "" {
		h["authorization"] = "Bearer " + token
	}
	ev := events.APIGatewayV2HTTPRequest{
		Version: "2.0", RawPath: path, Headers: h,
		RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
			Method: method, Path: path, SourceIP: ip, Protocol: "HTTP/1.1"}},
	}
	if body != nil {
		if gzipped {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write(body)
			_ = zw.Close()
			body = buf.Bytes()
			h["content-encoding"] = "gzip"
		}
		ev.Body, ev.IsBase64Encoded = base64.StdEncoding.EncodeToString(body), true
	}
	return ev
}

func publicHandler(t *testing.T, st store.Store) PublicFunc {
	t.Helper()
	init := func(context.Context) (Backends, error) {
		return Backends{Store: st, Pepper: testPepper, Limiter: apiserver.NewMemoryLimiter(func() time.Time { return testNow })}, nil
	}
	return Public(init, quiet(), func() time.Time { return testNow })
}

func pingBody(t *testing.T) []byte {
	t.Helper()
	now := testNow.UnixMilli()
	b, err := json.Marshal(domain.Ping{
		BatchID: "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44", Source: "my-pc", Ruleset: domain.RulesetPvP, SentAt: now,
		Contacts: []domain.Contact{{GUID: "Player-1329-0A1B2C3D", Name: "Mc Cheeki", Raw: "Mc-Cheeki-PvP-US",
			Reaction: domain.ReactionHostile, LastSeen: now,
			Fixes: []domain.Fix{{T: now, MapID: 1411, X: 51.2, Y: 40.7, Kind: domain.FixExact}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPublicHandler_EventTable(t *testing.T) {
	st := openStore(t)
	ingest, view := issueToken(t, st, "ingest"), issueToken(t, st, "view")
	h := publicHandler(t, st)
	big := bytes.Repeat([]byte("a"), apiserver.DefaultMaxBodyBytes+10)
	tests := []struct {
		name string
		ev   events.APIGatewayV2HTTPRequest
		want int
	}{
		{"health is open", event("GET", "/v1/health", "", "198.51.100.1", nil, false), 200},
		{"ping plain", event("POST", "/v1/pings", ingest, "198.51.100.2", pingBody(t), false), 200},
		{"ping gzip base64", event("POST", "/v1/pings", ingest, "198.51.100.3", pingBody(t), true), 200},
		{"bad json", event("POST", "/v1/pings", ingest, "198.51.100.4", []byte("{nope"), false), 400},
		{"missing auth", event("POST", "/v1/pings", "", "198.51.100.5", pingBody(t), false), 401},
		{"wrong scope", event("POST", "/v1/pings", view, "198.51.100.6", pingBody(t), false), 403},
		{"oversize", event("POST", "/v1/pings", ingest, "198.51.100.7", big, false), 413},
		{"unknown path", event("GET", "/v1/nothing", view, "198.51.100.8", nil, false), 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := h(context.Background(), tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tt.want {
				t.Fatalf("status %d, want %d, body %s", res.StatusCode, tt.want, res.Body)
			}
		})
	}
}

func TestPublicHandler_ClientIPComesFromSourceIP(t *testing.T) {
	st := openStore(t)
	h := publicHandler(t, st)
	// 10 failed auths from one sourceIP lock that IP; a different sourceIP is unaffected.
	for range 10 {
		_, _ = h(context.Background(), event("GET", "/v1/players", "clk_v_aaaaaaaa_"+strings.Repeat("b", 43), "203.0.113.9", nil, false))
	}
	locked, _ := h(context.Background(), event("GET", "/v1/health", "", "203.0.113.9", nil, false))
	other, _ := h(context.Background(), event("GET", "/v1/health", "", "203.0.113.10", nil, false))
	if locked.StatusCode != 429 || other.StatusCode != 200 {
		t.Fatalf("locked=%d other=%d, want 429 and 200", locked.StatusCode, other.StatusCode)
	}
}

func TestColdStart_FailsClosedWithoutPepper(t *testing.T) {
	calls := 0
	init := func(context.Context) (Backends, error) {
		calls++
		return Backends{}, errors.New("ssm down")
	}
	h := Public(init, quiet(), nil)
	for range 2 {
		res, err := h(context.Background(), event("GET", "/v1/health", "", "198.51.100.1", nil, false))
		if err != nil || res.StatusCode != 503 || res.Headers["Retry-After"] == "" {
			t.Fatalf("res=%+v err=%v, want 503 with Retry-After", res, err)
		}
	}
	if calls != 2 {
		t.Errorf("init ran %d times; a failed init must be retried by the next request", calls)
	}
}

func TestColdStart_RecoversAfterTransientFailure(t *testing.T) {
	st := openStore(t)
	fail := true
	init := func(context.Context) (Backends, error) {
		if fail {
			return Backends{}, errors.New("ssm down")
		}
		return Backends{Store: st, Pepper: testPepper}, nil
	}
	h := Public(init, quiet(), nil)
	if res, _ := h(context.Background(), event("GET", "/v1/health", "", "198.51.100.1", nil, false)); res.StatusCode != 503 {
		t.Fatalf("first = %d", res.StatusCode)
	}
	fail = false
	if res, _ := h(context.Background(), event("GET", "/v1/health", "", "198.51.100.1", nil, false)); res.StatusCode != 200 {
		t.Fatalf("after recovery = %d", res.StatusCode)
	}
}

func newAdmin(st store.Store, logs io.Writer) *Admin {
	return &Admin{Keys: st.Keys(), Pepper: testPepper, Now: func() time.Time { return testNow }, Log: slog.New(slog.NewTextHandler(logs, nil))}
}

func TestAdmin_CreateAndListNeverLeakSecrets(t *testing.T) {
	st := openStore(t)
	var logs bytes.Buffer
	a := newAdmin(st, &logs)
	created := adminCall(t, a, `{"cmd":"keys.create","scope":"view","label":"laptop","expiresDays":30}`)
	if created.Status != 200 || !strings.HasPrefix(created.Token, "clk_v_") || created.ID == "" {
		t.Fatalf("create = %+v", created)
	}
	list := adminCall(t, a, `{"cmd":"keys.list"}`)
	if len(list.Keys) != 1 || list.Keys[0].ID != created.ID || list.Keys[0].ExpiresAt == nil {
		t.Fatalf("list = %+v", list)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), created.Token) || strings.Contains(strings.ToLower(string(raw)), "hash") {
		t.Error("list exposes a secret or a hash")
	}
	if strings.Contains(logs.String(), "clk_") {
		t.Error("a token reached the log")
	}
}

func TestAdmin_RotateAndRevokeDisableKeys(t *testing.T) {
	st := openStore(t)
	var logs bytes.Buffer
	a := newAdmin(st, &logs)
	created := adminCall(t, a, `{"cmd":"keys.create","scope":"ingest","label":"pc"}`)
	rot := adminCall(t, a, `{"cmd":"keys.rotate","id":"`+created.ID+`"}`)
	if rot.Status != 200 || rot.ID == created.ID || rot.Token == "" {
		t.Fatalf("rotate = %+v", rot)
	}
	if old, _ := st.Keys().Get(context.Background(), created.ID); !old.Disabled {
		t.Error("rotate must disable the old key")
	}
	if res := adminCall(t, a, `{"cmd":"keys.revoke","id":"`+rot.ID+`"}`); res.Status != 200 {
		t.Fatalf("revoke = %+v", res)
	}
	if got, _ := st.Keys().Get(context.Background(), rot.ID); !got.Disabled {
		t.Error("revoke must disable the key")
	}
	if strings.Contains(logs.String(), "clk_") {
		t.Error("a token reached the log")
	}
}

func TestAdmin_RejectsBadEvents(t *testing.T) {
	st := openStore(t)
	a := &Admin{Keys: st.Keys(), Pepper: testPepper, Now: func() time.Time { return testNow }, Log: quiet()}
	tests := []struct {
		name, event string
		want        int
	}{
		{"unknown cmd", `{"cmd":"keys.delete"}`, 400},
		{"no cmd", `{}`, 400},
		{"extra field", `{"cmd":"keys.list","admin":true}`, 400},
		{"not json", `nope`, 400},
		{"trailing data", `{"cmd":"keys.list"}{"cmd":"keys.list"}`, 400},
		{"bad scope", `{"cmd":"keys.create","scope":"root","label":"x"}`, 400},
		{"no label", `{"cmd":"keys.create","scope":"view"}`, 400},
		{"control char label", `{"cmd":"keys.create","scope":"view","label":"a\nb"}`, 400},
		{"negative expiry", `{"cmd":"keys.create","scope":"view","label":"x","expiresDays":-1}`, 400},
		{"revoke without id", `{"cmd":"keys.revoke"}`, 400},
		{"revoke unknown", `{"cmd":"keys.revoke","id":"zzzzzzzz"}`, 404},
		{"rotate unknown", `{"cmd":"keys.rotate","id":"zzzzzzzz"}`, 404},
		{"too large", `{"cmd":"keys.list","label":"` + strings.Repeat("a", 5000) + `"}`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res := adminCall(t, a, tt.event); res.Status != tt.want || res.Token != "" {
				t.Fatalf("%+v, want status %d and no token", res, tt.want)
			}
		})
	}
}
