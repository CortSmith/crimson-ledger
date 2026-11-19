package domain

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const testMax = 256 << 10

func TestDecodePing_ExampleRoundTrips(t *testing.T) {
	raw, err := os.ReadFile("testdata/ping_example.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePing(bytes.NewReader(raw), testMax)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePing(p, testNow); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(p)
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(out, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("round trip differs:\n%s\n%s", raw, out)
	}
}

func TestDecodePing_Rejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int64
		want error
	}{
		{"unknown field", `{"batchId":"x","bogus":1}`, testMax, ErrInvalidJSON},
		{"unknown nested field", `{"contacts":[{"guid":"x","extra":1}]}`, testMax, ErrInvalidJSON},
		{"trailing data", `{} {}`, testMax, ErrInvalidJSON},
		{"trailing garbage", `{}x`, testMax, ErrInvalidJSON},
		{"wrong type", `{"sentAt":"now"}`, testMax, ErrInvalidJSON},
		{"overflowing float", `{"contacts":[{"fixes":[{"x":1e999}]}]}`, testMax, ErrInvalidJSON},
		{"not json", `hello`, testMax, ErrInvalidJSON},
		{"empty", ``, testMax, ErrInvalidJSON},
		{"oversize", `{"source":"` + strings.Repeat("a", 100) + `"}`, 50, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodePing(strings.NewReader(tt.in), tt.max); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func TestDecodePing_AtLimitAccepted(t *testing.T) {
	in := `{}`
	if _, err := DecodePing(strings.NewReader(in), int64(len(in))); err != nil {
		t.Fatal(err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestDecodePing_ReadErrorPropagates(t *testing.T) {
	if _, err := DecodePing(errReader{}, 10); err == nil || errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodePing_RejectsGzipBomb(t *testing.T) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zero := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		_, _ = zw.Write(zero)
	}
	_ = zw.Close()
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePing(gz, testMax); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func FuzzDecodePing(f *testing.F) {
	ex, _ := os.ReadFile("testdata/ping_example.json")
	f.Add(ex)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"contacts":[{"fixes":[{"x":1e999,"y":-0}]}]}`))
	f.Add([]byte(`{"kills":null,"charts":[]} trailing`))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := DecodePing(bytes.NewReader(data), testMax)
		if err != nil || ValidatePing(p, testNow) != nil {
			return
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal accepted ping: %v", err)
		}
		q, err := DecodePing(bytes.NewReader(out), testMax*4)
		if err != nil || !reflect.DeepEqual(p, q) {
			t.Fatalf("round trip differs: %v\n%+v\n%+v", err, p, q)
		}
	})
}
