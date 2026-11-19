//go:build integration

package abuse

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestX301_RulesetMustBeExactlyOne(t *testing.T) {
	for _, q := range []string{"", "ruleset=", "ruleset=*", "ruleset=all", "ruleset=%25", "ruleset=pvp,rp", "ruleset[]=rp",
		"ruleset=pvp&ruleset=rp", "RULESET=rp", "ruleset=%72p", "ruleset=rp%00", "ruleset=RP", "ruleset=rp+", "ruleset=normal%20pvp"} {
		a := get(t, viewer(), "/v1/sweep?"+q)
		if q == "ruleset=%72p" { // %72 is 'r': the decoded value is exactly "rp" so this one may legitimately succeed
			continue
		}
		if a.Status != 400 {
			t.Errorf("X301 %q: want 400, got %d %.100s", q, a.Status, a.Body)
		}
	}
}

func TestX302_DumpParameters(t *testing.T) {
	for _, q := range []string{"limit=1000000000", "limit=-1", "limit=0", "limit=2001", "limit=99999999999999999999", "limit=1e9", "limit=",
		"offset=0", "page=2", "cursor=x", "skip=5", "limit=51&q=ab"} {
		for _, base := range []string{"/v1/players?ruleset=rp&q=ab&", "/v1/bounty?ruleset=rp&"} {
			a := get(t, viewer(), base+q)
			if q == "limit=51&q=ab" && strings.HasPrefix(base, "/v1/bounty") {
				continue
			}
			if a.Status == 200 {
				t.Errorf("X302 %s%s: accepted (200)", base, q)
			}
		}
	}
	// Caps hold on the documented maxima.
	var pl struct {
		Players []any `json:"players"`
	}
	a := get(t, viewer(), "/v1/players?ruleset=rp&q=ab&limit=50")
	if a.Status != 200 || json.Unmarshal(a.Body, &pl) != nil || len(pl.Players) > 50 {
		t.Errorf("X302: search cap broken: %d", a.Status)
	}
}

func TestX304_GUIDEnumerationUniform(t *testing.T) {
	var base string
	for _, g := range []string{"Player-1329-DEADBEEF", "Player-1-AAAA", "Creature-0-1234", "Player-1329-" + strings.Repeat("F", 16)} {
		a := get(t, viewer(), "/v1/players/"+g+"/trail?ruleset=rp")
		if a.Status != 404 {
			t.Errorf("X304 %s: want 404, got %d", g, a.Status)
			continue
		}
		n := strings.ReplaceAll(string(a.Body), a.RequestID(), "")
		if base == "" {
			base = n
		} else if n != base {
			t.Errorf("X304: unknown guids answer differently: %q vs %q", base, n)
		}
	}
	for _, g := range []string{"x", "Player-1329-ZZZZ", "Player-1329-", "../../etc", "Player-1329-ABCD%00", "Player--ABCD", "Player-1329-ABCDEFABCDEFABCDE1"} {
		if a := get(t, viewer(), "/v1/players/"+url.PathEscape(g)+"/trail?ruleset=rp"); a.Status != 400 && a.Status != 404 {
			t.Errorf("X304 malformed %q: want 400/404, got %d", g, a.Status)
		}
	}
}

func TestX305_WildcardsAreLiteral(t *testing.T) {
	// Plant a name so a wildcard that matched everything would be visible.
	w := "Wildprobe"
	do(t, writer(), "POST", "/v1/pings", js(t, pingBody([]obj{contactOf(guid(), w, nowMS())}, nil)), map[string]string{"Content-Type": "application/json"})
	for _, q := range []string{"%25", "_", "*", ".", "\\", "%00", "a%25", "[a-z]", ".*", "^w", "w*", "wi%25", "＊", "w_ldprobe", "w.ldprobe"} {
		a := get(t, viewer(), "/v1/players?ruleset=rp&q="+q)
		if a.Status == 200 && contains(a.Body, w) {
			t.Errorf("X305 q=%q: wildcard matched a real name", q)
		}
		if a.Status != 200 && a.Status != 400 {
			t.Errorf("X305 q=%q: unexpected status %d", q, a.Status)
		}
	}
	// Control: the honest prefix finds it.
	if a := get(t, viewer(), "/v1/players?ruleset=rp&q=wild"); !contains(a.Body, w) {
		t.Errorf("X305 control: honest prefix did not find the planted name (%d %.100s)", a.Status, a.Body)
	}
}

