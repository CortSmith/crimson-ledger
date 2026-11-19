// Command t02 proves the risky calls behind internal/domain.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var errTooLarge = errors.New("too large")

func limited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errTooLarge
	}
	return b, nil
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func bomb() {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zero := make([]byte, 1<<20)
	for i := 0; i < 1024; i++ {
		_, _ = zw.Write(zero)
	}
	_ = zw.Close()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	gz, _ := gzip.NewReader(&buf)
	size := buf.Len()
	_, err := limited(gz, 256<<10)
	el := time.Since(start)
	runtime.ReadMemStats(&m1)
	mem := m1.TotalAlloc - m0.TotalAlloc
	fmt.Printf("1 gzip bomb: compressed=%d B err=%v elapsed=%v alloc=%d B %s\n", size, err, el, mem,
		verdict(errors.Is(err, errTooLarge) && el < time.Second && mem < 10<<20))
}

func floats() {
	for _, in := range []string{`{"x":1e999}`, `{"x":-0}`, `{"x":NaN}`, `{"x":1e-400}`} {
		var v struct{ X float64 }
		err := json.Unmarshal([]byte(in), &v)
		fmt.Printf("2 %-14s err=%v x=%v inf=%v nan=%v coordOK=%v\n", in, err != nil, v.X, math.IsInf(v.X, 0), math.IsNaN(v.X),
			err != nil || (!math.IsNaN(v.X) && !math.IsInf(v.X, 0) && v.X >= 0 && v.X <= 100))
	}
}

func runes() {
	fmt.Println("3 rune count:", verdict(utf8.RuneCountInString("Mc Chéeki") == 9))
	fmt.Println("3 control:", verdict(unicode.IsControl('\n') && unicode.IsControl(0x7f)))
	fmt.Println("3 Cf:", verdict(unicode.Is(unicode.Cf, 0x200B) && unicode.Is(unicode.Cf, 0x202E)))
}

var foldTable = map[rune]string{
	'a': "àáâãäåāăą", 'c': "çćĉċč", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥ", 'i': "ìíîïīĭįı", 'j': "ĵ", 'k': "ķ",
	'l': "ĺļľ", 'n': "ñńņň", 'o': "òóôõöōŏő", 'r': "ŕŗř", 's': "śŝşš", 't': "ţť", 'u': "ùúûüūŭůűų", 'w': "ŵ",
	'y': "ýÿŷ", 'z': "źżž",
}

func fold(s string) string {
	m := map[rune]rune{}
	for to, froms := range foldTable {
		for _, r := range froms {
			m[r] = to
		}
	}
	return strings.Map(func(r rune) rune {
		if x, ok := m[r]; ok {
			return x
		}
		return r
	}, strings.ToLower(s))
}

func main() {
	bomb()
	floats()
	runes()
	got := strings.Join(strings.Fields(fold("Élan Vital")), " ")
	fmt.Printf("4 %q %s\n", got, verdict(got == "elan vital"))
}
