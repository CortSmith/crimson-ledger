// Package integration is the T18 suite: one set of tests run unchanged against a real, deployed API
// (local compose/SQLite or the AWS stack). The target comes from the environment only; keys never
// travel through flags. Tests carry the `integration` build tag; this file does not, so the guards
// are unit-testable without a server.
package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Kinds of target.
const (
	KindLocal = "local"
	KindAWS   = "aws"
)

// AckEnv must be "yes" or the suite refuses to run: it writes data.
const AckEnv = "CL_I_UNDERSTAND_THIS_WRITES_DATA"

// Target is the deployment under test.
type Target struct {
	Kind      string // local | aws
	BaseURL   string // no trailing slash
	IngestKey string
	ViewKey   string
	// Optional extras, empty means the dependent tests skip.
	RateURL       string // second instance with the default rate limit (B17)
	RateIngestKey string
	APIBin        string // path of crimson-api (local key lifecycle, A16)
	Home          string // CRIMSON_LEDGER_HOME of that instance
	SpoofIP       bool   // the server trusts X-Forwarded-For (local bootstrap sets it) so tests get a private client IP
}

// stackFile lists the URLs bootstrap_aws.sh deployed, so a pasted production URL fails.
type stackFile struct {
	URLs []string `json:"urls"`
}

// FromEnv builds the Target and applies every safety guard. getenv is os.Getenv in production.
func FromEnv(getenv func(string) string) (Target, error) {
	t := Target{
		Kind: getenv("CL_TARGET"), BaseURL: strings.TrimRight(getenv("CL_URL"), "/"),
		IngestKey: getenv("CL_INGEST_KEY"), ViewKey: getenv("CL_VIEW_KEY"),
		RateURL: strings.TrimRight(getenv("CL_RATE_URL"), "/"), RateIngestKey: getenv("CL_RATE_INGEST_KEY"),
		APIBin: getenv("CL_API_BIN"), Home: getenv("CL_HOME"), SpoofIP: getenv("CL_TRUST_XFF") == "1",
	}
	if getenv(AckEnv) != "yes" {
		return Target{}, fmt.Errorf("refusing to run: set %s=yes (the suite writes test data)", AckEnv)
	}
	if t.Kind != KindLocal && t.Kind != KindAWS {
		return Target{}, errors.New("CL_TARGET must be local or aws")
	}
	if t.IngestKey == "" || t.ViewKey == "" {
		return Target{}, errors.New("CL_INGEST_KEY and CL_VIEW_KEY are required")
	}
	if err := checkTargetURL(t, getenv("CL_IT_STACK")); err != nil {
		return Target{}, err
	}
	return t, nil
}

// checkTargetURL applies the per-kind URL guards: loopback for local, https and a deployed stack for aws.
func checkTargetURL(t Target, stackPath string) error {
	u, err := url.Parse(t.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("CL_URL must be an absolute http(s) URL")
	}
	switch t.Kind {
	case KindLocal:
		if !isLoopback(u.Hostname()) {
			return fmt.Errorf("local target must be a loopback address, got host %q", u.Hostname())
		}
	case KindAWS:
		if u.Scheme != "https" {
			return errors.New("aws target must be https")
		}
		return inStackFile(stackPath, t.BaseURL)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func inStackFile(path, base string) error {
	if path == "" {
		path = ".it-stack.json"
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path chosen by the operator running the suite
	if err != nil {
		return fmt.Errorf("aws target needs %s written by bootstrap_aws.sh: %w", path, err)
	}
	var sf stackFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return fmt.Errorf("%s is not valid: %w", path, err)
	}
	for _, u := range sf.URLs {
		if strings.TrimRight(u, "/") == base {
			return nil
		}
	}
	return fmt.Errorf("%s is not a dedicated test stack listed in %s; refusing to run", base, path)
}
