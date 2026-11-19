package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"crimsonledger/internal/domain"
)

// SweepParams selects a GET /v1/sweep. Zero values are omitted.
type SweepParams struct {
	Ruleset   domain.Ruleset
	Zone      *int   // map id
	GUID      string // one contact
	MaxAgeSec int
}

// SweepResult is a GET /v1/sweep answer. NotModified is true when the server said 304 and Contacts came from the cache.
type SweepResult struct {
	ServerTime  int64                 `json:"serverTime"`
	Ruleset     domain.Ruleset        `json:"ruleset"`
	Contacts    []domain.SweepContact `json:"contacts"`
	NotModified bool                  `json:"-"`
}

// PlayerSummary is one row of GET /v1/players.
type PlayerSummary struct {
	GUID      string          `json:"guid"`
	Name      string          `json:"name"`
	Reaction  domain.Reaction `json:"reaction"`
	Fixes     int             `json:"fixes"`
	FirstSeen int64           `json:"firstSeen"`
	LastSeen  int64           `json:"lastSeen"`
}

// TrailParams selects a GET /v1/players/{guid}/trail.
type TrailParams struct {
	MapID *int
	Limit int
	Since int64 // epoch ms
}

// TrailMap counts a trail's fixes per map.
type TrailMap struct {
	MapID int    `json:"mapId"`
	Zone  string `json:"zone"`
	Count int    `json:"count"`
}

// Trail is a contact's position history.
type Trail struct {
	GUID  string       `json:"guid"`
	Name  string       `json:"name"`
	Total int          `json:"total"`
	Maps  []TrailMap   `json:"maps"`
	Fixes []domain.Fix `json:"fixes"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) (bool, error) {
	return c.do(ctx, call{method: http.MethodGet, path: path, query: q, cached: true}, out)
}

func setInt(q url.Values, k string, v int) {
	if v > 0 {
		q.Set(k, strconv.Itoa(v))
	}
}

// Sweep reads the live contacts.
func (c *Client) Sweep(ctx context.Context, p SweepParams) (SweepResult, error) {
	q := url.Values{}
	if p.Ruleset != "" {
		q.Set("ruleset", string(p.Ruleset))
	}
	if p.Zone != nil {
		q.Set("zone", strconv.Itoa(*p.Zone))
	}
	if p.GUID != "" {
		q.Set("guid", p.GUID)
	}
	setInt(q, "maxAgeSec", p.MaxAgeSec)
	var r SweepResult
	nm, err := c.get(ctx, "/v1/sweep", q, &r)
	r.NotModified = nm
	return r, err
}

// Players searches names.
func (c *Client) Players(ctx context.Context, rs domain.Ruleset, text string, limit int) ([]PlayerSummary, error) {
	q := url.Values{"q": {text}}
	if rs != "" {
		q.Set("ruleset", string(rs))
	}
	setInt(q, "limit", limit)
	var r struct {
		Players []PlayerSummary `json:"players"`
	}
	_, err := c.get(ctx, "/v1/players", q, &r)
	return r.Players, err
}

// Trail reads one contact's position history.
func (c *Client) Trail(ctx context.Context, rs domain.Ruleset, guid string, f TrailParams) (Trail, error) {
	q := url.Values{}
	if rs != "" {
		q.Set("ruleset", string(rs))
	}
	if f.MapID != nil {
		q.Set("map", strconv.Itoa(*f.MapID))
	}
	setInt(q, "limit", f.Limit)
	if f.Since > 0 {
		q.Set("since", strconv.FormatInt(f.Since, 10))
	}
	var t Trail
	_, err := c.get(ctx, "/v1/players/"+url.PathEscape(guid)+"/trail", q, &t)
	return t, err
}

// Bounty reads the kill ranking.
func (c *Client) Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error) {
	q := url.Values{}
	if rs != "" {
		q.Set("ruleset", string(rs))
	}
	setInt(q, "limit", limit)
	var r struct {
		Ranking []domain.KillTotal `json:"ranking"`
	}
	_, err := c.get(ctx, "/v1/bounty", q, &r)
	return r.Ranking, err
}

// Charts reads the map rectangles.
func (c *Client) Charts(ctx context.Context) ([]domain.Chart, error) {
	var r struct {
		Charts []domain.Chart `json:"charts"`
	}
	_, err := c.get(ctx, "/v1/charts", nil, &r)
	return r.Charts, err
}
