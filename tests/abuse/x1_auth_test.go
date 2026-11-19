//go:build integration

package abuse

import (
	"regexp"
	"strings"
	"testing"
)

var keyIDRe = regexp.MustCompile(`^clk_([iv])_([A-Za-z0-9]+)_(.+)$`)

func TestX101_NoOrOddAuthorization(t *testing.T) {
	for name, hdrs := range map[string][][2]string{
		"none":         nil,
		"empty bearer": {{"Authorization", "Bearer "}},
		"null":         {{"Authorization", "Bearer null"}},
		"undefined":    {{"Authorization", "Bearer undefined"}},
		"true":         {{"Authorization", "Bearer true"}},
		"zero":         {{"Authorization", "Bearer 0"}},
		"lowercase":    {{"Authorization", "bearer " + tgt.ViewKey + "x"}},
		"basic":        {{"Authorization", "Basic dXNlcjpwYXNz"}},
		"token":        {{"Authorization", "Token " + tgt.ViewKey + "x"}},
		"duplicate":    {{"Authorization", "Bearer bad"}, {"Authorization", "Bearer " + tgt.ViewKey}},
		"20KB":         {{"Authorization", "Bearer " + strings.Repeat("A", 20<<10)}},
	} {
		t.Run(name, func(t *testing.T) {
			a := raw(t, Raw{Target: "/v1/charts", Headers: append(hdrs, hdrXFF()...)})
			if name == "duplicate" {
				// Ambiguity must never authenticate as the second (valid) credential.
				if a.Status == 200 {
					t.Errorf("X101 duplicate Authorization authenticated")
				}
				return
			}
			if a.Status == 200 {
				t.Errorf("X101 %s: got 200 without a valid key", name)
			}
			if a.Status != 401 && a.Status != 400 && a.Status != 431 {
				t.Errorf("X101 %s: want 401/400/431, got %d", name, a.Status)
			}
		})
	}
}

func hdrXFF() [][2]string {
	if tgt.SpoofIP {
		return [][2]string{{"X-Forwarded-For", freshIP()}}
	}
	return nil
}

func TestX102_KeyInWrongPlaceIsIgnored(t *testing.T) {
	k := tgt.ViewKey
	for _, target := range []string{"/v1/charts?key=" + k, "/v1/charts?api_key=" + k, "/v1/charts?access_token=" + k} {
		if a := get(t, anon(), target); a.Status != 401 {
			t.Errorf("X102 %s: want 401, got %d", target[:20], a.Status)
		}
	}
	for _, h := range []map[string]string{{"X-API-Key": k}, {"Cookie": "key=" + k}, {"Cookie": "Authorization=Bearer " + k}} {
		if a := do(t, anon(), "GET", "/v1/charts", nil, h); a.Status != 401 {
			t.Errorf("X102 header %v: want 401, got %d", keysOf(h), a.Status)
		}
	}
	if a := do(t, anon(), "POST", "/v1/pings", js(t, obj{"key": tgt.IngestKey}), map[string]string{"Content-Type": "application/json"}); a.Status != 401 {
		t.Errorf("X102 body key: want 401, got %d", a.Status)
	}
}

func keysOf(m map[string]string) []string {
	var o []string
	for k := range m {
		o = append(o, k)
	}
	return o
}

func TestX103_MangledRealTokens(t *testing.T) {
	m := keyIDRe.FindStringSubmatch(tgt.ViewKey)
	if m == nil {
		t.Skip("view key not in clk_v_<id>_<secret> form")
	}
	scope, id, secret := m[1], m[2], m[3]
	cases := map[string]string{
		"truncated":        tgt.ViewKey[:len(tgt.ViewKey)-1],
		"suffix":           tgt.ViewKey + "A",
		"prefix":           "x" + tgt.ViewKey,
		"scope swapped":    "clk_i_" + id + "_" + secret,
		"upper id":         "clk_" + scope + "_" + strings.ToUpper(id) + "_" + secret,
		"upper secret":     "clk_" + scope + "_" + id + "_" + strings.ToUpper(secret),
		"fullwidth":        "ｃｌｋ_v_" + id + "_" + secret,
		"dotted lookalike": strings.Replace(tgt.ViewKey, "_", "＿", 1),
	}
	for name, tok := range cases {
		if tok == tgt.ViewKey {
			continue // upper-casing a digits-only id changes nothing
		}
		a := do(t, anon().WithKey(name, tok), "GET", "/v1/charts", nil, nil)
		if a.Status == 200 {
			t.Errorf("X103 %s: mangled token authenticated", name)
		}
	}
	// Go's client refuses a NUL byte, so send it raw. (A trailing space is not a case: HTTP strips it, so it is the same token.)
	a := raw(t, Raw{Target: "/v1/charts", Headers: append([][2]string{{"Authorization", "Bearer " + tgt.ViewKey + "\x00"}}, hdrXFF()...)})
	if a.Status == 200 {
		t.Errorf("X103 trailing NUL: mangled token authenticated")
	}
}

func TestX104_InjectionInCredential(t *testing.T) {
	m := keyIDRe.FindStringSubmatch(tgt.ViewKey)
	payloads := []string{"' OR 1=1--", "\" OR \"\"=\"", "#", ":v", "${jndi:ldap://x}", "begins_with(", "../../etc/passwd", "%00", "a#b", "*"}
	var base *Answer
	for _, p := range payloads {
		for _, tok := range []string{"clk_v_" + p + "_" + strings.Repeat("A", 43), p} {
			if m != nil && tok == "" {
				continue
			}
			a := do(t, anon().WithKey("inj", tok), "GET", "/v1/charts", nil, nil)
			if a.Status != 401 {
				t.Errorf("X104 %q: want 401, got %d", tok, a.Status)
				continue
			}
			norm := strings.ReplaceAll(string(a.Body), a.RequestID(), "")
			if base == nil {
				base = &a
				base.Body = []byte(norm)
			} else if string(base.Body) != norm {
				t.Errorf("X104 %q: body differs from other failures: %.160s", tok, a.Body)
			}
		}
	}
}
