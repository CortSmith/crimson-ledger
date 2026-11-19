//go:build integration

package abuse

import (
	"strings"
	"testing"
)

func TestX503_OversizeAndHeaderCount(t *testing.T) {
	big := []byte(`{"x":"` + strings.Repeat("a", 1<<20) + `"}`)
	if a := post(t, writer(), big); a.Status != 413 && a.Status != 400 {
		t.Errorf("X503 oversized body: want 413, got %d", a.Status)
	}
	var hs [][2]string
	hs = append(hs, [2]string{"Authorization", "Bearer " + tgt.ViewKey})
	for i := 0; i < 5000; i++ {
		hs = append(hs, [2]string{"X-F" + Itoa(i), "v"})
	}
	a := raw(t, Raw{Target: "/v1/charts", Headers: append(hs, hdrXFF()...)})
	if a.Status == 200 {
		t.Errorf("X503: 5000 extra headers accepted")
	}
	if h := get(t, anon(), "/v1/health"); h.Status != 200 {
		t.Errorf("X503: unhealthy afterwards")
	}
}

func TestX504_WorstCaseSearchIsCapped(t *testing.T) {
	a := get(t, viewer(), "/v1/players?ruleset=rp&q=aa&limit=50")
	if a.Status == 200 && strings.Count(string(a.Body), `"guid"`) > 50 {
		t.Errorf("X504: search returned more than 50 rows")
	}
	if a.Dur.Seconds() > 2 {
		t.Errorf("X504: search took %s", a.Dur)
	}
}
