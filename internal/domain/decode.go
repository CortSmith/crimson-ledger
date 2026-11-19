package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge is returned when the input exceeds the byte limit.
var ErrTooLarge = errors.New("body too large")

// ErrInvalidJSON wraps every syntax, type, unknown-field or trailing-data failure.
var ErrInvalidJSON = errors.New("invalid ping JSON")

// DecodePing reads at most maxBytes from r and strictly decodes one Ping: unknown
// fields and trailing data are rejected. It does not validate values; call
// ValidatePing on the result. Callers pass the already-decompressed stream, so
// the limit also bounds gzip bombs.
func DecodePing(r io.Reader, maxBytes int64) (Ping, error) {
	var p Ping
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return p, fmt.Errorf("read ping: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return p, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Ping{}, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Ping{}, fmt.Errorf("%w: trailing data", ErrInvalidJSON)
	}
	return p, nil
}
