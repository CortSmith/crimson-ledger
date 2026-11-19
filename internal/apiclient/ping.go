package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"crimsonledger/internal/domain"
)

// PingResult is the answer to POST /v1/pings.
type PingResult struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
	Contacts  int  `json:"contacts"`
	Fixes     int  `json:"fixes"`
	Kills     int  `json:"kills"`
}

// PostPing uploads one batch. Retries resend the same bytes, so the same batchId: the server treats it as idempotent.
func (c *Client) PostPing(ctx context.Context, p domain.Ping) (PingResult, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return PingResult{}, errors.New("could not encode the ping")
	}
	cl := call{method: http.MethodPost, path: "/v1/pings", body: body}
	if len(body) > gzipMinBytes {
		if cl.body, err = gzipBytes(body); err != nil {
			return PingResult{}, errors.New("could not compress the ping")
		}
		cl.gzip = true
	}
	var res PingResult
	_, err = c.do(ctx, cl, &res)
	return res, err
}