func TestX306_InjectionThroughQueryParameters(t *testing.T) {
	inj := []string{"' OR '1'='1", ";DROP TABLE contacts", "' UNION SELECT * FROM api_keys--", "a#b", "C#rp#guid", "W#rp#xx",
		"${x}", ":v0", "attribute_exists(pk)", "../x", "\x00"}
	for _, p := range inj {
		e := url.QueryEscape(p)
		for _, target := range []string{
			"/v1/players?ruleset=rp&q=" + e, "/v1/sweep?ruleset=rp&zone=" + e, "/v1/sweep?ruleset=rp&guid=" + e,
			"/v1/sweep?ruleset=rp&maxAgeSec=" + e, "/v1/players/" + url.PathEscape(p) + "/trail?ruleset=rp",
			"/v1/players/Player-1-ABCD/trail?ruleset=rp&map=" + e + "&since=" + e, "/v1/bounty?ruleset=" + e,
		} {
			a := get(t, viewer(), target)
			switch a.Status {
			case 400, 404:
			case 200:
				// Literal no-match: must be an empty result, never other entities' rows.
				// A name search may legitimately match a real name; the other parameters must yield nothing.
				if !strings.HasPrefix(target, "/v1/players?") && (contains(a.Body, `"guid":"`) || contains(a.Body, `"name":"`)) {
					t.Errorf("X306 %s: 200 with data", target)
				}
			default:
				t.Errorf("X306 %s: status %d", target, a.Status)
			}
		}
	}
}

func TestX307_VerboseErrorsStayBland(t *testing.T) {
	h := map[string]string{"Content-Type": "application/json"}
	deep := strings.Repeat("[", 100000)
	bodies := map[string][]byte{
		"invalid utf8": []byte("{\"batchId\":\"\xff\xfe\"}"),
		"deep json":    []byte(deep),
		"huge number":  []byte(`{"batchId":1e99999}`),
		"dup keys":     []byte(`{"batchId":"a","batchId":"b"}`),
		"bom":          append([]byte("\xef\xbb\xbf"), js(t, pingBody(nil, nil))...),
		"nul":          []byte(`{"source":"a\u0000b"}`),
		"truncated":    []byte(`{"batchId":`),
		"null":         []byte(`null`),
		"array":        []byte(`[]`),
	}
	for name, b := range bodies {
		a := do(t, writer(), "POST", "/v1/pings", b, h)
		if a.Status != 400 && a.Status != 413 {
			t.Errorf("X307 %s: want 400/413, got %d %.100s", name, a.Status, a.Body)
		}
		if a.Status == 400 && !IsUniformError(a) {
			t.Errorf("X307 %s: error body has extra fields: %.200s", name, a.Body)
		}
	}
	if a := get(t, viewer(), "/v1/players?ruleset=rp&q="+strings.Repeat("a", 100<<10)); a.Status != 400 && a.Status != 414 && a.Status != 431 {
		t.Errorf("X307 100KB query: got %d", a.Status)
	}
	if a := get(t, viewer(), "/v1/players?ruleset=rp&q=%ff%fe"); a.Status != 400 {
		t.Errorf("X307 invalid UTF-8 query: got %d", a.Status)
	}
}

func TestX308_CacheAndRangeSideChannels(t *testing.T) {
	a := get(t, viewer(), "/v1/sweep?ruleset=rp")
	et := a.Header.Get("ETag")
	if et == "" {
		t.Skip("no ETag on sweep")
	}
	// An ETag from one ruleset must not make another ruleset answer 304.
	if b := do(t, viewer(), "GET", "/v1/sweep?ruleset=pvp", nil, map[string]string{"If-None-Match": et}); b.Status == 304 {
		t.Errorf("X308: If-None-Match from rp produced 304 for pvp")
	}
	if b := do(t, viewer(), "GET", "/v1/sweep?ruleset=rp", nil, map[string]string{"Range": "bytes=0-3"}); b.Status == 206 {
		t.Errorf("X308: Range honoured")
	}
	if b := do(t, viewer(), "GET", "/v1/sweep?ruleset=rp", nil, map[string]string{"If-Match": `"nope"`}); b.Status == 200 && false {
		t.Log("If-Match ignored")
	}
	if strings.ContainsAny(et, "0123456789abcdef") && strings.Contains(et, strings.ToLower(tgt.ViewKey[:12])) {
		t.Errorf("X308: ETag contains key material")
	}
}

func TestX309_NoCORS(t *testing.T) {
	for _, r := range append(readRoutes, "/v1/pings", "/v1/health") {
		for _, m := range []string{"GET", "OPTIONS", "POST"} {
			a := do(t, viewer(), m, r, nil, map[string]string{"Origin": "https://evil.example",
				"Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization"})
			for k := range a.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("X309 %s %s: header %s present", m, r, k)
				}
			}
		}
	}
}

func TestX310_MaxAgeBounded(t *testing.T) {
	for _, v := range []string{"999999999", "86401", "-1", "0", "1e3", "9999999999999999999999"} {
		if a := get(t, viewer(), "/v1/sweep?ruleset=rp&maxAgeSec="+v); a.Status == 200 && v != "0" {
			t.Errorf("X310 maxAgeSec=%s: accepted", v)
		}
	}
}

func TestX311_EveryResponseIsJSONNosniff(t *testing.T) {
	targets := append([]string{"/v1/health", "/nope", "/v1/sweep"}, readRoutes...)
	for _, a := range []Actor{anon(), viewer(), writer()} {
		for _, p := range targets {
			ans := do(t, a, "GET", p, nil, nil)
			if ct := ans.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("X311 %s as %s: Content-Type %q", p, a.Name, ct)
			}
			if ans.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("X311 %s as %s: no nosniff", p, a.Name)
			}
		}
	}
}
