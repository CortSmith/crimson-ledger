package apiserver

import "net/http"

// route is one row of the route table: method and path pattern, the scope a key needs ("" = no key), and the handler.
type route struct {
	pattern string
	scope   Scope
	handler http.Handler
}

// routes is the table of public endpoints. T06 and T07 add their rows here.
func (s *server) routes() []route {
	return []route{
		{"GET /v1/health", "", http.HandlerFunc(s.health)},
		{"POST /v1/pings", ScopeIngest, http.HandlerFunc(s.handlePing)},
		{"GET /v1/sweep", ScopeView, http.HandlerFunc(s.handleSweep)},
		{"GET /v1/players", ScopeView, http.HandlerFunc(s.handlePlayers)},
		{"GET /v1/players/{guid}/trail", ScopeView, http.HandlerFunc(s.handleTrail)},
		{"GET /v1/bounty", ScopeView, http.HandlerFunc(s.handleBounty)},
		{"GET /v1/charts", ScopeView, http.HandlerFunc(s.handleCharts)},
	}
}
