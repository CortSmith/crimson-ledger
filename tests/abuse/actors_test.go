package abuse

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

func TestLeaksFindsSecretsAndInternals(t *testing.T) {
	for _, bad := range []string{"clk_v_abcd_xx", "SQLite error", "goroutine 12 [running]", "/home/u/x.go:12", "arn:aws:iam::1:role/x", "nil pointer dereference"} {
		if len(Leaks(Answer{Body: []byte(bad)})) == 0 {
			t.Errorf("%q not detected", bad)
		}
	}
	if l := Leaks(Answer{Body: []byte(`{"error":{"code":"unauthorized","message":"unauthorized","requestId":"ab12"}}`)}); len(l) != 0 {
		t.Errorf("false positive: %v", l)
	}
}

func TestUniformErrorShape(t *testing.T) {
	ok := `{"error":{"code":"not_found","message":"not found","requestId":"ab"}}` + "\n"
	if !IsUniformError(Answer{Body: []byte(ok)}) || IsUniformError(Answer{Body: []byte(`{"error":{"code":"x","message":"y","requestId":"z","trace":"q"}}`)}) {
		t.Fatal("shape check wrong")
	}
}

// PoC 1: the server receives exactly the bytes we send (duplicate headers, // paths, %00 survive).
func TestSendRawSendsExactBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		br := bufio.NewReader(c)
		var sb strings.Builder
		for {
			line, err := br.ReadString('\n')
			sb.WriteString(line)
			if err != nil || line == "\r\n" {
				break
			}
		}
		got <- sb.String()
		_, _ = c.Write([]byte("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"))
	}()
	a, err := SendRaw("http://"+ln.Addr().String(), Raw{Target: "//v1/sweep%00", Headers: [][2]string{{"Authorization", "Bearer a"}, {"Authorization", "Bearer b"}}})
	if err != nil || a.Status != 204 {
		t.Fatalf("%v %+v", err, a)
	}
	s := <-got
	if !strings.HasPrefix(s, "GET //v1/sweep%00 HTTP/1.1\r\n") || strings.Count(s, "Authorization:") != 2 {
		t.Fatalf("request mangled: %q", s)
	}
}
