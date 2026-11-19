package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/config"
	"crimsonledger/internal/store"
)

const (
	maxLabelRunes = 64
	maxExpiry     = 10 * 365 * 24 * time.Hour // a key meant to last longer than this should not expire at all
	day           = 24 * time.Hour
)

const keysUsage = "usage: crimson-api keys create --scope ingest|view --label NAME [--expires 90d] | list | revoke <id> | rotate <id>"

func runKeys(ctx context.Context, args []string, io IO) int {
	if len(args) == 0 {
		fmtLine(io, keysUsage)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create", "list", "revoke", "rotate":
	default:
		return usageFail(io.Stderr, "keys", fmt.Sprintf("unknown subcommand %q\n%s", sub, keysUsage))
	}
	st, pepper, err := openKeyStore()
	if err != nil {
		return fail(io.Stderr, "keys "+sub, err)
	}
	defer func() { _ = st.Close() }()
	kc := keyCommand{ctx: ctx, keys: st.Keys(), pepper: pepper, io: io}
	switch sub {
	case "create":
		return kc.create(rest)
	case "list":
		return kc.list()
	case "revoke":
		return kc.revoke(rest)
	}
	return kc.rotate(rest)
}

func openKeyStore() (store.Store, []byte, error) {
	cfg, err := config.Load("api.json", config.APIDefaults)
	if err != nil {
		return nil, nil, err
	}
	st, err := openStore(cfg)
	if err != nil {
		return nil, nil, err
	}
	pepper, err := config.LoadOrCreatePepper()
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return st, pepper, nil
}

type keyCommand struct {
	ctx    context.Context
	keys   store.KeyStore
	pepper []byte
	io     IO
}

func (c keyCommand) create(args []string) int {
	fs := flag.NewFlagSet("keys create", flag.ContinueOnError)
	scope := fs.String("scope", "", "ingest (logger) or view (viewer)")
	label := fs.String("label", "", "what the key is for, for example my-pc")
	expires := fs.String("expires", "", "lifetime such as 90d or 12h (default: never)")
	if code, ok := parseFlags(fs, args, c.io.Stderr); !ok {
		return code
	}
	ttl, err := parseExpiry(*expires)
	if err != nil {
		return usageFail(c.io.Stderr, "keys create", err.Error())
	}
	if err := validateLabel(*label); err != nil {
		return usageFail(c.io.Stderr, "keys create", err.Error())
	}
	sc := store.Scope(*scope)
	if sc != store.ScopeIngest && sc != store.ScopeView {
		return usageFail(c.io.Stderr, "keys create", "--scope must be ingest or view")
	}
	token, err := c.issue(sc, *label, ttl)
	if err != nil {
		return fail(c.io.Stderr, "keys create", err)
	}
	c.printToken(token)
	return exitOK
}

// issue generates a key, stores its hash and returns the token, which exists nowhere else.
func (c keyCommand) issue(sc store.Scope, label string, ttl time.Duration) (string, error) {
	token, rec, err := apiserver.GenerateKey(sc, c.pepper, c.io.random(), c.io.now(), ttl)
	if err != nil {
		return "", err
	}
	rec.Label = label
	if err := c.keys.Put(c.ctx, rec); err != nil {
		return "", fmt.Errorf("store key: %w", err)
	}
	return token, nil
}

// printToken writes the token alone to stdout, so it can be piped, and the advice to stderr.
func (c keyCommand) printToken(token string) {
	_, _ = fmt.Fprintln(c.io.Stdout, token)
	fmtLine(c.io, "This key is shown once and cannot be recovered. Paste it into the app's key field; do not put it in a command line or a chat.")
}

func (c keyCommand) list() int {
	recs, err := c.keys.List(c.ctx)
	if err != nil {
		return fail(c.io.Stderr, "keys list", err)
	}
	w := tabwriter.NewWriter(c.io.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tSCOPE\tLABEL\tCREATED\tEXPIRES\tDISABLED\tLAST USED")
	for _, r := range recs {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\t%s\n", r.ID, r.Scope, r.Label,
			formatTime(&r.CreatedAt), formatTime(r.ExpiresAt), r.Disabled, formatTime(r.LastUsedAt))
	}
	if err := w.Flush(); err != nil {
		return fail(c.io.Stderr, "keys list", err)
	}
	return exitOK
}

func (c keyCommand) revoke(args []string) int {
	if len(args) != 1 {
		return usageFail(c.io.Stderr, "keys revoke", "needs exactly one key id (see 'crimson-api keys list')")
	}
	if err := c.keys.SetDisabled(c.ctx, args[0], true); err != nil {
		return c.keyFail("keys revoke", args[0], err)
	}
	fmtLine(c.io, "revoked "+args[0]+" (a running server stops accepting it within 30 s)")
	return exitOK
}

func (c keyCommand) rotate(args []string) int {
	if len(args) != 1 {
		return usageFail(c.io.Stderr, "keys rotate", "needs exactly one key id (see 'crimson-api keys list')")
	}
	old, err := c.keys.Get(c.ctx, args[0])
	if err != nil {
		return c.keyFail("keys rotate", args[0], err)
	}
	token, err := c.issue(old.Scope, old.Label, remainingSpan(old))
	if err != nil {
		return fail(c.io.Stderr, "keys rotate", err)
	}
	if err := c.keys.SetDisabled(c.ctx, old.ID, true); err != nil {
		return fail(c.io.Stderr, "keys rotate", fmt.Errorf("new key created but %s is still active: %w", old.ID, err))
	}
	c.printToken(token)
	return exitOK
}

func (c keyCommand) keyFail(command, id string, err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return fail(c.io.Stderr, command, fmt.Errorf("no key with id %q", id))
	}
	return fail(c.io.Stderr, command, err)
}

// remainingSpan is the lifetime a replacement key gets: the same span the old key was issued with.
func remainingSpan(k store.KeyRecord) time.Duration {
	if k.ExpiresAt == nil {
		return 0
	}
	return max(k.ExpiresAt.Sub(k.CreatedAt), 0)
}

// parseExpiry reads "90d", "12h" or any time.ParseDuration text. Empty means never expires.
func parseExpiry(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("--expires %q: use a positive number of days such as 90d", s)
		}
		if n > int(maxExpiry/day) {
			return 0, fmt.Errorf("--expires %q is longer than 10 years; omit it for a key that never expires", s)
		}
		d = time.Duration(n) * day
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d <= 0 {
			return 0, fmt.Errorf("--expires %q: use a duration such as 90d or 12h", s)
		}
	}
	if d > maxExpiry {
		return 0, fmt.Errorf("--expires %q is longer than 10 years; omit it for a key that never expires", s)
	}
	return d, nil
}

func validateLabel(label string) error {
	if label == "" {
		return errors.New("--label is required, for example --label my-pc")
	}
	if len([]rune(label)) > maxLabelRunes {
		return fmt.Errorf("--label is longer than %d characters", maxLabelRunes)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return errors.New("--label must not contain control characters")
		}
	}
	return nil
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}
