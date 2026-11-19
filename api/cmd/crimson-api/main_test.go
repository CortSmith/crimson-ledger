package main

import (
	"context"
	"strings"
	"testing"
)

func TestRun_Usage(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no arguments", nil, exitUsage, "usage: crimson-api"},
		{"unknown command", []string{"frobnicate"}, exitUsage, "unknown command"},
		{"help", []string{"help"}, exitOK, "usage: crimson-api"},
		{"keys without subcommand", []string{"keys"}, exitUsage, "usage: crimson-api keys"},
		{"keys unknown subcommand", []string{"keys", "burn"}, exitUsage, "unknown subcommand"},
		{"serve with a stray argument", []string{"serve", "now"}, exitUsage, "takes no arguments"},
		{"serve bad flag", []string{"serve", "--nope"}, exitUsage, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newHome(t, nil)
			io, out, errOut := testIO()
			if code := run(context.Background(), tt.args, io); code != tt.code {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tt.code, errOut.String())
			}
			if all := out.String() + errOut.String(); !strings.Contains(all, tt.want) {
				t.Fatalf("output %q lacks %q", all, tt.want)
			}
		})
	}
}

func TestRun_Version(t *testing.T) {
	code, out, _ := runCmd(t, "version")
	if code != exitOK || !strings.HasPrefix(out, "crimson-api ") {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:1", true},
		{"[::1]:1", true},
		{"localhost:1", true},
		{"0.0.0.0:1", false},
		{":8443", false}, // empty host means every interface
		{"192.168.1.20:8443", false},
		{"my-pc.local:8443", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := isLoopbackAddr(tt.addr)
			if err != nil || got != tt.want {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	if _, err := isLoopbackAddr("no-port"); err == nil {
		t.Fatal("want an error for an address without a port")
	}
}

func TestChooseBindMode(t *testing.T) {
	tests := []struct {
		name        string
		listen      string
		tls, insec  bool
		container   bool
		want        bindMode
		wantRefused bool
	}{
		{"loopback plain", "127.0.0.1:8443", false, false, false, modeLoopback, false},
		{"public without tls", "0.0.0.0:8443", false, false, false, "", true},
		{"public with tls", "0.0.0.0:8443", true, false, false, modeTLS, false},
		{"public insecure-lan", "0.0.0.0:8443", false, true, false, modeInsecureLAN, false},
		{"public in container", "0.0.0.0:8443", false, false, true, modeContainer, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := chooseBindMode(tt.listen, tt.tls, tt.insec, tt.container)
			if (err != nil) != tt.wantRefused || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}
