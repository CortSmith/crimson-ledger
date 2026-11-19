package awswire

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"
	"unicode"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/store"
)

const (
	maxLabelRunes  = 64
	maxExpiryDays  = 3650
	maxEventBytes  = 4096
	statusOK       = 200
	statusBad      = 400
	statusNotFound = 404
	statusServer   = 500
)

// AdminRequest is the event of the admin function. Unknown fields are rejected.
type AdminRequest struct {
	Cmd         string `json:"cmd"`
	Scope       string `json:"scope,omitempty"`
	Label       string `json:"label,omitempty"`
	ExpiresDays int    `json:"expiresDays,omitempty"`
	ID          string `json:"id,omitempty"`
}

// AdminKey is one listed key; the hash is never part of it.
type AdminKey struct {
	ID         string     `json:"id"`
	Scope      string     `json:"scope"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Disabled   bool       `json:"disabled"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// AdminResponse is returned to the invoker only. The function's response is not logged by us, and the
// token appears nowhere else.
type AdminResponse struct {
	Status int        `json:"status"`
	Error  string     `json:"error,omitempty"`
	ID     string     `json:"id,omitempty"`
	Token  string     `json:"token,omitempty"`
	Keys   []AdminKey `json:"keys,omitempty"`
}

// Admin handles key management for the IAM-only admin Lambda.
type Admin struct {
	Keys   store.KeyStore
	Pepper []byte
	Now    func() time.Time
	Rand   io.Reader
	Log    *slog.Logger
}

// Handle decodes one raw event and runs it.
func (a *Admin) Handle(ctx context.Context, raw json.RawMessage) (AdminResponse, error) {
	var req AdminRequest
	if len(raw) > maxEventBytes {
		return bad("event too large"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return bad("event is not a valid admin request"), nil
	}
	if dec.More() {
		return bad("event is not a valid admin request"), nil
	}
	switch req.Cmd {
	case "keys.create":
		return a.create(ctx, req), nil
	case "keys.list":
		return a.list(ctx), nil
	case "keys.revoke":
		return a.revoke(ctx, req), nil
	case "keys.rotate":
		return a.rotate(ctx, req), nil
	}
	return bad("unknown cmd; use keys.create, keys.list, keys.revoke or keys.rotate"), nil
}

func (a *Admin) issue(ctx context.Context, sc store.Scope, label string, ttl time.Duration) (AdminResponse, error) {
	rnd := a.Rand
	if rnd == nil {
		rnd = rand.Reader
	}
	token, rec, err := apiserver.GenerateKey(sc, a.Pepper, rnd, a.Now(), ttl)
	if err != nil {
		return AdminResponse{}, err
	}
	rec.Label = label
	if err := a.Keys.Put(ctx, rec); err != nil {
		return AdminResponse{}, err
	}
	return AdminResponse{Status: statusOK, ID: rec.ID, Token: token}, nil
}

func (a *Admin) create(ctx context.Context, req AdminRequest) AdminResponse {
	sc := store.Scope(req.Scope)
	if sc != store.ScopeIngest && sc != store.ScopeView {
		return bad("scope must be ingest or view")
	}
	if err := validateLabel(req.Label); err != nil {
		return bad(err.Error())
	}
	if req.ExpiresDays < 0 || req.ExpiresDays > maxExpiryDays {
		return bad("expiresDays must be 0 (never) to 3650")
	}
	res, err := a.issue(ctx, sc, req.Label, time.Duration(req.ExpiresDays)*24*time.Hour)
	if err != nil {
		return a.fail("create", err)
	}
	return res
}

func (a *Admin) list(ctx context.Context) AdminResponse {
	recs, err := a.Keys.List(ctx)
	if err != nil {
		return a.fail("list", err)
	}
	out := AdminResponse{Status: statusOK, Keys: make([]AdminKey, 0, len(recs))}
	for _, r := range recs {
		out.Keys = append(out.Keys, AdminKey{ID: r.ID, Scope: string(r.Scope), Label: r.Label, CreatedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt, Disabled: r.Disabled, LastUsedAt: r.LastUsedAt})
	}
	return out
}

func (a *Admin) revoke(ctx context.Context, req AdminRequest) AdminResponse {
	if req.ID == "" {
		return bad("id is required")
	}
	if err := a.Keys.SetDisabled(ctx, req.ID, true); err != nil {
		return a.keyFail("revoke", err)
	}
	return AdminResponse{Status: statusOK, ID: req.ID}
}

func (a *Admin) rotate(ctx context.Context, req AdminRequest) AdminResponse {
	if req.ID == "" {
		return bad("id is required")
	}
	old, err := a.Keys.Get(ctx, req.ID)
	if err != nil {
		return a.keyFail("rotate", err)
	}
	var ttl time.Duration
	if old.ExpiresAt != nil {
		ttl = max(old.ExpiresAt.Sub(old.CreatedAt), 0)
	}
	res, err := a.issue(ctx, old.Scope, old.Label, ttl)
	if err != nil {
		return a.fail("rotate", err)
	}
	if err := a.Keys.SetDisabled(ctx, old.ID, true); err != nil {
		a.Log.Error("admin: new key created but old one still active", "cmd", "rotate", "id", old.ID, "err", err.Error())
		return AdminResponse{Status: statusServer, Error: "new key created but the old key is still active; revoke it by id", ID: res.ID, Token: res.Token}
	}
	return res
}

func (a *Admin) keyFail(cmd string, err error) AdminResponse {
	if errors.Is(err, store.ErrNotFound) {
		return AdminResponse{Status: statusNotFound, Error: "no key with that id"}
	}
	return a.fail(cmd, err)
}

// fail logs the cause (never a token: none is in scope here) and returns a generic error.
func (a *Admin) fail(cmd string, err error) AdminResponse {
	a.Log.Error("admin command failed", "cmd", cmd, "err", err.Error())
	return AdminResponse{Status: statusServer, Error: "internal error; see the function log"}
}

func bad(msg string) AdminResponse { return AdminResponse{Status: statusBad, Error: msg} }

func validateLabel(label string) error {
	if label == "" {
		return errors.New("label is required")
	}
	if len([]rune(label)) > maxLabelRunes {
		return errors.New("label is longer than 64 characters")
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return errors.New("label must not contain control characters")
		}
	}
	return nil
}
